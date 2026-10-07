package config_test

import (
	"strings"
	"testing"
	"time"

	"ride-backend/config"
)

func TestConfigValidation(t *testing.T) {
	validStaffSecret := "abcdefghijklmnopqrstuvwxyz123456" // 32 chars
	validTOTPKey := "12345678901234567890123456789012"     // 32 chars

	t.Run("Missing JWT_SECRET fails", func(t *testing.T) {
		cfg := &config.Config{
			JWTSecret:       "",
			StaffJWTSecret:  validStaffSecret,
			DBPassword:      "pass",
			StaffTOTPKey:    []byte(validTOTPKey),
			StaffAccessTTL:  15 * time.Minute,
			StaffRefreshTTL: 7 * 24 * time.Hour,
		}
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
			t.Fatalf("Expected JWT_SECRET required error, got %v", err)
		}
	})

	t.Run("Missing STAFF_JWT_SECRET fails", func(t *testing.T) {
		cfg := &config.Config{
			JWTSecret:       "app_secret",
			StaffJWTSecret:  "",
			DBPassword:      "pass",
			StaffTOTPKey:    []byte(validTOTPKey),
			StaffAccessTTL:  15 * time.Minute,
			StaffRefreshTTL: 7 * 24 * time.Hour,
		}
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "STAFF_JWT_SECRET") {
			t.Fatalf("Expected STAFF_JWT_SECRET required error, got %v", err)
		}
	})

	t.Run("STAFF_JWT_SECRET under 32 bytes fails", func(t *testing.T) {
		cfg := &config.Config{
			JWTSecret:       "app_secret",
			StaffJWTSecret:  "short_secret_under_32_bytes",
			DBPassword:      "pass",
			StaffTOTPKey:    []byte(validTOTPKey),
			StaffAccessTTL:  15 * time.Minute,
			StaffRefreshTTL: 7 * 24 * time.Hour,
		}
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "32 bytes") {
			t.Fatalf("Expected 32 bytes error, got %v", err)
		}
	})

	t.Run("STAFF_JWT_SECRET same as JWT_SECRET fails", func(t *testing.T) {
		same := "same_secret_for_both_realms_1234567890"
		cfg := &config.Config{
			JWTSecret:       same,
			StaffJWTSecret:  same,
			DBPassword:      "pass",
			StaffTOTPKey:    []byte(validTOTPKey),
			StaffAccessTTL:  15 * time.Minute,
			StaffRefreshTTL: 7 * 24 * time.Hour,
		}
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "must differ") {
			t.Fatalf("Expected secrets must differ error, got %v", err)
		}
	})

	t.Run("Missing DB_PASSWORD fails", func(t *testing.T) {
		cfg := &config.Config{
			JWTSecret:       "app_secret",
			StaffJWTSecret:  validStaffSecret,
			DBPassword:      "",
			StaffTOTPKey:    []byte(validTOTPKey),
			StaffAccessTTL:  15 * time.Minute,
			StaffRefreshTTL: 7 * 24 * time.Hour,
		}
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "DB_PASSWORD") {
			t.Fatalf("Expected DB_PASSWORD required error, got %v", err)
		}
	})

	t.Run("STAFF_TOTP_KEY not 32 bytes fails", func(t *testing.T) {
		cfg := &config.Config{
			JWTSecret:       "app_secret",
			StaffJWTSecret:  validStaffSecret,
			DBPassword:      "pass",
			StaffTOTPKey:    []byte("short_key"),
			StaffAccessTTL:  15 * time.Minute,
			StaffRefreshTTL: 7 * 24 * time.Hour,
		}
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "STAFF_TOTP_KEY") {
			t.Fatalf("Expected STAFF_TOTP_KEY error, got %v", err)
		}
	})

	t.Run("Valid config succeeds", func(t *testing.T) {
		cfg := &config.Config{
			JWTSecret:       "app_secret_different",
			StaffJWTSecret:  validStaffSecret,
			DBPassword:      "secure_pass",
			StaffTOTPKey:    []byte(validTOTPKey),
			StaffAccessTTL:  15 * time.Minute,
			StaffRefreshTTL: 7 * 24 * time.Hour,
		}
		err := cfg.Validate()
		if err != nil {
			t.Fatalf("Expected valid config to pass, got %v", err)
		}
	})
}
