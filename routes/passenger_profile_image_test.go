package routes_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/storage"
	"ride-backend/testutil"
)

func TestPassengerProfileImageContract(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg.PublicBaseURL = "https://api.example.test"
	owner, err := s.CreateUser("Owner", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser("Other", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := s.CreateUser("Driver", uuid.NewString()+"@example.test", "unused", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	token := func(id string, role models.Role) string {
		t.Helper()
		value, err := testutil.AppToken(s, id, role, cfg.JWTSecret, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	ownerToken, otherToken, driverToken := token(owner.ID, owner.Role), token(other.ID, other.Role), token(driver.ID, driver.Role)
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	get := func(auth string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/passenger/profile/me", nil)
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, body
	}
	post := func(auth, field, filename string, content []byte, extra bool) (int, map[string]any) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile(field, filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatal(err)
		}
		if extra {
			_ = writer.WriteField("user_id", other.ID)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/api/v1/passenger/profile/me/image", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, result
	}
	imageBytes := func(format string) []byte {
		t.Helper()
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		img.Set(0, 0, color.RGBA{R: 255, A: 255})
		var out bytes.Buffer
		var err error
		if format == "jpeg" {
			err = jpeg.Encode(&out, img, nil)
		} else {
			err = png.Encode(&out, img)
		}
		if err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	var invalidDimensions bytes.Buffer
	if err := png.Encode(&invalidDimensions, image.NewRGBA(image.Rect(0, 0, 4097, 1))); err != nil {
		t.Fatal(err)
	}
	if status, body := get(ownerToken); status != 200 || body["profile_picture_url"] != nil {
		t.Fatalf("empty profile: %d %#v", status, body)
	}
	if status, _ := post("", "profile_picture", "a.png", imageBytes("png"), false); status != 401 {
		t.Fatalf("unauthorized upload: %d", status)
	}
	if status, _ := post(driverToken, "profile_picture", "a.png", imageBytes("png"), false); status != 403 {
		t.Fatalf("driver upload: %d", status)
	}
	wrongType := httptest.NewRequest("POST", "/api/v1/passenger/profile/me/image", strings.NewReader("{}"))
	wrongType.Header.Set("Content-Type", "application/json")
	wrongType.Header.Set("Authorization", "Bearer "+ownerToken)
	wrongResponse, err := app.Test(wrongType, -1)
	if err != nil {
		t.Fatal(err)
	}
	wrongResponse.Body.Close()
	if wrongResponse.StatusCode != 415 {
		t.Fatalf("wrong multipart content type: %d", wrongResponse.StatusCode)
	}
	if status, _ := get(""); status != 401 {
		t.Fatalf("unauthorized profile read: %d", status)
	}
	status, body := post(ownerToken, "profile_picture", "first.jpg", imageBytes("jpeg"), true)
	if status != 200 {
		t.Fatalf("JPEG upload: %d %#v", status, body)
	}
	first := body["profile_picture_url"].(string)
	assertPhotoURL := func(raw string) string {
		t.Helper()
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "api.example.test" || !strings.HasSuffix(parsed.Path, ".png") {
			t.Fatalf("bad public URL: %q %v", raw, err)
		}
		req := httptest.NewRequest("GET", parsed.Path, nil)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 || !bytes.HasPrefix(data, []byte("\x89PNG")) {
			t.Fatalf("unauthed image read: %d", resp.StatusCode)
		}
		return strings.TrimPrefix(parsed.Path, "/api/v1/uploads/")
	}
	firstParsed, err := url.Parse(first)
	if err != nil || firstParsed.Scheme != "https" || firstParsed.Host != "api.example.test" {
		t.Fatalf("first public URL: %q %v", first, err)
	}
	firstName := strings.TrimPrefix(firstParsed.Path, "/api/v1/uploads/")
	if stored, ok := s.GetPassengerProfile(owner.ID); !ok || stored.ProfilePictureURL != "/api/v1/uploads/"+firstName {
		t.Fatalf("stored first photo reference: %#v", stored)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join("uploads", "public", firstName)) })
	if status, profile := get(ownerToken); status != 200 || profile["profile_picture_url"] != first {
		t.Fatalf("readback: %d %#v", status, profile)
	}
	if status, profile := get(otherToken); status != 200 || profile["profile_picture_url"] != nil {
		t.Fatalf("account isolation: %d %#v", status, profile)
	}
	for _, tc := range []struct {
		name, field string
		content     []byte
		want        int
	}{
		{"empty", "profile_picture", nil, 422},
		{"missing field", "wrong", imageBytes("png"), 422},
		{"unsupported", "profile_picture", []byte("GIF89a"), 415},
		{"invalid", "profile_picture", []byte("\x89PNG\r\n\x1a\ncorrupt"), 422},
		{"invalid dimensions", "profile_picture", invalidDimensions.Bytes(), 422},
		{"oversized", "profile_picture", bytes.Repeat([]byte{'x'}, 5*1024*1024+1), 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, result := post(ownerToken, tc.field, "bad.png", tc.content, false)
			if status != tc.want || result["error"] == nil {
				t.Fatalf("want %d, got %d %#v", tc.want, status, result)
			}
			if _, profile := get(ownerToken); profile["profile_picture_url"] != first {
				t.Fatal("failed upload changed saved photo")
			}
		})
	}
	status, body = post(ownerToken, "profile_picture", "second.png", imageBytes("png"), false)
	if status != 200 {
		t.Fatalf("replacement: %d %#v", status, body)
	}
	second := body["profile_picture_url"].(string)
	if second == first {
		t.Fatal("replacement reused cached URL")
	}
	secondName := assertPhotoURL(second)
	t.Cleanup(func() { _ = os.Remove(filepath.Join("uploads", "public", secondName)) })
	if status, profile := get(ownerToken); status != 200 || profile["profile_picture_url"] != second {
		t.Fatalf("replacement readback: %d %#v", status, profile)
	}
	if _, err := os.Stat(filepath.Join("uploads", "public", firstName)); !os.IsNotExist(err) {
		t.Fatalf("old photo was not removed: %v", err)
	}
	webp, err := base64.StdEncoding.DecodeString("UklGRjwAAABXRUJQVlA4IDAAAADQAQCdASoCAAIAAgA0JaACdLoB+AADsAD+8MQL/yC5YXXI1/8gP+QH/ID/+PIAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	status, body = post(ownerToken, "profile_picture", "third.webp", webp, false)
	if status != 200 {
		t.Fatalf("WebP upload: %d %#v", status, body)
	}
	third := body["profile_picture_url"].(string)
	if third == second {
		t.Fatal("WebP replacement reused cached URL")
	}
	thirdName := assertPhotoURL(third)
	t.Cleanup(func() { _ = os.Remove(filepath.Join("uploads", "public", thirdName)) })
	if status, profile := get(ownerToken); status != 200 || profile["profile_picture_url"] != third {
		t.Fatalf("WebP readback: %d %#v", status, profile)
	}
	if status, _ := post(ownerToken, "profile_picture", "fourth.png", imageBytes("png"), false); status != 429 {
		t.Fatalf("fourth validated upload: %d, want 429", status)
	}
	if status, _ := get(otherToken); status != 200 {
		t.Fatalf("other account profile: %d", status)
	}
}

func TestPassengerProfileImageR2URLsAndReplacement(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	local, err := storage.NewLocal(storage.LocalConfig{PublicDir: filepath.Join(root, "public"), PrivateDir: filepath.Join(root, "private"), PublicBaseURL: "https://images.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	s.Storage = local
	cfg.StorageDriver = "r2"
	cfg.R2PublicBaseURL = "https://images.example.test"
	user, err := s.CreateUser("Owner", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	token, err := testutil.AppToken(s, user.ID, user.Role, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	var content bytes.Buffer
	if err := png.Encode(&content, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	upload := func() string {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("profile_picture", "upload.png")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(content.Bytes())
		_ = writer.Close()
		req := httptest.NewRequest("POST", "/api/v1/passenger/profile/me/image", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("upload: %d %#v", resp.StatusCode, result)
		}
		got := result["profile_picture_url"].(string)
		if !strings.HasPrefix(got, "https://images.example.test/profile/"+user.ID+"/") {
			t.Fatalf("R2 URL: %q", got)
		}
		return got
	}
	firstURL := upload()
	firstProfile, ok := s.GetPassengerProfile(user.ID)
	if !ok || firstProfile.ProfilePictureKey == "" {
		t.Fatal("R2 profile key was not persisted")
	}
	firstKey := firstProfile.ProfilePictureKey
	secondURL := upload()
	if firstURL == secondURL {
		t.Fatal("R2 replacement reused URL")
	}
	if _, err := local.Head(t.Context(), storage.Public, firstKey); err != storage.ErrNotFound {
		t.Fatalf("old R2 object remained: %v", err)
	}
	req := httptest.NewRequest("GET", "/api/v1/passenger/profile/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["profile_picture_url"] != secondURL {
		t.Fatalf("R2 readback: %#v", result)
	}
}
