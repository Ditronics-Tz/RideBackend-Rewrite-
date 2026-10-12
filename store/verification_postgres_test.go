package store_test

import (
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"ride-backend/config"
	"ride-backend/db"
	"ride-backend/models"
	"ride-backend/store"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPostgresConcurrentVerification(t *testing.T) {
	if os.Getenv("RUN_POSTGRES_VERIFICATION_TESTS") != "1" {
		t.Skip("Set RUN_POSTGRES_VERIFICATION_TESTS=1 to use an isolated local Postgres schema")
	}
	_ = godotenv.Load("../.env")
	cfg, err := config.NewConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	root, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	rootSQL, _ := root.DB()
	defer rootSQL.Close()
	schema := "verification_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := root.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer root.Exec("DROP SCHEMA " + schema + " CASCADE")
	gdb, err := gorm.Open(postgres.Open(cfg.DSN()+" search_path="+schema), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := gdb.DB()
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(10)
	if err := db.AutoMigrate(gdb); err != nil {
		t.Fatal(err)
	}
	s := store.New(gdb)
	u, err := s.CreateUser("Concurrent Driver", "driver@example.test", "unused", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateDriverIdentifiers(u.ID, models.DriverProfileInput{LicenceNumber: "LIC-1", PlateNumber: "T 123 ABC", VehicleCardNumber: "CARD-1"})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().AddDate(1, 0, 0)
	for _, kind := range models.RequiredDocumentTypes {
		var expiry *time.Time
		if models.RequiresExpiry(kind) {
			expiry = &expires
		}
		if _, err := s.AddDriverDocument(u.ID, kind, "uploads/documents/fixture.png", expiry); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SubmitDriverDocuments(u.ID); err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateStaffUser("Reviewer A", "a@example.test", "unused", models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateStaffUser("Reviewer B", "b@example.test", "unused", models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, actor := range []*models.StaffUser{a, b} {
		wg.Add(1)
		go func(actor *models.StaffUser) { defer wg.Done(); <-start; results <- s.ClaimDriver(u.ID, actor, false) }(actor)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("Exactly one concurrent reviewer must win, got %d", successes)
	}
	detail, err := s.VerificationDetail(u.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Profile.AssignedTo == nil || detail.Profile.VerificationStatus != models.UnderReview {
		t.Fatal("Claim was not persisted")
	}
	var claims int64
	gdb.Model(&models.VerificationReview{}).Where("driver_id = ? AND decision = ?", u.ID, "claimed").Count(&claims)
	if claims != 1 {
		t.Fatalf("Expected one claim history row, got %d", claims)
	}
	if err := db.AutoMigrate(gdb); err != nil {
		t.Fatalf("Migration is not repeatable: %v", err)
	}
	other, err := s.CreateUser("Duplicate", "other@example.test", "unused", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateDriverIdentifiers(other.ID, models.DriverProfileInput{PlateNumber: "t-123 abc"}); err == nil {
		t.Fatal("Canonical duplicate plate accepted")
	}
	// The partial index allows blank profiles but protects writes outside the service too.
	if err := gdb.Model(&models.DriverProfile{}).Where("user_id = ?", other.ID).Update("licence_number", "lic-1").Error; err == nil {
		t.Fatal("Database unique licence index is missing")
	}
}
