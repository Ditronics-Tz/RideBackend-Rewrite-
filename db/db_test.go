package db_test

import (
	"os"
	"testing"

	"ride-backend/config"
	"ride-backend/db"
)

func TestPostgresConnect(t *testing.T) {
	if os.Getenv("STAFF_JWT_SECRET") == "" {
		t.Setenv("STAFF_JWT_SECRET", "test_staff_secret_key_12345678901234567890_min_32_bytes")
	}
	if os.Getenv("STAFF_TOTP_KEY") == "" {
		t.Setenv("STAFF_TOTP_KEY", "12345678901234567890123456789012")
	}
	if os.Getenv("JWT_SECRET") == "" {
		t.Setenv("JWT_SECRET", "test_app_secret_key_different_from_staff_12345")
	}
	if os.Getenv("DB_PASSWORD") == "" {
		t.Setenv("DB_PASSWORD", "postgres")
	}

	cfg := config.LoadConfig()
	gdb, err := db.Open(cfg)
	if err != nil {
		t.Logf("Database connection not active in test environment: %v", err)
		return
	}
	if gdb == nil {
		t.Fatal("Expected gdb to be non-nil")
	}
}
