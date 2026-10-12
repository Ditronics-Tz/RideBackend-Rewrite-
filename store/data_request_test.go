package store_test

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	backenddb "ride-backend/db"
	"ride-backend/models"
	backendstore "ride-backend/store"
	"ride-backend/testutil"
)

func TestExportContainsSectionsAndNoPasswordHash(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser("Export Name", "export@example.test", "$2a$12$secret", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("..", "uploads", "private", "test-exports")
	_ = os.RemoveAll(dir)
	defer os.RemoveAll(dir)
	cfg.ExportDir = dir
	if _, err = s.RequestExport(u.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessDataRequests(cfg); err != nil {
		t.Fatal(err)
	}
	var req models.DataExportRequest
	if err = s.DB().Where("user_id = ?", u.ID).First(&req).Error; err != nil {
		t.Fatal(err)
	}
	if req.Status != "ready" {
		t.Fatalf("status=%s", req.Status)
	}
	zr, err := zip.OpenReader(req.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var content string
	for _, f := range zr.File {
		if f.Name == "data.json" {
			rc, e := f.Open()
			if e != nil {
				t.Fatal(e)
			}
			b, e := io.ReadAll(rc)
			_ = rc.Close()
			if e != nil {
				t.Fatal(e)
			}
			content = string(b)
		}
	}
	if content == "" {
		t.Fatal("data.json missing")
	}
	var sections map[string]any
	if err = json.Unmarshal([]byte(content), &sections); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"account", "profiles", "ratings", "promos_and_referrals", "data_requests"} {
		if _, ok := sections[key]; !ok {
			t.Errorf("missing section %s", key)
		}
	}
	if strings.Contains(strings.ToLower(content), "password_hash") || strings.Contains(content, "$2a$12$secret") {
		t.Fatal("password hash was included in export")
	}
}

func TestDeletionAnonymizesPersonalColumnsAndIsIdempotent(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser("Miriam Test", "miriam@example.test", "hash", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	const phone = "+255712345678"
	const plate = "T123ABC"
	fileSuffix := uuid.NewString() + ".jpg"
	ownedFiles := []string{filepath.Join("uploads", "public", fileSuffix), filepath.Join("uploads", "licences", fileSuffix), filepath.Join("uploads", "documents", fileSuffix)}
	for _, p := range ownedFiles {
		if err = os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(p, []byte("private test upload"), 0o600); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(p)
	}
	_ = s.DB().Model(&models.User{}).Where("id = ?", u.ID).Updates(map[string]any{"phone": phone, "status": "active", "is_active": true}).Error
	_ = s.DB().Model(&models.DriverProfile{}).Where("user_id = ?", u.ID).Updates(map[string]any{"licence_number": "LIC-123", "vehicle_card_number": "CARD-123", "plate_number": plate, "licence_image_url": "/api/v1/files/licences/" + fileSuffix, "profile_picture_url": "/api/v1/uploads/" + fileSuffix}).Error
	_ = s.DB().Create(&models.DriverDocument{ID: "doc-" + u.ID, DriverID: u.ID, Type: "driving_licence", FilePath: ownedFiles[2], FileURL: "/api/v1/files/documents/doc", RejectionNote: "Miriam Test phone " + phone}).Error
	_ = s.DB().Create(&models.SupportNote{ID: "note-" + u.ID, UserID: u.ID, AuthorID: "staff", Body: "Miriam Test " + phone}).Error
	_ = s.DB().Create(&models.UserStatusEvent{ID: "event-" + u.ID, UserID: u.ID, Reason: "Miriam Test " + phone}).Error
	_ = s.DB().Create(&models.DriverDuplicateFlag{ID: "flag-" + u.ID, DriverID: u.ID, MatchedDriverID: "other", Field: "plate", Value: plate}).Error
	_ = s.DB().Create(&models.DriverNotification{ID: "notify-" + u.ID, DriverID: u.ID, DocumentID: "doc-" + u.ID, Message: "Miriam Test " + phone}).Error
	_ = s.DB().Create(&models.ReferralRewardPhone{Phone: phone, ReferralID: "ref-" + u.ID}).Error
	_ = s.DB().Create(&models.Referral{ID: "ref-" + u.ID, ReferrerID: u.ID, RefereeID: "another-user", Status: "rejected", RejectReason: "Miriam Test " + phone}).Error
	other, err := s.CreateUser("Other User", "other-user@example.test", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.DB().Create(&models.Rating{ID: "given-" + u.ID, FromUserID: u.ID, ToUserID: other.ID, Score: 4, Comment: "Miriam Test " + phone}).Error
	_ = s.DB().Create(&models.Rating{ID: "received-" + u.ID, FromUserID: other.ID, ToUserID: u.ID, Score: 5, Comment: "Miriam Test " + phone}).Error
	dir := filepath.Join("..", "uploads", "private", "test-exports")
	_ = os.RemoveAll(dir)
	defer os.RemoveAll(dir)
	cfg.ExportDir = dir
	cfg.DeletionGraceDays = 0
	_, _, err = s.RequestDeletion(u.ID, "user", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessDataRequests(cfg); err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessDataRequests(cfg); err != nil {
		t.Fatal(err)
	}
	needles := []string{"Miriam Test", "miriam@example.test", phone, plate, "LIC-123", "CARD-123"}
	for _, model := range backenddb.AutoMigrateModels() {
		// Scan every migrated table column, including fields omitted from JSON.
		schemaStmt := &gorm.Statement{DB: s.DB()}
		if e := schemaStmt.Parse(model); e != nil {
			t.Fatal(e)
		}
		table := schemaStmt.Schema.Table
		rows, e := s.DB().Raw("SELECT * FROM \"" + table + "\"").Rows()
		if e != nil {
			t.Fatal(e)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range values {
				dest[i] = &values[i]
			}
			if e = rows.Scan(dest...); e != nil {
				t.Fatal(e)
			}
			for _, v := range values {
				str := fmt.Sprint(v)
				for _, needle := range needles {
					if strings.Contains(str, needle) {
						t.Errorf("PII %q remains in %s", needle, table)
					}
				}
			}
		}
		_ = rows.Close()
	}
	var got models.User
	if err = s.DB().First(&got, "id = ?", u.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != "deleted" || got.IsActive || got.DeletedAt == nil || got.Email != "deleted+"+u.ID+"@deleted.invalid" {
		t.Fatalf("user not anonymized: %#v", got)
	}
	for _, p := range ownedFiles {
		if _, e := os.Stat(p); !os.IsNotExist(e) {
			t.Errorf("owned upload remains on disk: %s", p)
		}
	}
	var preserved []models.Rating
	if err = s.DB().Where("from_user_id = ? OR to_user_id = ?", u.ID, u.ID).Find(&preserved).Error; err != nil {
		t.Fatal(err)
	}
	if len(preserved) != 2 {
		t.Fatalf("ratings were not retained: %d", len(preserved))
	}
	for _, rating := range preserved {
		if rating.Comment != "" {
			t.Error("rating comment remains after deletion")
		}
	}
}

func TestDeletionGraceCancelAndExportRateLimit(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser("Cancel", "cancel@example.test", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.RequestDeletion(u.ID, "user", "", 14)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "pending" {
		t.Fatalf("status=%s", r.Status)
	}
	_, err = s.CancelDeletion(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetUserByID(u.ID)
	if got.Status != "active" || !got.IsActive {
		t.Fatalf("cancel did not restore account: %s", got.Status)
	}
	if _, err = s.RequestExport(u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RequestExport(u.ID); err == nil {
		t.Fatal("repeat export request should be limited")
	}
}

func TestDeletionBlockerHookAndHeldRequestsAreNotProcessed(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser("Blocked", "blocked@example.test", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	remove := backendstore.RegisterDeletionBlocker("test", func(_ *backendstore.Store, id string) ([]string, error) {
		if id == u.ID {
			return []string{"ongoing ride"}, nil
		}
		return nil, nil
	})
	defer remove()
	r, reasons, err := s.RequestDeletion(u.ID, "user", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "blocked" || len(reasons) != 1 {
		t.Fatalf("blocked request not recorded")
	}
	_ = s.DB().Model(&models.AccountDeletionRequest{}).Where("id = ?", r.ID).Updates(map[string]any{"status": "pending", "scheduled_for": time.Now().Add(-time.Minute)})
	_ = s.DB().Model(&models.AccountDeletionRequest{}).Where("id = ?", r.ID).Update("status", "on_hold").Error
	if err = s.ProcessDataRequests(cfg); err != nil {
		t.Fatal(err)
	}
	var check models.AccountDeletionRequest
	_ = s.DB().First(&check, "id = ?", r.ID).Error
	if check.Status != "on_hold" {
		t.Fatalf("held request processed: %s", check.Status)
	}
}
