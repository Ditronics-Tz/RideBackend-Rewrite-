package routes_test

import (
	"strings"

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
	clock := time.Now().UTC().Truncate(time.Second)
	s.Clock = func() time.Time { return clock }
	app := routes.BuildApp(cfg, s)
	status, body, _ := authRequest(t, app, "POST", "/staff/auth/login", "", map[string]string{"email": u.Email, "password": password})
	expectAuthStatus(t, status, 200, body)
	if body["mfa_required"] != true || body["enrollment_required"] != true || body["access_token"] != nil {
		t.Fatalf("unenrolled staff received a session: %v", body)
	}
	challenge := body["challenge_token"].(string)
	status, body, header := authRequest(t, app, "POST", "/staff/auth/mfa/enroll", "", map[string]string{"challenge_token": challenge})
	expectAuthStatus(t, status, 200, body)
	if header.Get("Cache-Control") != "no-store" || !strings.Contains(body["qr_code_url"].(string), "otpauth://totp/Rida:") {
		t.Fatalf("enrollment material was not protected: %v", body["qr_code_url"])
	}
	secret := body["secret"].(string)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/mfa/enroll/confirm", "", map[string]string{"challenge_token": challenge, "code": "000000"})
	expectAuthStatus(t, status, 401, body)
	code, err := utils.GenerateTOTPCode(secret, clock)
	if err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/mfa/enroll/confirm", "", map[string]string{"challenge_token": challenge, "code": code})
	expectAuthStatus(t, status, 200, body)
	current := body["access_token"].(string)
	currentRefresh := body["refresh_token"].(string)
	if body["totp_enabled"] != nil {
		t.Fatal("enrollment confirm should return the normal session user, not a bare flag")
	}
	codes, _ := body["recovery_codes"].([]any)
	if len(codes) != 8 {
		t.Fatalf("recovery codes: %v", body["recovery_codes"])
	}
	clock = clock.Add(30 * time.Second)
	loginCode, err := utils.GenerateTOTPCode(secret, clock)
	if err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/login", "", map[string]string{"email": u.Email, "password": password, "totp_code": loginCode})
	expectAuthStatus(t, status, 200, body)
	if body["access_token"] != nil || body["enrollment_required"] != false {
		t.Fatalf("enrolled login must stop at the challenge: %v", body)
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/mfa/verify", "", map[string]string{"challenge_token": body["challenge_token"].(string), "code": loginCode})
	expectAuthStatus(t, status, 200, body)
	other := body["access_token"].(string)
	otherRefresh := body["refresh_token"].(string)
	clock = clock.Add(30 * time.Second)
	proof, err := utils.GenerateTOTPCode(secret, clock)
	if err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/2fa/setup", current, map[string]string{})
	expectAuthStatus(t, status, 400, body)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/2fa/setup", current, map[string]string{"password": password})
	expectAuthStatus(t, status, 400, body)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/2fa/disable", current, map[string]string{"password": password, "code": proof})
	expectAuthStatus(t, status, 403, body)
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/2fa/setup", current, map[string]string{"password": password, "code": proof})
	expectAuthStatus(t, status, 200, body)
	replacement := body["secret"].(string)
	fresh, ok := s.GetStaffUserByID(u.ID)
	active, decErr := utils.DecryptAESGCM(*fresh.TOTPSecretEnc, cfg.StaffTOTPKey)
	if !ok || decErr != nil || active != secret || !fresh.TOTPEnabled || fresh.PendingTOTPSecretEnc == nil {
		t.Fatal("replacement setup erased active authenticator")
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/2fa/verify", current, map[string]string{"code": "not-valid"})
	expectAuthStatus(t, status, 400, body)
	clock = clock.Add(30 * time.Second)
	replacementCode, err := utils.GenerateTOTPCode(replacement, clock)
	if err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "POST", "/staff/auth/2fa/verify", current, map[string]string{"code": replacementCode})
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
