package routes_test

import (
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
)

func TestEmailOnlyRegistrationAndPassengerProfileCompletion(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	register := map[string]string{"email": "  PROFILE@example.test  ", "password": "unique-password-for-profile!"}
	status, body, _ := authRequest(t, app, "POST", "/auth/register", "", register)
	expectAuthStatus(t, status, 201, body)
	user := body["user"].(map[string]any)
	if user["name"] != "" || user["full_name"] != nil || user["phone"] != nil || user["phone_number"] != nil || user["profile_completed"] != false || user["email"] != "profile@example.test" {
		t.Fatalf("email-only account has unexpected profile fields: %#v", user)
	}
	access := body["accessToken"].(string)
	status, profile, _ := authRequest(t, app, "GET", "/passenger/profile/me", access, nil)
	expectAuthStatus(t, status, 200, profile)
	if profile["full_name"] != "" || profile["phone"] != "" || profile["profile_completed"] != false {
		t.Fatal("new passenger profile is not empty")
	}
	status, body, _ = authRequest(t, app, "POST", "/auth/register", "", map[string]string{"email": "profile@example.test", "password": "another-long-password!"})
	if status != 409 {
		t.Fatal("duplicate normalized email was accepted")
	}
	for _, input := range []map[string]string{
		{"email": "invalid", "password": "unique-password-for-profile!"},
		{"email": "another@example.test", "password": "short"},
	} {
		status, _, _ = authRequest(t, app, "POST", "/auth/register", "", input)
		if status != 400 {
			t.Fatalf("invalid registration returned %d", status)
		}
	}
	status, body, _ = authRequest(t, app, "PUT", "/passenger/profile/me", access, map[string]string{"full_name": "John Doe"})
	expectAuthStatus(t, status, 200, body)
	status, body, _ = authRequest(t, app, "PUT", "/passenger/profile/me", access, map[string]string{"phone_number": "0712 345 678"})
	expectAuthStatus(t, status, 200, body)
	if body["phone"] != "+255712345678" || body["full_name"] != "John Doe" || body["profile_completed"] != true {
		t.Fatalf("profile update not persisted: %#v", body)
	}
	status, me, _ := authRequest(t, app, "GET", "/me", access, nil)
	expectAuthStatus(t, status, 200, me)
	current := me["user"].(map[string]any)
	if current["name"] != "John Doe" || current["phone_number"] != "+255712345678" || current["profile_completed"] != true || current["phone_verified_at"] != nil {
		t.Fatalf("profile completion or verification state incorrect: %#v", current)
	}
	if me["onboardingRequired"].([]any)[0] != "phone_verification" {
		t.Fatal("unverified phone lost onboarding requirement")
	}
	status, body, _ = authRequest(t, app, "POST", "/auth/login", "", map[string]string{"email": "PROFILE@example.test", "password": "unique-password-for-profile!"})
	expectAuthStatus(t, status, 200, body)
	status, _, _ = authRequest(t, app, "POST", "/auth/login", "", map[string]string{"phone": "+255712345678", "password": "unique-password-for-profile!"})
	if status != 401 {
		t.Fatal("phone-only login was accepted")
	}
	oldHash, err := bcrypt.GenerateFromPassword([]byte("existing-long-password!"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser("Existing", "existing-profile@example.test", string(oldHash), models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "POST", "/auth/login", "", map[string]string{"email": other.Email, "password": "existing-long-password!"})
	expectAuthStatus(t, status, 200, body)
	otherSession, err := testutil.AppToken(s, other.ID, other.Role, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	status, _, _ = authRequest(t, app, "PUT", "/passenger/profile/me", otherSession, map[string]string{"phone_number": "+255712345678"})
	if status != 409 {
		t.Fatalf("duplicate phone returned %d", status)
	}
}
