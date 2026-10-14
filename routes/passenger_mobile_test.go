package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/storage"
	"ride-backend/testutil"
)

func mobileJSONRequest(t *testing.T, app *fiber.App, method, path, token, key string, payload any, want int) map[string]any {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, "/api/v1"+path, body)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	result := map[string]any{}
	if res.StatusCode != 204 {
		_ = json.NewDecoder(res.Body).Decode(&result)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s: want %d, got %d: %#v", method, path, want, res.StatusCode, result)
	}
	return result
}

func TestPassengerMobilePlacesPreferencesQuotesAndSchedules(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg.RideBaseFareTZS, cfg.RidePerKmFareTZS, cfg.RidePerMinuteFareTZS = 1000, 500, 100
	cfg.RideEstimatedSpeedKMPH = 30
	passenger, _ := s.CreateUser("Passenger", uuid.NewString()+"@test.invalid", "unused", models.RolePassenger)
	other, _ := s.CreateUser("Other", uuid.NewString()+"@test.invalid", "unused", models.RolePassenger)
	driver, _ := s.CreateUser("Driver", uuid.NewString()+"@test.invalid", "unused", models.RoleDriver)
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	ownerToken, _ := testutil.AppToken(s, passenger.ID, passenger.Role, cfg.JWTSecret, time.Hour)
	otherToken, _ := testutil.AppToken(s, other.ID, other.Role, cfg.JWTSecret, time.Hour)
	driverToken, _ := testutil.AppToken(s, driver.ID, driver.Role, cfg.JWTSecret, time.Hour)
	mobileJSONRequest(t, app, "GET", "/passenger/saved-places", "", "", nil, 401)
	mobileJSONRequest(t, app, "GET", "/passenger/saved-places", driverToken, "", nil, 403)
	place := models.SavedPlaceInput{Kind: "home", Label: "Home", Address: "Mbezi Beach", Lat: -6.72, Lng: 39.22}
	created := mobileJSONRequest(t, app, "POST", "/passenger/saved-places", ownerToken, "", place, 201)
	id := created["id"].(string)
	mobileJSONRequest(t, app, "POST", "/passenger/saved-places", ownerToken, "", place, 409)
	if got := mobileJSONRequest(t, app, "GET", "/passenger/saved-places", otherToken, "", nil, 200)["places"].([]any); len(got) != 0 {
		t.Fatal("another passenger can see saved places")
	}
	mobileJSONRequest(t, app, "PUT", "/passenger/saved-places/"+id, otherToken, "", place, 404)
	mobileJSONRequest(t, app, "DELETE", "/passenger/saved-places/"+id, otherToken, "", nil, 404)
	prefs := mobileJSONRequest(t, app, "GET", "/passenger/preferences", ownerToken, "", nil, 200)
	if prefs["language"] != "en" || prefs["ride_updates"] != true {
		t.Fatalf("unexpected defaults: %#v", prefs)
	}
	prefs = mobileJSONRequest(t, app, "PATCH", "/passenger/preferences", ownerToken, "", map[string]any{"ride_updates": false, "language": "sw"}, 200)
	if prefs["language"] != "sw" || prefs["ride_updates"] != false {
		t.Fatalf("preference patch did not retain false: %#v", prefs)
	}
	if mobileJSONRequest(t, app, "GET", "/passenger/preferences", otherToken, "", nil, 200)["language"] != "en" {
		t.Fatal("preferences crossed account boundary")
	}
	trip := models.CreateRideInput{Pickup: models.RidePoint{Address: "Mbezi", Lat: -6.72, Lng: 39.22}, Dropoff: models.RidePoint{Address: "City", Lat: -6.82, Lng: 39.28}, VehicleType: "car"}
	quote := mobileJSONRequest(t, app, "POST", "/rides/quote", ownerToken, "", trip, 200)
	if quote["estimated_fare_tzs"].(float64) <= 0 || quote["distance_basis"] != "straight_line" || quote["payment_method"] != "cash" {
		t.Fatalf("unexpected quote: %#v", quote)
	}
	when := time.Now().UTC().Add(20 * time.Minute).Truncate(time.Second)
	schedule := models.ScheduledRideInput{Pickup: trip.Pickup, Dropoff: trip.Dropoff, VehicleType: trip.VehicleType, ScheduledFor: when}
	first := mobileJSONRequest(t, app, "POST", "/scheduled-rides", ownerToken, "schedule-key-001", schedule, 201)
	repeated := mobileJSONRequest(t, app, "POST", "/scheduled-rides", ownerToken, "schedule-key-001", schedule, 201)
	if first["id"] != repeated["id"] {
		t.Fatal("schedule retry created a second row")
	}
	if mobileJSONRequest(t, app, "GET", "/scheduled-rides?status=upcoming", ownerToken, "", nil, 200)["total"] != float64(1) {
		t.Fatal("upcoming schedule filter lost the booking")
	}
	schedule.Dropoff.Address = "Different"
	mobileJSONRequest(t, app, "POST", "/scheduled-rides", ownerToken, "schedule-key-001", schedule, 409)
	scheduleID := first["id"].(string)
	mobileJSONRequest(t, app, "GET", "/scheduled-rides/"+scheduleID, otherToken, "", nil, 404)
	result := mobileJSONRequest(t, app, "POST", "/scheduled-rides/"+scheduleID+"/cancel", ownerToken, "", nil, 200)
	if result["status"] != "cancelled" {
		t.Fatalf("cancel did not persist: %#v", result)
	}
}

