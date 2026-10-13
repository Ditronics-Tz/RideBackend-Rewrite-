package config_test

import (
	"github.com/jackc/pgx/v5/pgconn"
	"ride-backend/config"
	"strings"
	"testing"
)

func TestExplicitOrigins(t *testing.T) {
	for _, raw := range []string{"*", "https://*.example.com", "https://example.com/path", "null", "https://user:pass@example.com"} {
		if config.ValidateOrigins(raw) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if !config.OriginAllowed("https://admin.example.com,http://localhost:5173", "https://admin.example.com") {
		t.Fatal("approved origin denied")
	}
	if config.OriginAllowed("", "https://admin.example.com") || config.OriginAllowed("*", "https://admin.example.com") {
		t.Fatal("empty/wildcard allowed")
	}
}

func TestDSNQuotesCredentialValues(t *testing.T) {
	cfg := &config.Config{DBHost: "localhost", DBPort: "5432", DBUser: "fixture user", DBPassword: "fixture' \\ sslmode=disable", DBName: "fixture db", DBSSLMode: "require"}
	parsed, err := pgconn.ParseConfig(cfg.DSN())
	if err != nil {
		t.Fatal("unable to parse quoted configuration")
	}
	if parsed.Password != cfg.DBPassword || parsed.User != cfg.DBUser || parsed.Database != cfg.DBName || parsed.TLSConfig == nil || parsed.ConnectTimeout.Seconds() != 10 {
		t.Fatal("DSN values escaped their fields")
	}
}

func TestProductionDatabaseTLS(t *testing.T) {
	for _, mode := range []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"} {
		cfg := &config.Config{AppEnv: "production", DBSSLMode: mode, JWTSecret: strings.Repeat("aZ4!", 10), StaffJWTSecret: strings.Repeat("bY5?", 10), DBPassword: "fixture", StaffTOTPKey: []byte(strings.Repeat("x", 32))}
		err := cfg.Validate()
		want := mode == "require" || mode == "verify-ca" || mode == "verify-full"
		if (err == nil) != want {
			t.Fatalf("TLS mode %s unexpected: %v", mode, err)
		}
	}
}
