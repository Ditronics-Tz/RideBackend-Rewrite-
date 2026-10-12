package testutil

import (
	"github.com/google/uuid"
	"time"

	"ride-backend/config"
	backenddb "ride-backend/db"
	"ride-backend/store"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// SetupTestStore creates an isolated in-memory DB and returns configured store and config.
func SetupTestStore() (*store.Store, *config.Config, error) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, nil, err
	}

	err = db.AutoMigrate(backenddb.AutoMigrateModels()...)
	if err != nil {
		return nil, nil, err
	}
	if err := backenddb.MigrateVerification(db); err != nil {
		return nil, nil, err
	}
	if err := backenddb.MigrateAccounts(db); err != nil {
		return nil, nil, err
	}

	appStore := store.New(db)
	cfg := &config.Config{
		Port:                    "3000",
		JWTSecret:               "test_app_secret_12345678901234567890",
		StaffJWTSecret:          "test_staff_secret_12345678901234567890_min_32_bytes",
		StaffAccessTTL:          15 * time.Minute,
		StaffRefreshTTL:         7 * 24 * time.Hour,
		StaffTOTPKey:            []byte("12345678901234567890123456789012"),
		StaffRequire2FAForAdmin: true,
		DeletionGraceDays:       14,
		ExportTTLHours:          168,
		ExportDir:               "uploads/private/exports",
	}

	return appStore, cfg, nil
}
