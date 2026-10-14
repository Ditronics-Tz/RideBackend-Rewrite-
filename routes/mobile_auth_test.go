package routes_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/api/idtoken"
	"ride-backend/controllers"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/store"
	"ride-backend/testutil"
	"ride-backend/utils"
)

func TestMobileGoogleExchangeProfileReplayAndAccountConflict(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg.GoogleWebClientID = "web-client-id.apps.googleusercontent.com"
	ac := controllers.NewAuthController(cfg, s)
	nonce := ""
	email := "google-mobile@example.test"
	subject := "google-mobile-sub"
	ac.GoogleID = utils.GoogleIDTokenVerifier{Validate: func(_ context.Context, token, audience string) (*idtoken.Payload, error) {
		if token != "signed-fixture" || audience != cfg.GoogleWebClientID {
			return nil, errors.New("invalid signature")
		}
		return &idtoken.Payload{Issuer: "https://accounts.google.com", Audience: audience, Expires: time.Now().Add(time.Hour).Unix(), Subject: subject, Claims: map[string]interface{}{"nonce": nonce, "email": email, "email_verified": true, "name": "Mobile Rider"}}, nil
	}}
	app := fiber.New()
	app.Use(middleware.RequestContext(nil))
	routes.SetupAuthRoutes(app.Group("/api/v1"), ac, cfg)
	status, challenge, _ := authRequest(t, app, "POST", "/auth/google/challenge", "", map[string]any{})
	expectAuthStatus(t, status, 200, challenge)
	nonce = challenge["nonce"].(string)
	id := challenge["challengeId"].(string)
	status, body, _ := authRequest(t, app, "POST", "/auth/google", "", map[string]string{"idToken": "signed-fixture", "challengeId": id})
	expectAuthStatus(t, status, 200, body)
	access := body["accessToken"].(string)
	if body["access_token"] != access || body["sessionId"] == "" || body["user"].(map[string]any)["phone"] != nil {
		t.Fatal("mobile session contract invalid")
	}
	if body["onboardingRequired"].([]any)[0] != "phone_verification" {
		t.Fatal("missing phone onboarding requirement")
	}
	status, profile, _ := authRequest(t, app, "GET", "/me", access, nil)
	expectAuthStatus(t, status, 200, profile)
	status, replay, _ := authRequest(t, app, "POST", "/auth/google", "", map[string]string{"idToken": "signed-fixture", "challengeId": id})
	if status != 401 || replay["code"] != "GOOGLE_CHALLENGE_INVALID" {
		t.Fatal("challenge replay accepted")
	}
	status, challenge, _ = authRequest(t, app, "POST", "/auth/google/challenge", "", map[string]any{})
	expectAuthStatus(t, status, 200, challenge)
	nonce = challenge["nonce"].(string)
	status, body, _ = authRequest(t, app, "POST", "/auth/google", "", map[string]string{"idToken": "signed-fixture", "challengeId": challenge["challengeId"].(string)})
	expectAuthStatus(t, status, 200, body)
	if body["user"].(map[string]any)["id"] != profile["user"].(map[string]any)["id"] {
		t.Fatal("Google subject did not resolve stable account")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("valid-password-for-test!"), 12)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("Existing", "existing-mobile@example.test", string(hash), models.RolePassenger); err != nil {
		t.Fatal(err)
	}
	status, challenge, _ = authRequest(t, app, "POST", "/auth/google/challenge", "", map[string]any{})
	expectAuthStatus(t, status, 200, challenge)
	nonce = challenge["nonce"].(string)
	email, subject = "existing-mobile@example.test", "different-google-sub"
	status, body, _ = authRequest(t, app, "POST", "/auth/google", "", map[string]string{"idToken": "signed-fixture", "challengeId": challenge["challengeId"].(string)})
	if status != 409 || body["code"] != "ACCOUNT_LINK_REQUIRED" {
		t.Fatal("existing password account auto-linked")
	}
}

