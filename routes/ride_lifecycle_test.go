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
	"ride-backend/store"
	"ride-backend/testutil"
)

func TestRideLifecycleAndParticipantScope(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg.RideBaseFareTZS, cfg.RidePerKmFareTZS, cfg.RidePerMinuteFareTZS = 1000, 500, 100
	cfg.RideEstimatedSpeedKMPH, cfg.RideOfferTimeoutSeconds, cfg.RideSearchRadiusKM = 30, 30, 10
	passenger, err := s.CreateUser("Passenger", uuid.NewString()+"@test.invalid", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser("Other", uuid.NewString()+"@test.invalid", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := s.CreateUser("Driver", uuid.NewString()+"@test.invalid", "unused", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	prepareRideDriver(t, s, driver.ID)
	if err := s.DB().Model(&models.DriverProfile{}).Where("user_id = ?", driver.ID).Updates(map[string]any{"is_online": true, "verification_status": models.Approved, "vehicle_type": "car"}).Error; err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	passengerToken, _ := testutil.AppToken(s, passenger.ID, passenger.Role, cfg.JWTSecret, time.Hour)
	otherToken, _ := testutil.AppToken(s, other.ID, other.Role, cfg.JWTSecret, time.Hour)
	driverToken, _ := testutil.AppToken(s, driver.ID, driver.Role, cfg.JWTSecret, time.Hour)
	request := func(method, path, token, idem string, payload any, want int) map[string]any {
		t.Helper()
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if idem != "" {
			req.Header.Set("Idempotency-Key", idem)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&result)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: expected %d, got %d: %v", method, path, want, resp.StatusCode, result)
		}
		return result
	}
	request("POST", "/driver/location", driverToken, "location-key-1", map[string]float64{"lat": -6.8, "lng": 39.2, "accuracy": 10}, 200)
	payload := map[string]any{"pickup": map[string]any{"lat": -6.8, "lng": 39.2, "address": "Pickup"}, "dropoff": map[string]any{"lat": -6.81, "lng": 39.2, "address": "Dropoff"}, "vehicle_type": "car"}
	created := request("POST", "/rides", passengerToken, "create-ride-001", payload, 201)
	if created["status"] != "offered" || created["estimated_fare"] == nil || created["driver"] == nil {
		t.Fatalf("ride was not offered with a server fare: %v", created)
	}
	id := created["id"].(string)
	request("POST", "/rides", passengerToken, "create-ride-002", payload, 409)
	replay := request("POST", "/rides", passengerToken, "create-ride-001", payload, 201)
	if replay["id"] != id {
		t.Fatalf("idempotent create returned another ride: %v", replay)
	}
	changed := map[string]any{"pickup": map[string]any{"lat": -6.8, "lng": 39.2, "address": "Changed"}, "dropoff": map[string]any{"lat": -6.81, "lng": 39.2, "address": "Dropoff"}, "vehicle_type": "car"}
	request("POST", "/rides", passengerToken, "create-ride-001", changed, 409)
	request("GET", "/rides/"+id, otherToken, "", nil, 404)
	request("POST", "/rides/"+id+"/accept", driverToken, "accept-ride-001", nil, 200)
	request("POST", "/rides/"+id+"/arrived", driverToken, "arrive-ride-001", nil, 200)
	request("POST", "/rides/"+id+"/start", driverToken, "start-ride-001", nil, 200)
	completed := request("POST", "/rides/"+id+"/complete", driverToken, "complete-ride-001", map[string]int64{"final_fare": 1}, 200)
	if completed["status"] != "completed" || completed["final_fare"] == nil {
		t.Fatalf("ride was not completed with fare: %v", completed)
	}
	if completed["final_fare"].(float64) == 1 {
		t.Fatalf("client supplied fare was trusted: %v", completed)
	}
	if _, exists := completed["fare"]; exists {
		t.Fatalf("legacy internal fare field leaked into ride API: %v", completed)
	}
	events := completed["events"].([]any)
	if len(events) < 7 {
		t.Fatalf("ride transition events were not recorded: %v", events)
	}
	request("POST", "/rides/"+id+"/complete", driverToken, "complete-ride-002", nil, 409)
	request("PUT", "/driver/availability", driverToken, "availability-key-1", map[string]bool{"online": true}, 200)
	request("PUT", "/driver/availability", driverToken, "availability-key-1", map[string]bool{"online": false}, 409)
	request("POST", "/driver/location", driverToken, "location-key-2", map[string]float64{"lat": -6.8, "lng": 39.2, "accuracy": 10}, 200)
	cancelled := request("POST", "/rides", passengerToken, "create-ride-003", payload, 201)
	cancelID := cancelled["id"].(string)
	request("POST", "/rides/"+cancelID+"/cancel", passengerToken, "cancel-ride-001", map[string]string{"reason": ""}, 400)
	cancelled = request("POST", "/rides/"+cancelID+"/cancel", passengerToken, "cancel-ride-001", map[string]string{"reason": "Plans changed"}, 200)
	if cancelled["status"] != "cancelled_by_passenger" || cancelled["cancel_reason"] != "Plans changed" || cancelled["cancelled_at"] == nil {
		t.Fatalf("passenger cancellation was not persisted: %v", cancelled)
	}
	list := request("GET", "/rides?status=cancelled_by_passenger", passengerToken, "", nil, 200)
	if list["total"].(float64) != 1 || len(list["rides"].([]any)) != 1 {
		t.Fatalf("participant ride filter returned an unexpected list: %v", list)
	}
	otherList := request("GET", "/rides", otherToken, "", nil, 200)
	if otherList["total"].(float64) != 0 {
		t.Fatalf("ride list exposed another passenger's data: %v", otherList)
	}
}

func prepareRideDriver(t *testing.T, s *store.Store, id string) {
	t.Helper()
	if _, err := s.UpdateDriverIdentifiers(id, models.DriverProfileInput{LicenceNumber: "LIC" + id, PlateNumber: "PL" + id, VehicleCardNumber: "VC" + id}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expires := now.Add(365 * 24 * time.Hour)
	for _, kind := range models.RequiredDocumentTypes {
		var expiry *time.Time
		if models.RequiresExpiry(kind) {
			expiry = &expires
		}
		if err := s.DB().Create(&models.DriverDocument{ID: uuid.NewString(), DriverID: id, Type: kind, FilePath: "private/test", Status: "approved", IsCurrent: true, ExpiresAt: expiry, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
}
