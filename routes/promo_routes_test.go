package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"net/http/httptest"
	"ride-backend/models"
	"ride-backend/notifier"
	"ride-backend/routes"
	"ride-backend/testutil"
	"ride-backend/utils"
	"strings"
	"testing"
	"time"
)

type promoNotifications struct{ users []string }

func (n *promoNotifications) NotifyPromo(_ context.Context, userID string, _ notifier.PromoMessage) error {
	n.users = append(n.users, userID)
	return nil
}

func TestPromoPermissionsAuditAndPassengerFlow(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg.StaffRequire2FAForAdmin = false
	notifications := &promoNotifications{}
	s.Notifier = notifications
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	admin, err := s.CreateStaffUser("Promo admin", uuid.NewString()+"@example.test", "unused", models.StaffRoleAdmin, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	support, err := s.CreateStaffUser("Promo support", uuid.NewString()+"@example.test", "unused", models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	passenger, err := s.CreateUser("Promo passenger", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser("Other passenger", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := s.CreateUser("Promo driver", uuid.NewString()+"@example.test", "unused", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	at, _ := utils.GenerateStaffToken(admin.ID, admin.Role, cfg.StaffJWTSecret, cfg.StaffAccessTTL)
	st, _ := utils.GenerateStaffToken(support.ID, support.Role, cfg.StaffJWTSecret, cfg.StaffAccessTTL)
	pt, _ := utils.GenerateAppToken(passenger.ID, passenger.Role, cfg.JWTSecret, time.Hour)
	ot, _ := utils.GenerateAppToken(other.ID, other.Role, cfg.JWTSecret, time.Hour)
	dt, _ := utils.GenerateAppToken(driver.ID, driver.Role, cfg.JWTSecret, time.Hour)
	request := func(method, path, token string, payload interface{}, want int) []byte {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req, 10000)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s %s: want %d got %d: %s", method, path, want, resp.StatusCode, out)
		}
		return out
	}
	code := "TEST-" + strings.ToUpper(uuid.NewString()[:8])
	payload := map[string]interface{}{"code": strings.ToLower(code), "title": "Passenger reward", "discount_type": "percent", "discount_value": 20, "max_discount": 1000, "min_fare": 1000, "audience": "selected", "status": "active", "starts_at": time.Now().Add(-time.Hour), "expires_at": time.Now().Add(24 * time.Hour), "audience_user_ids": []string{passenger.ID}}
	out := request("POST", "/admin/promos", at, payload, 201)
	var p models.PromoCode
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatal(err)
	}
	if p.Code != code {
		t.Fatal("code not normalized")
	}
	request("POST", "/admin/promos", at, payload, 409)
	payload["code"] = "BAD-" + strings.ToUpper(uuid.NewString()[:8])
	payload["audience_user_ids"] = []string{driver.ID}
	request("POST", "/admin/promos", at, payload, 400)
	var bad int64
	s.DB().Model(&models.PromoCode{}).Where("code = ?", payload["code"]).Count(&bad)
	if bad != 0 {
		t.Fatal("failed create was not rolled back")
	}
	base := "/admin/promos/" + p.ID
	for _, path := range []string{"/admin/promos", base, base + "/audience", base + "/redemptions", "/admin/users/" + passenger.ID + "/promos", "/admin/referral-settings", "/admin/referrals"} {
		request("GET", path, st, nil, 200)
	}
	for _, action := range []struct{ method, path string }{{"POST", "/admin/promos"}, {"PATCH", base}, {"POST", base + "/pause"}, {"POST", base + "/resume"}, {"POST", base + "/archive"}, {"POST", base + "/audience"}, {"DELETE", base + "/audience/" + passenger.ID}, {"POST", base + "/share"}, {"PUT", "/admin/referral-settings"}} {
		request(action.method, action.path, st, map[string]interface{}{}, 403)
	}
	request("GET", "/admin/promos", pt, nil, 401)
	request("GET", "/promos/available?fare=5000", dt, nil, 403)
	request("GET", "/referral/me", dt, nil, 403)
	request("POST", "/referral/apply", dt, map[string]string{"code": "ABCDEFGH"}, 403)
	out = request("GET", "/promos/available?fare=5000", pt, nil, 200)
	if !bytes.Contains(out, []byte(code)) {
		_, _, evaluationErr := s.EvaluatePromo(passenger.ID, code, 5000, time.Now())
		t.Fatalf("eligible promo missing: %s; evaluation: %v; created: %s", out, evaluationErr, p.Status)
	}
	out = request("GET", "/promos/available?fare=5000", ot, nil, 200)
	if bytes.Contains(out, []byte(code)) {
		t.Fatal("private promo exposed")
	}
	request("POST", "/promos/validate", ot, map[string]interface{}{"code": code, "fare": 5000}, 400)
	out = request("POST", base+"/audience", at, map[string]interface{}{"user_ids": []string{passenger.ID, other.ID, driver.ID, uuid.NewString()}, "emails": []string{other.Email, "missing@example.test"}}, 200)
	var counts struct {
		Added         int      `json:"added"`
		Already       int      `json:"already_present"`
		NotFound      []string `json:"not_found"`
		NotPassengers []string `json:"not_passengers"`
	}
	if err := json.Unmarshal(out, &counts); err != nil {
		t.Fatal(err)
	}
	if counts.Added != 1 || counts.Already != 1 || len(counts.NotFound) != 2 || len(counts.NotPassengers) != 1 {
		t.Fatalf("unexpected audience counts: %s", out)
	}
	request("DELETE", base+"/audience/"+other.ID, at, nil, 200)
	out = request("POST", base+"/share", at, nil, 200)
	if len(notifications.users) != 1 || notifications.users[0] != passenger.ID || !bytes.Contains(out, []byte(`"notified_count":1`)) {
		t.Fatalf("wrong share: %s", out)
	}
	var grant models.PromoAudience
	if err := s.DB().First(&grant, "promo_id = ? AND user_id = ?", p.ID, passenger.ID).Error; err != nil || grant.NotifiedAt == nil {
		t.Fatal("notification timestamp missing", err)
	}
	request("PATCH", base, at, map[string]interface{}{"title": "Updated reward"}, 200)
	for _, action := range []string{"pause", "resume"} {
		request("POST", base+"/"+action, at, nil, 200)
	}
	for i := 0; i < 10; i++ {
		out = request("POST", "/promos/validate", pt, map[string]interface{}{"code": code, "fare": 10000}, 200)
		if !bytes.Contains(out, []byte(`"discount_amount":1000`)) || !bytes.Contains(out, []byte(`"final_fare":9000`)) {
			t.Fatalf("wrong discount: %s", out)
		}
	}
	request("POST", "/promos/validate", pt, map[string]interface{}{"code": code, "fare": 10000}, 429)
	out = request("POST", "/promos/validate", ot, map[string]interface{}{"code": "UNKNOWN", "fare": 1000}, 400)
	if !bytes.Contains(out, []byte(`"fields"`)) {
		t.Fatal("validation fields missing")
	}
	if _, err := s.ReservePromo(passenger.ID, code, 10000, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	request("PATCH", base, at, map[string]interface{}{"discount_value": 30}, 400)
	request("PATCH", base, at, map[string]interface{}{"title": "Still editable"}, 200)
	request("POST", base+"/archive", at, nil, 200)
	request("POST", base+"/share", at, nil, 400)
	settings := models.ReferralSettings{Enabled: true, RefereeRewardType: "fixed", RefereeRewardValue: 1000, ReferrerRewardType: "fixed", ReferrerRewardValue: 2000, QualifyingMinFare: 3000, MaxRewardsPerReferrer: 10, RewardValidDays: 30}
	request("PUT", "/admin/referral-settings", at, settings, 200)
	request("GET", "/referral/me", pt, nil, 200)
	request("GET", "/admin/users?role=passenger&q="+passenger.Email+"&limit=1&offset=0", st, nil, 200)
	out = request("GET", "/admin/users", st, nil, 200)
	if !bytes.Contains(out, []byte(`"users"`)) || !bytes.Contains(out, []byte(`"count"`)) {
		t.Fatal("legacy users response broken")
	}
	for _, action := range []string{"create_promo", "update_promo", "pause_promo", "resume_promo", "archive_promo", "add_promo_audience", "remove_promo_audience", "share_promo", "update_referral_settings"} {
		var count int64
		if err := s.DB().Model(&models.StaffAuditLog{}).Where("actor_id = ? AND action = ?", admin.ID, action).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatalf("missing audit %s", action)
		}
		var logs []models.StaffAuditLog
		if err := s.DB().Where("actor_id = ? AND action = ?", admin.ID, action).Find(&logs).Error; err != nil {
			t.Fatal(err)
		}
		for _, log := range logs {
			if log.Meta != "{}" {
				t.Fatalf("named promo audit must omit personal metadata: %s", log.Meta)
			}
		}
	}
	// An all-passenger share reaches every passenger, excludes drivers, and
	// does not make grants a prerequisite for passengers who sign up later.
	payload["code"] = "ALL-" + strings.ToUpper(uuid.NewString()[:8])
	payload["audience"] = "all"
	delete(payload, "audience_user_ids")
	out = request("POST", "/admin/promos", at, payload, 201)
	var all models.PromoCode
	if err := json.Unmarshal(out, &all); err != nil {
		t.Fatal(err)
	}
	notifications.users = nil
	var passengerCount int64
	if err := s.DB().Model(&models.User{}).Where("role = ?", models.RolePassenger).Count(&passengerCount).Error; err != nil {
		t.Fatal(err)
	}
	request("POST", "/admin/promos/"+all.ID+"/share", at, nil, 200)
	if int64(len(notifications.users)) != passengerCount {
		t.Fatal("all-passenger share missed recipients")
	}
	for _, id := range notifications.users {
		if id == driver.ID {
			t.Fatal("driver notified of passenger promo")
		}
	}
	late, err := s.CreateUser("Late passenger", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EvaluatePromo(late.ID, all.Code, 5000, time.Now()); err != nil {
		t.Fatal("future passenger excluded", err)
	}
}
