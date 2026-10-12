package routes_test

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"image"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
	"ride-backend/utils"
	"testing"
	"time"
)

func TestPrivateDocumentMultipartUpload(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	u, err := s.CreateUser("Uploading Driver", uuid.NewString()+"@example.test", "unused", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	token, _ := utils.GenerateAppToken(u.ID, u.Role, cfg.JWTSecret, time.Hour)
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	upload := func(kind, expiry string, content []byte, want int) *models.DriverDocument {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("type", kind)
		_ = writer.WriteField("expires_at", expiry)
		part, _ := writer.CreateFormFile("file", "document.png")
		_, _ = part.Write(content)
		_ = writer.Close()
		req := httptest.NewRequest("POST", "/api/v1/driver/documents", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			var out interface{}
			_ = json.NewDecoder(resp.Body).Decode(&out)
			t.Fatalf("%s upload: want %d got %d: %v", kind, want, resp.StatusCode, out)
		}
		if want != 201 {
			return nil
		}
		var doc models.DriverDocument
		if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
			t.Fatal(err)
		}
		var saved models.DriverDocument
		s.DB().First(&saved, "id = ?", doc.ID)
		t.Cleanup(func() { _ = os.Remove(saved.FilePath) })
		if doc.DriverID != u.ID || doc.FileURL != "/api/v1/files/documents/"+doc.ID || doc.FilePath != "" {
			t.Fatal("Upload leaked storage path or incorrect owner")
		}
		return &doc
	}
	upload("driving_licence", "", picture.Bytes(), 400)
	upload("vehicle_photo", "", []byte("%PDF-1.4\nfixture"), 400)
	upload("national_id", "", []byte("executable disguised as PNG"), 400)
	upload("unknown", "", picture.Bytes(), 400)
	doc := upload("driving_licence", time.Now().AddDate(1, 0, 0).Format("2006-01-02"), picture.Bytes(), 201)
	if doc.ExpiresAt == nil || doc.ExpiresAt.In(time.FixedZone("EAT", 3*3600)).Hour() != 0 {
		t.Fatal("Expiry must remain valid through the calendar date in Tanzania")
	}
	upload("vehicle_photo", "", picture.Bytes(), 201)
	upload("insurance", time.Now().AddDate(1, 0, 0).Format("2006-01-02"), []byte("%PDF-1.4\nfixture"), 201)
}
