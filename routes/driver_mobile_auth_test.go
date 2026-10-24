package routes_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"google.golang.org/api/idtoken"
	"ride-backend/controllers"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
	"ride-backend/utils"
)

func TestMobileGoogleAccountRoleAndReplaceableProfilePhoto(t *testing.T) {
	for _, role := range []string{"driver", "passenger"} {
		t.Run(role, func(t *testing.T) {
			s, cfg, err := testutil.SetupTestStore()
			if err != nil {
				t.Fatal(err)
			}
			cfg.GoogleWebClientID = "web-client"
			ac := controllers.NewAuthController(cfg, s)
			nonce := ""
			picture := "https://lh3.googleusercontent.com/verified-photo"
			ac.GoogleID = utils.GoogleIDTokenVerifier{Validate: func(_ context.Context, token, audience string) (*idtoken.Payload, error) {
				return &idtoken.Payload{Issuer: "https://accounts.google.com", Audience: audience, Expires: time.Now().Add(time.Hour).Unix(), Subject: "photo-sub", Claims: map[string]any{"nonce": nonce, "email": "photo@example.test", "email_verified": true, "name": "Asha Driver", "picture": picture}}, nil
			}}
			app := fiber.New()
			app.Use(middleware.RequestContext(nil))
			router := app.Group("/api/v1")
			routes.SetupAuthRoutes(router, ac, cfg)
			routes.SetupProfileRoutes(router, controllers.NewProfileController(cfg, s), cfg)
			defer app.Shutdown()
			exchange := func(requestedRole string) (int, map[string]any) {
				status, challenge, _ := authRequest(t, app, "POST", "/auth/google/challenge", "", map[string]any{})
				expectAuthStatus(t, status, 200, challenge)
				nonce = challenge["nonce"].(string)
				status, body, _ := authRequest(t, app, "POST", "/auth/google", "", map[string]string{"idToken": "verified-fixture", "challengeId": challenge["challengeId"].(string), "role": requestedRole, "picture": "https://attacker.test/untrusted"})
				return status, body
			}
			status, body := exchange(role)
			expectAuthStatus(t, status, 200, body)
			user := body["user"].(map[string]any)
			if user["role"] != role {
				t.Fatal("wrong account role")
			}
			access := body["accessToken"].(string)
			endpoint := "/" + role + "/profile/me"
			status, profile, _ := authRequest(t, app, "GET", endpoint, access, nil)
			expectAuthStatus(t, status, 200, profile)
			if profile["profile_picture_url"] != picture {
				t.Fatal("verified Google photo was not saved")
			}
			// Real multipart replacement must take precedence on subsequent Google sign-in.
			var data bytes.Buffer
			form := multipart.NewWriter(&data)
			part, err := form.CreateFormFile("profile_picture", "new.png")
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(part, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
				t.Fatal(err)
			}
			if err := form.Close(); err != nil {
				t.Fatal(err)
			}
			upload := endpoint + "/image"
			if role == "driver" {
				upload += "s"
			}
			req := httptest.NewRequest("POST", "/api/v1"+upload, &data)
			req.Header.Set("Content-Type", form.FormDataContentType())
			req.Header.Set("Authorization", "Bearer "+access)
			response, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatalf("photo replacement status %d", response.StatusCode)
			}
			status, profile, _ = authRequest(t, app, "GET", endpoint, access, nil)
			expectAuthStatus(t, status, 200, profile)
			uploaded := profile["profile_picture_url"]
			if uploaded == picture || uploaded == "" {
				t.Fatal("uploaded photo was not saved")
			}
			status, body = exchange(role)
			expectAuthStatus(t, status, 200, body)
			status, profile, _ = authRequest(t, app, "GET", endpoint, body["accessToken"].(string), nil)
			expectAuthStatus(t, status, 200, profile)
			if profile["profile_picture_url"] != uploaded {
				t.Fatal("Google sign-in overwrote the uploaded photo")
			}
			other := "driver"
			if role == "driver" {
				other = "passenger"
			}
			status, body = exchange(other)
			if status != 403 || body["code"] != "ACCOUNT_ROLE_MISMATCH" {
				t.Fatal("existing account switched role")
			}
			stored, ok := s.GetUserByID(user["id"].(string))
			if !ok || string(stored.Role) != role {
				t.Fatal("existing account was promoted")
			}
			status, _ = exchange("admin")
			if status != 400 {
				t.Fatal("Google sign-up accepted a staff role")
			}
		})
	}
}

func TestDriverEmailRegistrationAndPersonalProfilePersist(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	status, body, _ := authRequest(t, app, "POST", "/auth/register", "", map[string]string{"email": "driver-profile@example.test", "password": "long-unique-driver-password!", "role": "driver"})
	expectAuthStatus(t, status, 201, body)
	access := body["accessToken"].(string)
	id := body["user"].(map[string]any)["id"].(string)
	verified := time.Now()
	if err := s.DB().Model(&models.User{}).Where("id = ?", id).Updates(map[string]any{"phone": "+255712000001", "phone_verified_at": verified}).Error; err != nil {
		t.Fatal(err)
	}
	status, body, _ = authRequest(t, app, "PUT", "/driver/profile/me", access, map[string]string{"full_name": " Asha Juma ", "phone_number": "0712 345 678"})
	expectAuthStatus(t, status, 200, body)
	status, body, _ = authRequest(t, app, "GET", "/me", access, nil)
	expectAuthStatus(t, status, 200, body)
	user := body["user"].(map[string]any)
	if user["name"] != "Asha Juma" || user["phone_number"] != "+255712345678" || user["profile_completed"] != true || user["phone_verified_at"] != nil {
		t.Fatal("driver profile was not persisted or phone retained verification")
	}
	status, _, _ = authRequest(t, app, "PUT", "/driver/profile/me", access, map[string]string{"full_name": "", "phone_number": "not-a-phone"})
	if status != 422 && status != 400 {
		t.Fatalf("invalid driver details status %d", status)
	}
	stored, _ := s.GetUserByID(id)
	if stored.Name != "Asha Juma" {
		t.Fatal("invalid update modified driver name")
	}
}
