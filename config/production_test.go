package config_test

import (
	"strings"
	"testing"

	"ride-backend/config"
)

func TestProductionSecretValidation(t *testing.T) {
	for _, tc := range []struct{ name, env, app, staff, errorKey string }{
		{"valid", "production", strings.Repeat("aZ4!", 10), strings.Repeat("bY5?", 10), ""},
		{"short app", "production", "short", strings.Repeat("bY5?", 10), "JWT_SECRET"},
		{"same realms", "production", strings.Repeat("aZ4!", 10), strings.Repeat("aZ4!", 10), "must differ"},
		{"app example", "production", "replace-with-a-unique-app-secret-32bytes", strings.Repeat("bY5?", 10), "JWT_SECRET"},
		{"staff example", "production", strings.Repeat("aZ4!", 10), "test_staff_secret_12345678901234567890", "STAFF_JWT_SECRET"},
		{"unknown environment", "prod", strings.Repeat("aZ4!", 10), strings.Repeat("bY5?", 10), "APP_ENV"},
		{"development compatible", "development", "local", strings.Repeat("bY5?", 10), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{AppEnv: tc.env, DBSSLMode: "require", JWTSecret: tc.app, StaffJWTSecret: tc.staff, DBPassword: "local-test", StaffTOTPKey: []byte(strings.Repeat("x", 32))}
			err := cfg.Validate()
			if tc.errorKey == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.errorKey) {
				t.Fatalf("expected refusal for %s, got %v", tc.errorKey, err)
			}
			if strings.Contains(err.Error(), tc.app) || strings.Contains(err.Error(), tc.staff) {
				t.Fatal("validation disclosed a secret")
			}
		})
	}
}
