package main

import (
	"bytes"
	"context"
	"testing"
)

func TestSchemaCommandRefusesDownWithoutConnecting(t *testing.T) {
	var output bytes.Buffer
	if err := run(context.Background(), "down", &output); err != errCommand || output.Len() != 0 {
		t.Fatal("unsupported rollback must be refused")
	}
}

func TestSchemaCommandConfigurationUsesInjectedDBOnly(t *testing.T) {
	for _, name := range []string{"APP_ENV", "DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME", "DB_SSLMODE", "JWT_SECRET", "STAFF_JWT_SECRET", "STAFF_TOTP_KEY", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY"} {
		t.Setenv(name, "")
	}
	t.Setenv("APP_ENV", "production")
	cfg, err := databaseConfigFromEnv()
	if err != nil || cfg.DBSSLMode != "require" || cfg.JWTSecret != "" || cfg.R2AccessKeyID != "" {
		t.Fatal("database command required unrelated settings or insecure TLS")
	}
	t.Setenv("DB_SSLMODE", "verify-full")
	cfg, err = databaseConfigFromEnv()
	if err != nil || cfg.DBSSLMode != "verify-full" {
		t.Fatal("explicit TLS setting ignored")
	}
}
