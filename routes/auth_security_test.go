package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
	"ride-backend/controllers"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
	"ride-backend/utils"
)

func authRequest(t *testing.T, app *fiber.App, method, path, token string, body any) (int, map[string]any, http.Header) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req, 10000)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	if resp.StatusCode != 204 {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, out, resp.Header
}
func expectAuthStatus(t *testing.T, status, want int, body map[string]any) {
	t.Helper()
	if status != want {
		t.Fatalf("want%d got%d error=%v", want, status, body["error"])
	}
}
func requireSession(t *testing.T, body map[string]any) (string, string) {
	t.Helper()
	access, _ := body["access_token"].(string)
	refresh, _ := body["refresh_token"].(string)
	if access == "" || refresh == "" || body["token"] != access || body["expires_in"] != float64(900) {
		t.Fatal("invalid session response")
	}
	return access, refresh
}

func TestAppLoginLockoutUniformityAndStaffUnlock(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("valid-test-password!"), 12)
	u, err := s.CreateUser("Login User", "login-security@example.test", string(hash), models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	for _, email := range []string{"unknown@example.test", u.Email} {
		status, body, _ := authRequest(t, app, "POST", "/auth/login", "", map[string]string{"email": email, "password": "wrong"})
		expectAuthStatus(t, status, 401, body)
		if body["error"] != "invalid credentials" {
			t.Fatal("credential response leaked account state")
		}
	}
	for i := 0; i < 4; i++ {
		status, body, _ := authRequest(t, app, "POST", "/auth/login", "", map[string]string{"email": u.Email, "password": "wrong"})
		expectAuthStatus(t, status, 401, body)
	}
	status, body, _ := authRequest(t, app, "POST", "/auth/login", "", map[string]string{"email": u.Email, "password": "valid-test-password!"})
	expectAuthStatus(t, status, 401, body)
	if body["error"] != "invalid credentials" {
		t.Fatal("lockout differs")
	}
	admin, err := s.CreateStaffUser("Admin", "unlock-admin@example.test", "fixture", models.StaffRoleAdmin, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg.StaffRequire2FAForAdmin = false
	staffToken, err := testutil.StaffToken(s, admin.ID, admin.Role, cfg.StaffJWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "GET", "/admin/users/"+u.ID+"/sessions", staffToken, nil)
	expectAuthStatus(t, status, 200, body)
	if body["failed_login_count"] != float64(5) || body["locked_until"] == nil {
		t.Fatal("lockout metadata missing")
	}
	status, body, _ = authRequest(t, app, "POST", "/admin/users/"+u.ID+"/unlock", staffToken, map[string]any{})
	expectAuthStatus(t, status, 200, body)
	status, body, _ = authRequest(t, app, "POST", "/auth/login", "", map[string]string{"email": u.Email, "password": "valid-test-password!"})
	expectAuthStatus(t, status, 200, body)
	requireSession(t, body)
	status, body, _ = authRequest(t, app, "GET", "/admin/users/"+u.ID+"/security-events?type=login_failed", staffToken, nil)
	expectAuthStatus(t, status, 200, body)
	if body["total"] != float64(5) {
		t.Fatal("failure event count incorrect")
	}
	var count int64
	s.DB().Model(&models.StaffAuditLog{}).Where("action = ? AND target_id = ?", "unlock_app_login", u.ID).Count(&count)
	if count != 1 {
		t.Fatal("unlock missing audit")
	}
}

func TestAppSessionRotationReplayLogoutAndOwnership(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	register := func(email string) (string, string, string) {
		status, body, _ := authRequest(t, app, "POST", "/auth/register", "", map[string]string{"name": "Session User", "email": email, "password": "valid-register-password!"})
		expectAuthStatus(t, status, 201, body)
		a, r := requireSession(t, body)
		return a, r, body["user"].(map[string]any)["id"].(string)
	}
	access, refresh, userID := register("session-one@example.test")
	otherAccess, _, _ := register("session-other@example.test")
	claims, err := utils.ParseAppToken(access, cfg.JWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	sid := claims["sid"].(string)
	if claims["exp"].(float64)-claims["iat"].(float64) != 900 {
		t.Fatal("access TTL not fifteen minutes")
	}
	var row models.AppSession
	if err := s.DB().First(&row, "id = ?", sid).Error; err != nil {
		t.Fatal(err)
	}
	if row.TokenHash == refresh || row.TokenHash == "" {
		t.Fatal("refresh stored raw")
	}
	status, body, _ := authRequest(t, app, "DELETE", "/auth/sessions/"+sid, otherAccess, nil)
	if status == 204 || status == 200 {
		t.Fatal("other account revoked session")
	}
	status, body, _ = authRequest(t, app, "POST", "/auth/refresh", "", map[string]string{"refresh_token": refresh})
	expectAuthStatus(t, status, 200, body)
	newAccess, newRefresh := requireSession(t, body)
	if newRefresh == refresh {
		t.Fatal("refresh was not rotated")
	}
	status, body, _ = authRequest(t, app, "GET", "/auth/me", access, nil)
	expectAuthStatus(t, status, 401, body)
	status, body, _ = authRequest(t, app, "GET", "/auth/me", newAccess, nil)
	expectAuthStatus(t, status, 200, body)
	status, body, _ = authRequest(t, app, "POST", "/auth/refresh", "", map[string]string{"refresh_token": refresh})
	expectAuthStatus(t, status, 401, body)
	status, body, _ = authRequest(t, app, "GET", "/auth/me", newAccess, nil)
	expectAuthStatus(t, status, 401, body)
	status, body, _ = authRequest(t, app, "POST", "/auth/refresh", "", map[string]string{"refresh_token": newRefresh})
	expectAuthStatus(t, status, 401, body)
	newAccess, err = testutil.AppToken(s, userID, models.RolePassenger, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "POST", "/auth/logout", newAccess, nil)
	expectAuthStatus(t, status, 200, body)
	status, body, _ = authRequest(t, app, "GET", "/auth/me", newAccess, nil)
	expectAuthStatus(t, status, 401, body)
	legacy, err := utils.GenerateAppToken(userID, models.RolePassenger, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "GET", "/auth/me", legacy, nil)
	expectAuthStatus(t, status, 401, body)
}

func TestRegistrationPasswordValidationGenericErrorsAndThrottle(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	for _, password := range []string{"short", "password123"} {
		status, body, _ := authRequest(t, app, "POST", "/auth/register", "", map[string]string{"name": "User", "email": "password-policy@example.test", "password": password})
		expectAuthStatus(t, status, 400, body)
		if body["fields"] == nil {
			t.Fatal("password field error missing")
		}
	}
	input := map[string]string{"name": "User", "email": "unique-register@example.test", "password": "valid-password-with-symbol!"}
	status, body, _ := authRequest(t, app, "POST", "/auth/register", "", input)
	expectAuthStatus(t, status, 201, body)
	status, body, _ = authRequest(t, app, "POST", "/auth/register", "", input)
	expectAuthStatus(t, status, 409, body)
	if body["error"] != "Unable to register this account" {
		t.Fatal("duplicate error leaked details")
	}
	status, body, _ = authRequest(t, app, "POST", "/auth/register", "", map[string]string{"name": "User", "email": "last-register@example.test", "password": "short"})
	expectAuthStatus(t, status, 400, body)
	status, body, headers := authRequest(t, app, "POST", "/auth/register", "", input)
	expectAuthStatus(t, status, 429, body)
	if headers.Get("Retry-After") == "" {
		t.Fatal("rate limit missing retry header")
	}
}

type fakeGoogleAuth struct {
	calls    int
	identity utils.GoogleIdentity
}

func (f *fakeGoogleAuth) Authenticate(context.Context, string) (utils.GoogleIdentity, error) {
	f.calls++
	return f.identity, nil
}
func TestOAuthBrowserStateReplayAndVerifiedSubject(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg.GoogleClientID = "fixture-client"
	cfg.GoogleClientSecret = "fixture-only"
	cfg.GoogleRedirectURL = "http://localhost:8080/api/v1/auth/google/callback"
	cfg.GoogleRedirectAllowedURLs = cfg.GoogleRedirectURL
	provider := &fakeGoogleAuth{identity: utils.GoogleIdentity{Subject: "subject-one", Email: "google-one@example.test", VerifiedEmail: true, Name: "Google User"}}
	ac := controllers.NewAuthController(cfg, s)
	ac.Google = provider
	app := fiber.New()
	app.Use(middleware.RequestContext(nil))
	routes.SetupAuthRoutes(app.Group("/api/v1"), ac, cfg)
	start := func() (string, *http.Cookie) {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/auth/google", nil))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 307 {
			t.Fatal("OAuth start rejected")
		}
		location, _ := url.Parse(resp.Header.Get("Location"))
		state := location.Query().Get("state")
		if len(state) != 64 {
			t.Fatal("invalid state entropy")
		}
		cookies := resp.Cookies()
		if len(cookies) == 0 || !cookies[0].HttpOnly {
			t.Fatal("browser binding missing")
		}
		return state, cookies[0]
	}
	callback := func(state string, cookie *http.Cookie) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/auth/google/callback?code=fixture-code&state="+state, nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := app.Test(req, 10000)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		body := map[string]any{}
		_ = json.Unmarshal(raw, &body)
		return resp.StatusCode, body
	}
	state, cookie := start()
	status, body := callback(state, nil)
	expectAuthStatus(t, status, 400, body)
	if provider.calls != 0 {
		t.Fatal("provider reached before browser state proof")
	}
	status, body = callback(state, cookie)
	expectAuthStatus(t, status, 200, body)
	requireSession(t, body)
	userID := body["user"].(map[string]any)["id"]
	status, body = callback(state, cookie)
	expectAuthStatus(t, status, 400, body)
	if provider.calls != 1 {
		t.Fatal("state replay reached provider")
	}
	provider.identity.Email = "changed-google@example.test"
	state, cookie = start()
	status, body = callback(state, cookie)
	expectAuthStatus(t, status, 200, body)
	if body["user"].(map[string]any)["id"] != userID {
		t.Fatal("identity keyed by mutable email")
	}
	provider.identity.Subject = "subject-unverified"
	provider.identity.VerifiedEmail = false
	state, cookie = start()
	status, body = callback(state, cookie)
	expectAuthStatus(t, status, 401, body)
	if _, ok := s.GetUserByEmail(provider.identity.Email); ok {
		t.Fatal("unverified identity created account")
	}
	provider.identity.VerifiedEmail = true
	provider.identity.Email = "local-google-collision@example.test"
	if _, err := s.CreateUser("Local", provider.identity.Email, "existing-password-hash", models.RolePassenger); err != nil {
		t.Fatal(err)
	}
	state, cookie = start()
	status, body = callback(state, cookie)
	expectAuthStatus(t, status, 409, body)
	if strings.Contains(cfg.GoogleRedirectURL, "access_token=") {
		t.Fatal("token in redirect URI")
	}
}
