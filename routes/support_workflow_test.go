package routes_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
)

func TestPersistedSupportAdminRidesAndRideBoundRatings(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DisableLegacyRatings = true
	cfg.StaffRequire2FAForAdmin = false
	passenger, err := s.CreateUser("Passenger", uuid.NewString()+"@support.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := s.CreateUser("Driver", uuid.NewString()+"@support.test", "unused", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := s.CreateUser("Outsider", uuid.NewString()+"@support.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	prepareRideDriver(t, s, driver.ID)
	if err := s.DB().Model(&models.DriverProfile{}).Where("user_id = ?", driver.ID).Updates(map[string]any{"is_online": true, "verification_status": models.Approved, "vehicle_type": "car"}).Error; err != nil {
		t.Fatal(err)
	}
	support, err := s.CreateStaffUser("Support", uuid.NewString()+"@support.test", "unused", models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := s.CreateStaffUser("Admin", uuid.NewString()+"@support.test", "unused", models.StaffRoleAdmin, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	passengerToken, _ := testutil.AppToken(s, passenger.ID, passenger.Role, cfg.JWTSecret, time.Hour)
	driverToken, _ := testutil.AppToken(s, driver.ID, driver.Role, cfg.JWTSecret, time.Hour)
	outsiderToken, _ := testutil.AppToken(s, outsider.ID, outsider.Role, cfg.JWTSecret, time.Hour)
	supportToken, _ := testutil.StaffToken(s, support.ID, support.Role, cfg.StaffJWTSecret, cfg.StaffAccessTTL)
	adminToken, _ := testutil.StaffToken(s, admin.ID, admin.Role, cfg.StaffJWTSecret, cfg.StaffAccessTTL)
	request := func(method, path, token string, payload any, want int) map[string]any {
		t.Helper()
		var body *bytes.Reader
		if payload == nil {
			body = bytes.NewReader(nil)
		} else {
			encoded, _ := json.Marshal(payload)
			body = bytes.NewReader(encoded)
		}
		req := httptest.NewRequest(method, "/api/v1"+path, body)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&result)
		if resp.StatusCode != want {
			t.Fatalf("%s %s expected %d got %d: %v", method, path, want, resp.StatusCode, result)
		}
		return result
	}
	ticket := request("POST", "/support/tickets", passengerToken, models.CreateSupportTicketInput{Subject: "Trip issue", Description: "Please review this trip", Category: "ride"}, 201)
	ticketID := ticket["id"].(string)
	request("GET", "/support/tickets", passengerToken, nil, 200)
	request("GET", "/admin/tickets", supportToken, nil, 200)
	request("PATCH", "/admin/tickets/"+ticketID, supportToken, map[string]any{"action": "assign"}, 200)
	request("PATCH", "/admin/tickets/"+ticketID, supportToken, map[string]any{"action": "note", "note": "Checked trip details"}, 200)
	request("PATCH", "/admin/tickets/"+ticketID, supportToken, map[string]any{"action": "update", "status": "resolved", "resolution_outcome": "refunded_offline", "resolution_note": "Explained the fare review result", "reason": "Fare reviewed with the passenger"}, 200)
	detail := request("GET", "/admin/tickets/"+ticketID, adminToken, nil, 200)
	if detail["ticket"].(map[string]any)["status"] != "resolved" || len(detail["notes"].([]any)) != 1 {
		t.Fatalf("ticket workflow was not persisted: %v", detail)
	}
	stats := request("GET", "/admin/stats", supportToken, nil, 200)
	if stats["operational_counts_available"] != true || stats["open_tickets"].(float64) != 0 {
		t.Fatalf("dashboard counts are not live: %v", stats)
	}

	now := time.Now().UTC()
	activeRide := models.Ride{ID: uuid.NewString(), PassengerID: passenger.ID, DriverID: driver.ID, Status: "assigned", Pickup: "Central", Dropoff: "Market", PickupLat: -6.8, PickupLng: 39.2, DropoffLat: -6.81, DropoffLng: 39.2, VehicleType: "car", CreatedAt: now, UpdatedAt: now}
	if err := s.DB().Create(&activeRide).Error; err != nil {
		t.Fatal(err)
	}
	adminRides := request("GET", "/admin/rides?status=assigned&q=Central", supportToken, nil, 200)
	if adminRides["total"].(float64) != 1 {
		t.Fatalf("admin ride filter did not return the ride: %v", adminRides)
	}
	request("POST", "/admin/rides/"+activeRide.ID+"/cancel", supportToken, map[string]string{"reason": "Safety review"}, 200)
	adminDetail := request("GET", "/admin/rides/"+activeRide.ID, adminToken, nil, 200)
	if adminDetail["status"] != "cancelled_by_admin" || len(adminDetail["events"].([]any)) == 0 {
		t.Fatalf("admin ride cancellation was not persisted: %v", adminDetail)
	}
	cancelEvent := adminDetail["events"].([]any)[len(adminDetail["events"].([]any))-1].(map[string]any)
	if cancelEvent["type"] != "cancelled_by_staff" || cancelEvent["actor_type"] != "support" {
		t.Fatalf("support ride cancellation has incorrect event attribution: %v", cancelEvent)
	}
	reassignRide := models.Ride{ID: uuid.NewString(), PassengerID: passenger.ID, DriverID: models.UnassignedRideDriverID, Status: "searching", Pickup: "Central", Dropoff: "Market", PickupLat: -6.8, PickupLng: 39.2, DropoffLat: -6.81, DropoffLng: 39.2, VehicleType: "car", CreatedAt: now, UpdatedAt: now}
	if err := s.DB().Create(&reassignRide).Error; err != nil {
		t.Fatal(err)
	}
	request("POST", "/admin/rides/"+reassignRide.ID+"/reassign", supportToken, map[string]string{"driver_id": driver.ID, "reason": "Original driver unavailable"}, 200)

	completed := models.Ride{ID: uuid.NewString(), PassengerID: passenger.ID, DriverID: driver.ID, Status: "completed", CreatedAt: now, UpdatedAt: now}
	if err := s.DB().Create(&completed).Error; err != nil {
		t.Fatal(err)
	}
	rating := request("POST", "/rides/"+completed.ID+"/rating", passengerToken, map[string]any{"score": 5, "comment": "Good trip"}, 201)
	ratingID := rating["id"].(string)
	request("POST", "/rides/"+completed.ID+"/rating", passengerToken, map[string]any{"score": 4}, 409)
	request("POST", "/rides/"+completed.ID+"/rating", outsiderToken, map[string]any{"score": 5}, 403)
	request("POST", "/ratings/", passengerToken, map[string]any{"to_user_id": driver.ID, "score": 4}, 410)
	public := request("GET", "/ratings/user/"+driver.ID, passengerToken, nil, 200)
	if public["total_ratings"].(float64) != 1 {
		t.Fatalf("ride rating missing from public ratings: %v", public)
	}
	request("GET", "/admin/ratings", supportToken, nil, 403)
	request("POST", "/admin/ratings/"+ratingID+"/hide", adminToken, map[string]string{"reason": "Abusive content"}, 200)
	public = request("GET", "/ratings/user/"+driver.ID, passengerToken, nil, 200)
	if public["total_ratings"].(float64) != 0 {
		t.Fatalf("hidden rating remained public: %v", public)
	}
	request("POST", "/admin/ratings/"+ratingID+"/restore", adminToken, nil, 400)
	queue := request("GET", "/admin/ratings?hidden=true&user="+driver.ID+"&q=good&max_score=5", adminToken, nil, 200)
	if queue["total"].(float64) != 1 {
		t.Fatalf("hidden rating missing from moderation queue: %v", queue)
	}
	rows := queue["ratings"].([]any)
	if rows[0].(map[string]any)["hidden"] != true {
		t.Fatalf("rating visibility flag missing from moderation response: %v", rows[0])
	}
	request("POST", "/admin/ratings/"+ratingID+"/restore", adminToken, map[string]string{"reason": "Review completed"}, 200)
	request("POST", "/rides/"+completed.ID+"/rating", driverToken, map[string]any{"score": 5}, 201)
}
