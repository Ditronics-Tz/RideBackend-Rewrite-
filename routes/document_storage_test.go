package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http/httptest"
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

type signedLocalStorage struct {
	storage.Storage
	headType string
	headSize int64
}

func (s *signedLocalStorage) PresignPut(_ context.Context, v storage.Visibility, key, contentType string, ttl time.Duration) (storage.PresignedURL, error) {
	return storage.PresignedURL{URL: "https://objects.example.invalid/" + key, Method: "PUT", Headers: map[string]string{"Content-Type": contentType}, ExpiresAt: time.Now().Add(ttl)}, nil
}
func (s *signedLocalStorage) PresignGet(_ context.Context, v storage.Visibility, key string, ttl time.Duration) (storage.PresignedURL, error) {
	return storage.PresignedURL{URL: "https://objects.example.invalid/" + key, Method: "GET", ExpiresAt: time.Now().Add(ttl)}, nil
}
func (s *signedLocalStorage) Head(ctx context.Context, v storage.Visibility, key string) (storage.ObjectInfo, error) {
	info, err := s.Storage.Head(ctx, v, key)
	if s.headType != "" {
		info.ContentType = s.headType
	}
	if s.headSize != 0 {
		info.Size = s.headSize
	}
	return info, err
}

func TestObjectDocumentLifecycleOwnershipAuditAndImmutability(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	local, err := storage.NewLocal(storage.LocalConfig{PublicDir: filepath.Join(root, "public"), PrivateDir: filepath.Join(root, "private"), PublicBaseURL: "/api/v1/uploads"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := local.Close(); err != nil {
			t.Error(err)
		}
	})
	provider := &signedLocalStorage{Storage: local}
	s.Storage = provider
	cfg.StorageDriver = "r2"
	app := routes.BuildApp(cfg, s)
	driver, err := s.CreateUser("Driver", uuid.NewString()+"@test.invalid", "", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser("Other", uuid.NewString()+"@test.invalid", "", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	staff, err := s.CreateStaffUser("Support", uuid.NewString()+"@test.invalid", "", models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	token, err := testutil.AppToken(s, driver.ID, driver.Role, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, err := testutil.AppToken(s, other.ID, other.Role, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	staffToken, err := testutil.StaffToken(s, staff.ID, staff.Role, cfg.StaffJWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, bearer, body string, status int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := app.Test(req, 10000)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != status {
			t.Fatalf("%s %s returned %d, want %d: %s", method, path, resp.StatusCode, status, raw)
		}
		return raw
	}
	body, _ := json.Marshal(map[string]any{"type": "vehicle_card", "content_type": "image/png", "size": data.Len()})
	raw := request("POST", "/api/v1/driver/documents/presign", token, string(body), 201)
	var out struct {
		Document models.DriverDocument `json:"document"`
		Upload   storage.PresignedURL  `json:"upload"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	var doc models.DriverDocument
	if err := s.DB().First(&doc, "id = ?", out.Document.ID).Error; err != nil {
		t.Fatal(err)
	}
	if doc.Status != "pending_upload" || doc.IsCurrent || doc.FileURL != "" || !strings.HasPrefix(doc.ObjectKey, "staging/documents/") {
		t.Fatal("pending object metadata invalid")
	}
	staging := doc.ObjectKey
	if _, err := provider.Put(context.Background(), storage.Private, staging, storage.Upload{Body: bytes.NewReader(data.Bytes()), Size: int64(data.Len()), ContentType: "image/png"}); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/driver/documents/" + doc.ID + "/confirm"
	request("POST", path, otherToken, "", 404)
	for _, tc := range []struct {
		name, kind string
		size       int64
	}{{"wrong type", "text/html", 0}, {"wrong size", "", int64(data.Len() + 1)}} {
		t.Run(tc.name, func(t *testing.T) {
			provider.headType = tc.kind
			provider.headSize = tc.size
			request("POST", path, token, "", 400)
		})
	}
	provider.headType = ""
	provider.headSize = 0
	request("POST", path, token, "", 200)
	request("POST", path, token, "", 200)
	if err := s.DB().First(&doc, "id = ?", doc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if doc.ObjectKey == staging || !strings.HasPrefix(doc.ObjectKey, "documents/") || doc.Status != "submitted" || !doc.IsCurrent || doc.StagingObjectKey != "" {
		t.Fatal("confirmation did not promote immutable evidence")
	}
	if _, err := provider.Put(context.Background(), storage.Private, staging, storage.Upload{Body: bytes.NewReader([]byte("replacement")), Size: 11, ContentType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	reader, _, err := provider.Get(context.Background(), storage.Private, doc.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, data.Bytes()) {
		t.Fatal("reusing staged upload mutated confirmed document")
	}
	get := "/api/v1/files/documents/" + doc.ID + "?presign=true"
	request("GET", get, "", "", 401)
	request("GET", get, otherToken, "", 403)
	request("GET", get, token, "", 200)
	request("GET", get, staffToken, "", 200)
	var audits int64
	if err := s.DB().Model(&models.StaffAuditLog{}).Where("actor_id = ? AND target_id = ? AND action = ?", staff.ID, doc.ID, "view_driver_document").Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("staff reads audited %d times, want 1", audits)
	}
}
