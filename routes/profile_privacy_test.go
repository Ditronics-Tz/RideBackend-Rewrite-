package routes_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
)

func TestPublicProfilesHidePIIAndOwnersAndStaffRetainFullView(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	driver, err := s.CreateUser("Driver Name", uuid.NewString()+"@example.test", "unused", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	passenger, err := s.CreateUser("Passenger Name", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DB().Model(&models.User{}).Where("id = ?", passenger.ID).Update("phone", "+255700123456").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.DB().Model(&models.DriverProfile{}).Where("user_id = ?", driver.ID).Updates(map[string]any{
		"licence_number": "LIC-SENSITIVE-123", "vehicle_card_number": "CARD-SENSITIVE-456", "plate_number": "T999AAA",
		"licence_image_url": "/api/v1/files/licences/secret.png", "vehicle_type": "sedan",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.DB().Model(&models.PassengerProfile{}).Where("user_id = ?", passenger.ID).Update("phone", "+255700123456").Error; err != nil {
		t.Fatal(err)
	}
	other, err := testutil.AppToken(s, passenger.ID, passenger.Role, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	driverToken, err := testutil.AppToken(s, driver.ID, driver.Role, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	staff, err := s.CreateStaffUser("Support", uuid.NewString()+"@example.test", "unused", models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	staffToken, err := testutil.StaffToken(s, staff.ID, staff.Role, cfg.StaffJWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	get := func(path, token string) map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: expected 200, got %d", path, resp.StatusCode)
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	containsAny := func(body map[string]any, values ...string) bool {
		t.Helper()
		encoded, _ := json.Marshal(body)
		for _, value := range values {
			if strings.Contains(string(encoded), value) {
				return true
			}
		}
		return false
	}
	driverPublic := get("/api/v1/driver/profile/"+driver.ID, other)
	if driverPublic["name"] != "Driver Name" || driverPublic["vehicle_type"] != "sedan" || containsAny(driverPublic, "LIC-SENSITIVE-123", "CARD-SENSITIVE-456", "T999AAA", "secret.png") {
		t.Fatal("public driver profile exposed compliance identifiers or omitted safe fields")
	}
	passengerPublic := get("/api/v1/passenger/profile/"+passenger.ID, driverToken)
	if passengerPublic["name"] != "Passenger Name" || containsAny(passengerPublic, "+255700123456", "phone") {
		t.Fatal("public passenger profile exposed phone data")
	}
	ownerDriver := get("/api/v1/driver/profile/"+driver.ID, driverToken)
	if ownerDriver["licence_number"] != "LIC-SENSITIVE-123" || ownerDriver["plate_number"] != "T999AAA" {
		t.Fatal("driver owner lost access to own private profile")
	}
	staffView := get("/api/v1/admin/users/"+driver.ID+"/profile", staffToken)
	profile := staffView["driver_profile"].(map[string]any)
	if profile["licence_number"] != "LIC-SENSITIVE-123" || profile["plate_number"] != "T999AAA" {
		t.Fatal("support staff lost full profile review access")
	}
}

func TestProfileImageDecodeReencodeAndPerUserQuota(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.CreateUser("Image Passenger", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	token, err := testutil.AppToken(s, user.ID, user.Role, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	post := func(content []byte) (int, string) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("profile_picture", "misleading.jpg")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(content)
		_ = writer.Close()
		req := httptest.NewRequest("POST", "/api/v1/passenger/profile/me/image", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var response map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&response)
		url, _ := response["profile_picture_url"].(string)
		return resp.StatusCode, url
	}
	if status, _ := post([]byte("MZ fake executable pretending to be image/png")); status != 400 {
		t.Fatalf("spoofed image status = %d, want 400", status)
	}
	for i := 0; i < 3; i++ {
		status, url := post(imageBytes.Bytes())
		if status != 200 || !strings.HasSuffix(url, ".png") {
			t.Fatalf("valid image %d: status=%d url=%q", i+1, status, url)
		}
		filename := strings.TrimPrefix(url, "/api/v1/uploads/")
		saved := filepath.Join("uploads", "public", filename)
		data, err := os.ReadFile(saved)
		if err != nil {
			t.Fatal(err)
		}
		if _, format, err := image.Decode(bytes.NewReader(data)); err != nil || format != "png" {
			t.Fatal("saved profile picture was not decoded and re-encoded as PNG")
		}
		t.Cleanup(func() { _ = os.Remove(saved) })
	}
	if status, _ := post(imageBytes.Bytes()); status != 429 {
		t.Fatalf("fourth per-user upload status = %d, want 429", status)
	}
}
