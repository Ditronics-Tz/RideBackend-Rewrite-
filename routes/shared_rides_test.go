package routes_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
	"testing"
	"time"
)

func TestSharedRideVisibilityAndRoleGuards(t *testing.T) {
	s, cfg, e := testutil.SetupTestStore()
	if e != nil {
		t.Fatal(e)
	}
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	appToken := func(role models.Role, email string) (string, string) {
		u, e := s.CreateUser("Rider", email, "unused", role)
		if e != nil {
			t.Fatal(e)
		}
		token, e := testutil.AppToken(s, u.ID, role, cfg.JWTSecret, time.Hour)
		if e != nil {
			t.Fatal(e)
		}
		return u.ID, token
	}
	staffToken := func(role models.StaffRole, email string) string {
		u, e := s.CreateStaffUser("Staff", email, "unused", role, nil, false)
		if e != nil {
			t.Fatal(e)
		}
		tok, e := testutil.StaffToken(s, u.ID, role, cfg.StaffJWTSecret, time.Hour)
		if e != nil {
			t.Fatal(e)
		}
		return tok
	}
	d, driver := appToken(models.RoleDriver, "driver@shared.test")
	prepareRideDriver(t, s, d)
	if e := s.DB().Model(&models.DriverProfile{}).Where("user_id=?", d).Updates(map[string]any{"vehicle_type": "bajaji", "verification_status": models.Approved}).Error; e != nil {
		t.Fatal(e)
	}
	_, passenger := appToken(models.RolePassenger, "passenger@shared.test")
	_, stranger := appToken(models.RolePassenger, "other@shared.test")
	admin := staffToken(models.StaffRoleAdmin, "admin@shared.test")
	support := staffToken(models.StaffRoleSupport, "staff@shared.test")
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
	fees := map[string]any{"shared_fee": 700, "hail_percent": 15, "enabled": true, "version": 0}
	request("PUT", "/admin/transport/settings", support, "", fees, 403)
	request("PUT", "/admin/transport/settings", admin, "", fees, 200)
	request("PUT", "/admin/transport/settings", admin, "", fees, 409)
	data := models.SharedPublish{Pickup: models.RidePoint{Lat: -6.94, Lng: 39.19, Address: "Mbande"}, Dropoff: models.RidePoint{Lat: -6.82, Lng: 39.28, Address: "Gerezani"}, Capacity: 3, Seats: 2, Price: 1500, WaitMinutes: 15}
	request("POST", "/shared-trips", passenger, "publish-bad", data, 403)
	request("POST", "/shared-trips", driver, "", data, 400)
	published := request("POST", "/shared-trips", driver, "publish-good", data, 200)
	path := "/shared-trips/" + published["id"].(string)
	request("POST", "/admin"+path+"/cancel", support, "", map[string]any{"reason": "test"}, 403)
	booking := request("POST", path+"/reserve", passenger, "passenger-book", map[string]any{"passenger_id": d}, 200)
	if booking["pin"] == nil || booking["held"] != float64(1) || booking["confirmed"] != float64(0) {
		t.Fatal("incorrect booking")
	}
	for _, token := range []string{driver, stranger} {
		v := request("GET", path, token, "", nil, 200)
		if v["pin"] != nil || v["my_seat"] != nil {
			t.Fatal("private passenger code disclosed")
		}
	}
	seat := booking["my_seat"].(map[string]any)
	verify := map[string]any{"seat_id": seat["id"], "pin": booking["pin"]}
	request("POST", path+"/verify", stranger, "wrong-actor", verify, 404)
	v := request("POST", path+"/verify", driver, "driver-pickup", verify, 200)
	if v["confirmed"] != float64(1) || v["held"] != float64(0) {
		t.Fatal("pickup did not verify seat")
	}
	request("POST", path+"/start", driver, "driver-start", nil, 200)
	request("POST", path+"/complete", driver, "driver-finish", nil, 200)
	request("GET", path, stranger, "", nil, 404)
	request("GET", "/admin/shared-trips", support, "", nil, 200)
}