func TestMobilePhonePasswordLoginAndRefreshFailures(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("valid-password-for-test!"), 12)
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser("Phone Rider", "phone-mobile@example.test", string(hash), models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	phone := "+255712345678"
	if err := s.DB().Model(u).Update("phone", phone).Error; err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	status, body, _ := authRequest(t, app, "POST", "/auth/login", "", map[string]string{"identifier": "0712 345 678", "password": "valid-password-for-test!"})
	if status != 401 {
		t.Fatal("unverified phone was accepted for login")
	}
	if err := s.DB().Model(u).Update("phone_verified_at", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "POST", "/auth/login", "", map[string]string{"identifier": "0712 345 678", "password": "valid-password-for-test!"})
	expectAuthStatus(t, status, 200, body)
	access := body["accessToken"].(string)
	if body["user"].(map[string]any)["phone"] != phone || len(body["onboardingRequired"].([]any)) != 0 {
		t.Fatal("verified phone login contract invalid")
	}
	status, _, _ = authRequest(t, app, "GET", "/me", access, nil)
	expectAuthStatus(t, status, 200, body)
	status, body, _ = authRequest(t, app, "POST", "/auth/refresh", "", map[string]string{"refreshToken": "invalid"})
	if status != 401 || body["code"] != "INVALID_REFRESH_TOKEN" {
		t.Fatal("invalid refresh not classified")
	}
	status, body, _ = authRequest(t, app, "POST", "/auth/logout", access, nil)
	expectAuthStatus(t, status, 200, body)
	req := httptest.NewRequest("GET", "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("revoked access still worked")
	}
}

func TestMobileLogoutAllRevokesEveryDevice(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("valid-password-for-test!"), 12)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("Two Devices", "devices-mobile@example.test", string(hash), models.RolePassenger); err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	login := func() (string, string) {
		status, body, _ := authRequest(t, app, "POST", "/auth/login", "", map[string]string{"email": "devices-mobile@example.test", "password": "valid-password-for-test!"})
		expectAuthStatus(t, status, 200, body)
		return body["accessToken"].(string), body["refreshToken"].(string)
	}
	a1, r1 := login()
	a2, r2 := login()
	status, body, _ := authRequest(t, app, "POST", "/auth/logout-all", a1, nil)
	expectAuthStatus(t, status, 200, body)
	for _, access := range []string{a1, a2} {
		status, _, _ := authRequest(t, app, "GET", "/me", access, nil)
		if status != 401 {
			t.Fatal("logout-all left access token active")
		}
	}
	for _, refresh := range []string{r1, r2} {
		status, body, _ := authRequest(t, app, "POST", "/auth/refresh", "", map[string]string{"refreshToken": refresh})
		if status != 401 || body["code"] != "INVALID_REFRESH_TOKEN" {
			t.Fatal("logout-all left refresh token active")
		}
	}
}

func TestMobileRefreshDatabaseOutageKeepsCredentialClassification(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser("Outage", "outage-mobile@example.test", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := s.IssueAppSession(context.Background(), u.ID, store.SessionDevice{})
	if err != nil {
		t.Fatal(err)
	}
	access, err := utils.GenerateAppSessionToken(u.ID, models.RolePassenger, issued.Session.ID, cfg.JWTSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	sqlDB, err := s.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	status, body, _ := authRequest(t, app, "POST", "/auth/refresh", "", map[string]string{"refreshToken": issued.RefreshToken})
	if status != 503 || body["code"] != "AUTH_TEMPORARILY_UNAVAILABLE" {
		t.Fatal("temporary refresh failure was classified as invalid credential")
	}
	status, body, _ = authRequest(t, app, "GET", "/me", access, nil)
	if status != 503 || body["code"] != "AUTH_TEMPORARILY_UNAVAILABLE" {
		t.Fatal("temporary protected-request failure was classified as an account restriction")
	}
}
