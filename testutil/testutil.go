package testutil

import (
	"time"

	"ride-backend/config"
	"ride-backend/models"
	"ride-backend/store"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// SetupTestStore creates an isolated in-memory DB and returns configured store and config.
func SetupTestStore() (*store.Store, *config.Config, error) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, nil, err
	}

	err = db.AutoMigrate(
		&models.User{},
		&models.DriverProfile{},
		&models.PassengerProfile{},
		&models.Rating{},
		&models.StaffUser{},
		&models.StaffSession{},
		&models.StaffAuditLog{},
	)
	if err != nil {
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
	}

	return appStore, cfg, nil
}