func TestScheduledDispatchAndCurrentDriverLocation(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg.RideBaseFareTZS, cfg.RidePerKmFareTZS, cfg.RidePerMinuteFareTZS = 1000, 500, 100
	cfg.RideEstimatedSpeedKMPH, cfg.RideSearchRadiusKM, cfg.RideOfferTimeoutSeconds = 30, 10, 30
	passenger, _ := s.CreateUser("Passenger", uuid.NewString()+"@test.invalid", "unused", models.RolePassenger)
	other, _ := s.CreateUser("Other", uuid.NewString()+"@test.invalid", "unused", models.RolePassenger)
	driver, _ := s.CreateUser("Driver", uuid.NewString()+"@test.invalid", "unused", models.RoleDriver)
	clock := time.Now().UTC()
	s.Clock = func() time.Time { return clock }
	input := models.ScheduledRideInput{Pickup: models.RidePoint{Address: "A", Lat: -6.72, Lng: 39.22}, Dropoff: models.RidePoint{Address: "B", Lat: -6.82, Lng: 39.28}, ScheduledFor: clock.Add(10 * time.Minute)}
	scheduled, err := s.CreateScheduledRide(context.Background(), passenger.ID, "schedule-key-002", input)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(11 * time.Minute)
	if err := s.DispatchDueScheduledRides(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := s.DispatchDueScheduledRides(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	got, err := s.ScheduledRide(context.Background(), passenger.ID, scheduled.ID)
	if err != nil || got.RideID == nil || got.Status != "failed" || got.FailureCode != "no_driver_found" {
		t.Fatalf("no-driver dispatch outcome lost: %+v, %v", got, err)
	}
	var count int64
	if err := s.DB().Model(&models.Ride{}).Where("passenger_id = ?", passenger.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("schedule replay created %d rides: %v", count, err)
	}
	activeRide := models.Ride{ID: uuid.NewString(), PassengerID: passenger.ID, DriverID: driver.ID, Status: "assigned", Pickup: "A", Dropoff: "B", CreatedAt: clock, UpdatedAt: clock}
	if err := s.DB().Create(&activeRide).Error; err != nil {
		t.Fatal(err)
	}
	point := models.DriverLocation{DriverID: driver.ID, Lat: -6.75, Lng: 39.25, Accuracy: 12, UpdatedAt: clock}
	if err := s.DB().Create(&point).Error; err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	ownerToken, _ := testutil.AppToken(s, passenger.ID, passenger.Role, cfg.JWTSecret, time.Hour)
	otherToken, _ := testutil.AppToken(s, other.ID, other.Role, cfg.JWTSecret, time.Hour)
	path := "/rides/" + activeRide.ID + "/driver-location"
	if mobileJSONRequest(t, app, "GET", path, ownerToken, "", nil, 200)["lat"] != point.Lat {
		t.Fatal("current driver point not returned")
	}
	mobileJSONRequest(t, app, "GET", path, otherToken, "", nil, 404)
	clock = clock.Add(3 * time.Minute)
	mobileJSONRequest(t, app, "GET", path, ownerToken, "", nil, 204)
}

func TestTicketConversationAndPrivateAttachmentScope(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	privateRoot := t.TempDir()
	local, err := storage.NewLocal(storage.LocalConfig{PublicDir: filepath.Join(privateRoot, "public"), PrivateDir: filepath.Join(privateRoot, "private")})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	s.Storage = local
	owner, _ := s.CreateUser("Owner", uuid.NewString()+"@test.invalid", "unused", models.RolePassenger)
	other, _ := s.CreateUser("Other", uuid.NewString()+"@test.invalid", "unused", models.RolePassenger)
	staff, err := s.CreateStaffUser("Support", uuid.NewString()+"@test.invalid", "unused", models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	ownerToken, _ := testutil.AppToken(s, owner.ID, owner.Role, cfg.JWTSecret, time.Hour)
	otherToken, _ := testutil.AppToken(s, other.ID, other.Role, cfg.JWTSecret, time.Hour)
	staffToken, _ := testutil.StaffToken(s, staff.ID, staff.Role, cfg.StaffJWTSecret, cfg.StaffAccessTTL)
	ticket := mobileJSONRequest(t, app, "POST", "/support/tickets", ownerToken, "", models.CreateSupportTicketInput{Subject: "Trip issue", Description: "Help me", Category: "ride"}, 201)
	id := ticket["id"].(string)
	mobileJSONRequest(t, app, "GET", "/support/tickets/"+id, otherToken, "", nil, 404)
	mobileJSONRequest(t, app, "POST", "/support/tickets/"+id+"/messages", otherToken, "message-key-001", map[string]string{"body": "No access"}, 404)
	mobileJSONRequest(t, app, "POST", "/support/tickets/"+id+"/messages", ownerToken, "message-key-002", map[string]string{"body": "More details"}, 201)
	mobileJSONRequest(t, app, "POST", "/admin/tickets/"+id+"/messages", staffToken, "message-key-003", map[string]string{"body": "We are checking"}, 201)
	if err := s.DB().Create(&models.SupportTicketNote{ID: uuid.NewString(), TicketID: id, StaffID: staff.ID, Note: "private internal note", CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	conversation := mobileJSONRequest(t, app, "GET", "/support/tickets/"+id, ownerToken, "", nil, 200)
	if conversation["messages_total"] != float64(2) || strings.Contains(strings.ToLower(strings.TrimSpace(conversation["ticket"].(map[string]any)["description"].(string))), "private") {
		t.Fatalf("unexpected conversation: %#v", conversation)
	}
	if _, exists := conversation["notes"]; exists {
		t.Fatal("internal staff notes exposed to passenger")
	}
	if err := s.DB().Model(&models.SupportTicket{}).Where("id = ?", id).Update("status", "resolved").Error; err != nil {
		t.Fatal(err)
	}
	mobileJSONRequest(t, app, "POST", "/support/tickets/"+id+"/messages", ownerToken, "message-key-004", map[string]string{"body": "Still need help"}, 201)
	if mobileJSONRequest(t, app, "GET", "/support/tickets/"+id, ownerToken, "", nil, 200)["ticket"].(map[string]any)["status"] != "open" {
		t.Fatal("passenger reply did not reopen ticket")
	}
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, _ := writer.CreateFormFile("file", "receipt.pdf")
	pdf := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n%%EOF\n")
	_, _ = part.Write(pdf)
	_ = writer.Close()
	req := httptest.NewRequest("POST", "/api/v1/support/tickets/"+id+"/attachments", &upload)
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	req.Header.Set("Idempotency-Key", "attachment-key-001")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var attachment map[string]any
	if err := json.NewDecoder(res.Body).Decode(&attachment); err != nil || res.StatusCode != 201 {
		t.Fatalf("attachment upload: %d %v %v", res.StatusCode, attachment, err)
	}
	filePath := "/support/tickets/" + id + "/attachments/" + attachment["id"].(string)
	visible, _ := json.Marshal(mobileJSONRequest(t, app, "GET", "/support/tickets/"+id, ownerToken, "", nil, 200))
	if bytes.Contains(visible, []byte("support/tickets/")) {
		t.Fatal("private object key exposed in ticket response")
	}
	for _, tc := range []struct {
		path  string
		token string
		want  int
	}{{filePath, otherToken, 404}, {filePath, ownerToken, 200}, {"/admin/tickets/" + id + "/attachments/" + attachment["id"].(string), staffToken, 200}} {
		read := httptest.NewRequest(http.MethodGet, "/api/v1"+tc.path, nil)
		read.Header.Set("Authorization", "Bearer "+tc.token)
		response, err := app.Test(read)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != tc.want || tc.want == 200 && !bytes.Equal(data, pdf) {
			t.Fatalf("attachment access: want %d got %d", tc.want, response.StatusCode)
		}
	}
}
