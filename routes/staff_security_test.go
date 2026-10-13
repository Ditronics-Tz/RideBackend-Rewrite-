package routes_test

import (
	"golang.org/x/crypto/bcrypt"
	"ride-backend/controllers"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/store"
	"ride-backend/testutil"
	"ride-backend/utils"
	"testing"
	"time"
)

func TestStaffPendingAuthenticatorProofActivationAndRevocation(t *testing.T) {
	controllers.ResetLoginRateLimiters()
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	password := "staff-security-password!"
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), 12)
	u, err := s.CreateStaffUser("Staff", "totp-security@example.test", string(hash), models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	login := func(code string) (string, string) {
		status, body, _ := authRequest(t, app, "POST", "/staff/auth/login", "", map[string]string{"email": u.Email, "password": password, "totp_code": code})
		expectAuthStatus(t, status, 200, body)
		return body["access_token"].(string), body["refresh_token"].(string)
	}
	current, currentRefresh := login("")
	other, otherRefresh := login("")
	status, body, _ := authRequest(t, app, "POST", "/staff/auth/2fa/setup", current, map[string]string{})
	expectAuthStatus(t, status, 400, body)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/2fa/setup", current, map[string]string{"password": password})
	expectAuthStatus(t, status, 200, body)
	secret := body["secret"].(string)
	if body["pending_expires_at"] == nil {
		t.Fatal("pending expiry missing")
	}
	fresh, ok := s.GetStaffUserByID(u.ID)
	if !ok || fresh.TOTPEnabled || fresh.TOTPSecretEnc != nil || fresh.PendingTOTPSecretEnc == nil {
		t.Fatal("setup changed active credentials")
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/2fa/verify", current, map[string]string{"code": "not-valid"})
	expectAuthStatus(t, status, 400, body)
	code, err := utils.GenerateTOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/2fa/verify", current, map[string]string{"code": code})
	expectAuthStatus(t, status, 200, body)
	newAccess := body["access_token"].(string)
	if body["totp_enabled"] != true {
		t.Fatal("activation missing enabled state")
	}
	for _, old := range []string{current, other} {
		status, body, _ = authRequest(t, app, "GET", "/staff/auth/me", old, nil)
		expectAuthStatus(t, status, 401, body)
	}
	status, body, _ = authRequest(t, app, "GET", "/staff/auth/me", newAccess, nil)
	expectAuthStatus(t, status, 200, body)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/refresh", "", map[string]string{"refresh_token": otherRefresh})
	expectAuthStatus(t, status, 401, body)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/2fa/setup", newAccess, map[string]string{"password": password})
	expectAuthStatus(t, status, 400, body)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/2fa/setup", newAccess, map[string]string{"password": password, "code": code})
	expectAuthStatus(t, status, 200, body)
	replacement := body["secret"].(string)
	fresh, _ = s.GetStaffUserByID(u.ID)
	active, err := utils.DecryptAESGCM(*fresh.TOTPSecretEnc, cfg.StaffTOTPKey)
	if err != nil || active != secret || !fresh.TOTPEnabled {
		t.Fatal("replacement setup erased active authenticator")
	}
	replacementCode, err := utils.GenerateTOTPCode(replacement, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/2fa/verify", newAccess, map[string]string{"code": replacementCode})
	expectAuthStatus(t, status, 200, body)
	newAccess = body["access_token"].(string)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/refresh", "", map[string]string{"refresh_token": currentRefresh})
	expectAuthStatus(t, status, 200, body)
	currentRefresh = body["refresh_token"].(string)
	newAccess = body["access_token"].(string)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/logout-all", newAccess, nil)
	expectAuthStatus(t, status, 200, body)
	status, body, _ = authRequest(t, app, "GET", "/staff/auth/me", newAccess, nil)
	expectAuthStatus(t, status, 401, body)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/refresh", "", map[string]string{"refresh_token": currentRefresh})
	expectAuthStatus(t, status, 401, body)
}

func TestStaffCannotRevokeOtherAccountAndDatabaseVersionHasNoCacheLag(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg.AppEnv = "production"
	app := routes.BuildApp(cfg, s)
	a, err := s.CreateStaffUser("A", "staff-a@example.test", "fixture", models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateStaffUser("B", "staff-b@example.test", "fixture", models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	ta, err := testutil.StaffToken(s, a.ID, a.Role, cfg.StaffJWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tb, err := testutil.StaffToken(s, b.ID, b.Role, cfg.StaffJWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	otherRefresh := "fixture-refresh-other"
	if _, err := s.CreateStaffSession(b.ID, utils.HashSHA256(otherRefresh), b.ID, "", "", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	status, body, _ := authRequest(t, app, "POST", "/staff/auth/logout", ta, map[string]string{"refresh_token": otherRefresh})
	expectAuthStatus(t, status, 400, body)
	status, body, _ = authRequest(t, app, "GET", "/staff/auth/me", tb, nil)
	expectAuthStatus(t, status, 200, body)
	// A second Store instance writes revocation without local cache invalidation.
	second := store.New(s.DB())
	if err := second.RevokeAllStaffSessions(b.ID); err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "GET", "/staff/auth/me", tb, nil)
	expectAuthStatus(t, status, 401, body)
	status, body, _ = authRequest(t, app, "GET", "/files/documents/nonexistent", tb, nil)
	expectAuthStatus(t, status, 401, body)
}
