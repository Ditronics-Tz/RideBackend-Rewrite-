package routes_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
	"ride-backend/utils"
)

func TestStaffMFAChallengeEnrollmentRecoveryAndReplay(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Unix(1_800_000_000, 0).UTC()
	s.Clock = func() time.Time { return clock }
	password := "support-mfa-password!"
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), 12)
	support, err := s.CreateStaffUser("Support", "mfa-support@example.test", string(hash), models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	adminHash, _ := bcrypt.GenerateFromPassword([]byte(password), 12)
	admin, err := s.CreateStaffUser("Admin", "mfa-admin@example.test", string(adminHash), models.StaffRoleAdmin, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	passengerHash, _ := bcrypt.GenerateFromPassword([]byte("passenger-password-ok"), 12)
	passenger, err := s.CreateUser("Passenger", "mfa-passenger@example.test", string(passengerHash), models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)

	status, body, _ := authRequest(t, app, "POST", "/auth/login", "", map[string]string{"email": passenger.Email, "password": "passenger-password-ok"})
	expectAuthStatus(t, status, 200, body)
	if body["access_token"] == nil || body["mfa_required"] == true {
		t.Fatalf("passenger login changed: %v", body)
	}

	status, body, _ = authRequest(t, app, "POST", "/staff/auth/login", "", map[string]string{"email": support.Email, "password": "wrong-password"})
	expectAuthStatus(t, status, 401, body)
	if body["mfa_required"] == true {
		t.Fatal("wrong password revealed an MFA challenge")
	}

	status, body, _ = authRequest(t, app, "POST", "/staff/auth/login", "", map[string]string{"email": "missing@example.test", "password": password})
	expectAuthStatus(t, status, 401, body)

	status, body, _ = authRequest(t, app, "POST", "/staff/auth/login", "", map[string]string{"email": admin.Email, "password": password})
	expectAuthStatus(t, status, 200, body)
	adminChallenge := body["challenge_token"].(string)
	unenrolledSession, err := s.CreateStaffSession(admin.ID, utils.HashSHA256("unenrolled-refresh"), admin.ID, "test", "127.0.0.1", clock.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	unenrolledToken, err := utils.GenerateStaffSessionToken(admin.ID, admin.Role, unenrolledSession.ID, admin.AuthVersion, cfg.StaffJWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "GET", "/staff/auth/me", unenrolledToken, nil)
	expectAuthStatus(t, status, 403, body)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/refresh", "", map[string]string{"refresh_token": "unenrolled-refresh"})
	expectAuthStatus(t, status, 401, body)
	bare, _ := testutil.StaffToken(s, admin.ID, admin.Role, cfg.StaffJWTSecret, time.Hour)
	status, body, _ = authRequest(t, app, "GET", "/staff/auth/me", adminChallenge, nil)
	expectAuthStatus(t, status, 401, body)
	status, body, _ = authRequest(t, app, "GET", "/staff/auth/me", bare, nil)
	expectAuthStatus(t, status, 200, body)

	status, body, _ = authRequest(t, app, "POST", "/staff/auth/login", "", map[string]string{"email": support.Email, "password": password})
	expectAuthStatus(t, status, 200, body)
	challenge := body["challenge_token"].(string)
	if body["challenge_expires_in"] != float64(300) {
		t.Fatalf("challenge ttl: %v", body["challenge_expires_in"])
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/mfa/enroll", "", map[string]string{"challenge_token": challenge})
	expectAuthStatus(t, status, 200, body)
	secret := body["secret"].(string)
	uri := body["qr_code_url"].(string)
	if !strings.Contains(uri, "otpauth://totp/") || !strings.Contains(uri, "algorithm=SHA1") || !strings.Contains(uri, "digits=6") || !strings.Contains(uri, "period=30") {
		t.Fatal(uri)
	}
	clock = clock.Add(11 * time.Minute)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/mfa/enroll/confirm", "", map[string]string{"challenge_token": challenge, "code": "123456"})
	expectAuthStatus(t, status, 401, body)
	if body["code"] != "mfa_challenge_expired" {
		t.Fatalf("expired enrollment: %v", body)
	}

	clock = time.Unix(1_800_000_000, 0).UTC()
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/login", "", map[string]string{"email": support.Email, "password": password})
	expectAuthStatus(t, status, 200, body)
	challenge = body["challenge_token"].(string)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/mfa/enroll", "", map[string]string{"challenge_token": challenge})
	expectAuthStatus(t, status, 200, body)
	secret = body["secret"].(string)
	code, err := utils.GenerateTOTPCode(secret, clock)
	if err != nil {
		t.Fatal(err)
	}
	var first map[string]any
	var second map[string]any
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, first = postAuth(app, "/staff/auth/mfa/enroll/confirm", map[string]string{"challenge_token": challenge, "code": code})
	}()
	go func() {
		defer wg.Done()
		_, second = postAuth(app, "/staff/auth/mfa/enroll/confirm", map[string]string{"challenge_token": challenge, "code": code})
	}()
	wg.Wait()
	wins := 0
	var session map[string]any
	for _, result := range []map[string]any{first, second} {
		if result["access_token"] != nil {
			wins++
			session = result
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent enrollment wins=%d first=%v second=%v", wins, first["error"], second["error"])
	}
	recovery := session["recovery_codes"].([]any)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/mfa/enroll/confirm", "", map[string]string{"challenge_token": challenge, "code": code})
	expectAuthStatus(t, status, 401, body)

	clock = clock.Add(30 * time.Second)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/login", "", map[string]string{"email": support.Email, "password": password})
	expectAuthStatus(t, status, 200, body)
	if body["enrollment_required"] != false {
		t.Fatal(body)
	}
	verifyChallenge := body["challenge_token"].(string)
	same, err := utils.GenerateTOTPCode(secret, clock.Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/mfa/verify", "", map[string]string{"challenge_token": verifyChallenge, "code": same})
	expectAuthStatus(t, status, 401, body)
	next, err := utils.GenerateTOTPCode(secret, clock)
	if err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/mfa/verify", "", map[string]string{"challenge_token": verifyChallenge, "code": next})
	expectAuthStatus(t, status, 200, body)
	access := body["access_token"].(string)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/mfa/verify", "", map[string]string{"challenge_token": verifyChallenge, "code": next})
	expectAuthStatus(t, status, 401, body)

	status, body, _ = authRequest(t, app, "GET", "/staff/auth/mfa/status", access, nil)
	expectAuthStatus(t, status, 200, body)
	if body["totp_enabled"] != true || body["recovery_codes_remaining"] != float64(8) {
		t.Fatalf("status: %v", body)
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/login", "", map[string]string{"email": support.Email, "password": password})
	expectAuthStatus(t, status, 200, body)
	recoveryChallenge := body["challenge_token"].(string)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/mfa/recovery/verify", "", map[string]string{"challenge_token": recoveryChallenge, "recovery_code": recovery[0].(string)})
	expectAuthStatus(t, status, 200, body)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/login", "", map[string]string{"email": support.Email, "password": password})
	expectAuthStatus(t, status, 200, body)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/mfa/recovery/verify", "", map[string]string{"challenge_token": body["challenge_token"].(string), "recovery_code": recovery[0].(string)})
	expectAuthStatus(t, status, 401, body)

	clock = clock.Add(30 * time.Second)
	freshCode, err := utils.GenerateTOTPCode(secret, clock)
	if err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/mfa/recovery/regenerate", access, map[string]string{"code": freshCode})
	expectAuthStatus(t, status, 200, body)
	replaced := body["recovery_codes"].([]any)
	if len(replaced) != 8 || replaced[0] == recovery[0] {
		t.Fatal("recovery codes were not replaced")
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/login", "", map[string]string{"email": support.Email, "password": password})
	expectAuthStatus(t, status, 200, body)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/mfa/recovery/verify", "", map[string]string{"challenge_token": body["challenge_token"].(string), "recovery_code": recovery[1].(string)})
	expectAuthStatus(t, status, 401, body)

	corrupt := "00"
	if _, err := s.UpdateStaffUser(admin.ID, func(u *models.StaffUser) {
		u.TOTPEnabled = true
		u.TOTPSecretEnc = &corrupt
	}); err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/login", "", map[string]string{"email": admin.Email, "password": password})
	expectAuthStatus(t, status, 401, body)
	if body["challenge_token"] != nil {
		t.Fatal("corrupted secret issued a challenge")
	}
}

func postAuth(app *fiber.App, path string, payload map[string]string) (int, map[string]any) {
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/v1"+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 10000)
	if err != nil {
		return 0, map[string]any{"error": err.Error()}
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}
