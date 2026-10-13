package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net/http/httptest"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
	"testing"
	"time"
)

type captureSMS struct{ Phone, Code string }

func (s *captureSMS) SendOTP(_ context.Context, phone, code string) error {
	s.Phone = phone
	s.Code = code
	return nil
}

func TestAccountControlAndPhoneVerification(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg.StaffRequire2FAForAdmin = false
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	hash, _ := bcrypt.GenerateFromPassword([]byte("Test-pass-123"), bcrypt.MinCost)
	u, _ := s.CreateUser("Passenger", uuid.NewString()+"@example.test", string(hash), models.RolePassenger)
	other, _ := s.CreateUser("Other", uuid.NewString()+"@example.test", string(hash), models.RolePassenger)
	support, _ := s.CreateStaffUser("Support", uuid.NewString()+"@example.test", "unused", models.StaffRoleSupport, nil, false)
	admin, _ := s.CreateStaffUser("Admin", uuid.NewString()+"@example.test", "unused", models.StaffRoleAdmin, nil, false)
	token, _ := testutil.AppToken(s, u.ID, u.Role, cfg.JWTSecret, time.Hour)
	otherToken, _ := testutil.AppToken(s, other.ID, other.Role, cfg.JWTSecret, time.Hour)
	st, _ := testutil.StaffToken(s, support.ID, support.Role, cfg.StaffJWTSecret, cfg.StaffAccessTTL)
	at, _ := testutil.StaffToken(s, admin.ID, admin.Role, cfg.StaffJWTSecret, cfg.StaffAccessTTL)
	request := func(method, path, auth string, payload interface{}, want int) []byte {
		t.Helper()
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: want %d got %d: %s", method, path, want, resp.StatusCode, out)
		}
		return out
	}
	base := "/admin/users/" + u.ID
	request("POST", base+"/suspend", st, map[string]string{}, 400)
	request("POST", base+"/suspend", st, map[string]string{"reason": "Safety investigation"}, 200)
	request("GET", "/auth/me", token, nil, 403)
	request("POST", "/auth/login", "", map[string]string{"email": u.Email, "password": "Test-pass-123"}, 401)
	request("POST", base+"/reactivate", st, map[string]string{"reason": "Investigation complete"}, 200)
	request("GET", "/auth/me", token, nil, 401)
	token, err = testutil.AppToken(s, u.ID, u.Role, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request("GET", "/auth/me", token, nil, 200)
	request("POST", base+"/notes", st, map[string]string{"body": "Staff-only safety context"}, 201)
	request("GET", base+"/notes", token, nil, 401)
	notes := request("GET", base+"/notes", st, nil, 200)
	var list struct {
		Notes []models.SupportNote `json:"notes"`
	}
	_ = json.Unmarshal(notes, &list)
	request("DELETE", base+"/notes/"+list.Notes[0].ID, st, nil, 403)
	request("DELETE", base+"/notes/"+list.Notes[0].ID, at, nil, 204)
	history := request("GET", base+"/history", st, nil, 200)
	if !bytes.Contains(history, []byte("Safety investigation")) {
		t.Fatal("Suspension history missing")
	}
	request("POST", base+"/ban", st, map[string]string{"reason": "Abuse"}, 403)
	request("POST", base+"/ban", at, map[string]string{"reason": "Repeated abuse"}, 200)
	request("POST", base+"/reactivate", st, map[string]string{"reason": "Cannot bypass admin ban"}, 409)
	request("POST", base+"/reactivate", at, map[string]string{"reason": "Appeal approved"}, 200)
	until := time.Now().Add(time.Minute)
	request("POST", base+"/suspend", st, map[string]interface{}{"reason": "Temporary restriction", "until": until}, 200)
	s.DB().Model(&models.User{}).Where("id = ?", u.ID).Update("suspended_until", time.Now().Add(-time.Minute))
	request("GET", "/auth/me", token, nil, 401)
	token, err = testutil.AppToken(s, u.ID, u.Role, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request("GET", "/auth/me", token, nil, 200)
	request("POST", "/auth/phone/request-otp", token, map[string]string{"phone": "0712345678"}, 503)
	sms := &captureSMS{}
	s.SMS = sms
	request("POST", "/auth/phone/request-otp", token, map[string]string{"phone": "0712345678"}, 200)
	if sms.Phone != "+255712345678" {
		t.Fatal("Phone not normalized")
	}
	request("POST", "/auth/phone/request-otp", token, map[string]string{"phone": sms.Phone}, 409)
	request("POST", "/auth/phone/verify-otp", token, map[string]string{"code": "wrong!"}, 400)
	request("POST", "/auth/phone/verify-otp", token, map[string]string{"code": sms.Code}, 200)
	request("POST", "/auth/phone/verify-otp", token, map[string]string{"code": sms.Code}, 400)
	verified, _ := s.GetUserByID(u.ID)
	if verified.PhoneVerifiedAt == nil {
		t.Fatal("Phone ownership not recorded")
	}
	request("POST", "/auth/phone/request-otp", otherToken, map[string]string{"phone": "+255712345678"}, 409)
	request("PUT", "/passenger/profile/me", token, map[string]string{"phone": "0719999999"}, 400)
	var count int64
	if err := s.DB().Model(&models.StaffAuditLog{}).Where("actor_id = ?", support.ID).Count(&count).Error; err != nil || count < 10 {
		t.Fatalf("Support actions not audited: count=%d error=%v", count, err)
	}
}
