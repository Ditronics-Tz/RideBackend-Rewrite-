package config_test

import (
	"strings"
	"testing"

	"ride-backend/config"
)

func TestStorageConfiguration(t *testing.T) {
	valid := func() *config.Config {
		return &config.Config{AppEnv: "development", JWTSecret: "local", StaffJWTSecret: strings.Repeat("s", 32), DBPassword: "local-test", StaffTOTPKey: []byte(strings.Repeat("x", 32)), StorageDriver: "r2", R2AccountID: "test-account", R2AccessKeyID: "fixture-access", R2SecretAccessKey: "fixture-secret", R2PublicBucket: "public-test", R2PrivateBucket: "private-test", R2PublicBaseURL: "https://images.example.invalid"}
	}
	for _, tc := range []struct {
		name     string
		change   func(*config.Config)
		rejected bool
	}{
		{"r2", func(*config.Config) {}, false},
		{"local", func(c *config.Config) { c.StorageDriver = "local" }, false},
		{"unknown", func(c *config.Config) { c.StorageDriver = "other" }, true},
		{"missing credential", func(c *config.Config) { c.R2SecretAccessKey = "" }, true},
		{"same buckets", func(c *config.Config) { c.R2PrivateBucket = c.R2PublicBucket }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid()
			tc.change(cfg)
			err := cfg.Validate()
			if (err != nil) != tc.rejected {
				t.Fatalf("unexpected configuration result: %v", err)
			}
			if err != nil && (strings.Contains(err.Error(), cfg.R2AccessKeyID) || strings.Contains(err.Error(), "fixture-secret")) {
				t.Fatal("credential included in configuration error")
			}
		})
	}
}
