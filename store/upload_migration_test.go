package store_test

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"ride-backend/models"
	"ride-backend/testutil"
)

func TestMigrationUsesDatabaseMetadataAndRevokesPublicExposure(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "public"), 0o750); err != nil {
		t.Fatal(err)
	}
	publicName := "licence_misleading_photo.jpg"
	documentPath := filepath.Join(root, "public", "private-card.jpg")
	for _, path := range []string{filepath.Join(root, publicName), documentPath, filepath.Join(root, "public", "unclassified.jpg")} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	user, err := s.CreateUser("Driver", uuid.NewString()+"@test.invalid", "", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DB().Model(&models.DriverProfile{}).Where("user_id = ?", user.ID).Update("profile_picture_url", "/api/v1/uploads/"+publicName).Error; err != nil {
		t.Fatal(err)
	}
	doc := models.DriverDocument{ID: uuid.NewString(), DriverID: user.ID, Type: "vehicle_card", FilePath: documentPath, FileURL: "/api/v1/uploads/private-card.jpg", Status: "submitted"}
	if err := s.DB().Create(&doc).Error; err != nil {
		t.Fatal(err)
	}
	dry, err := s.MigrateLegacyUploads(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Moved != 0 || dry.Private != 1 || dry.Quarantined != 1 {
		t.Fatalf("dry counts: %+v", dry)
	}
	if _, err := s.MigrateLegacyUploads(root, false); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().First(&doc, "id = ?", doc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if doc.FilePath == documentPath || doc.FileURL != "/api/v1/files/documents/"+doc.ID {
		t.Fatal("private metadata was not repaired")
	}
	if _, err := os.Stat(doc.FilePath); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	// Use Fiber's underlying static-file engine without an open-file cache so
	// the fixture directory can be removed immediately on Windows.
	fs := &fasthttp.FS{Root: filepath.Join(root, "public"), SkipCache: true, PathRewrite: func(ctx *fasthttp.RequestCtx) []byte {
		return []byte(strings.TrimPrefix(string(ctx.Path()), "/api/v1/uploads"))
	}}
	handler := fs.NewRequestHandler()
	app.Get("/api/v1/uploads/*", func(c *fiber.Ctx) error { handler(c.Context()); return nil })
	for _, tc := range []struct {
		name   string
		status int
	}{{publicName, 200}, {"private-card.jpg", 404}, {"unclassified.jpg", 404}} {
		resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/uploads/"+tc.name, nil))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("public route %s = %d, want %d", tc.name, resp.StatusCode, tc.status)
		}
	}
	if again, err := s.MigrateLegacyUploads(root, false); err != nil || again.Moved != 0 {
		t.Fatalf("unsafe restart: %+v %v", again, err)
	}
}
