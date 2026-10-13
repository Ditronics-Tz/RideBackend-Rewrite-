package main

import (
	"bytes"
	"context"
	"testing"
)

func clearMigrationEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"APP_ENV", "DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME", "DB_SSLMODE", "STORAGE_DRIVER", "R2_ACCOUNT_ID", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY", "R2_PUBLIC_BUCKET", "R2_PRIVATE_BUCKET", "R2_PUBLIC_BASE_URL", "JWT_SECRET", "STAFF_JWT_SECRET", "STAFF_TOTP_KEY"} {
		t.Setenv(name, "")
	}
}

func TestMigrationConfigRequiresNoAuthOrR2CredentialsForInventory(t *testing.T) {
	clearMigrationEnv(t)
	t.Setenv("STORAGE_DRIVER", "r2")
	t.Setenv("DB_NAME", "migration_test")
	cfg, err := migrationConfigFromEnv()
	if err != nil || cfg.DBName != "migration_test" || cfg.R2AccessKeyID != "" || cfg.JWTSecret != "" || cfg.DBSSLMode != "disable" {
		t.Fatalf("inventory config unexpectedly requires unrelated settings: %v", err)
	}
}

func TestMigrationProductionDatabaseTLSDefault(t *testing.T) {
	clearMigrationEnv(t)
	t.Setenv("APP_ENV", "production")
	cfg, err := migrationConfigFromEnv()
	if err != nil || cfg.DBSSLMode != "require" {
		t.Fatalf("production TLS default: %v", err)
	}
	t.Setenv("DB_SSLMODE", "verify-full")
	cfg, err = migrationConfigFromEnv()
	if err != nil || cfg.DBSSLMode != "verify-full" {
		t.Fatalf("explicit TLS mode: %v", err)
	}
}

func TestMigrationRealRunRejectsMissingStorageBeforeConnectingDatabase(t *testing.T) {
	clearMigrationEnv(t)
	for _, driver := range []string{"local", "r2"} {
		t.Setenv("STORAGE_DRIVER", driver)
		var output bytes.Buffer
		err := run(context.Background(), t.TempDir(), false, &output)
		if err != errMigration || output.Len() != 0 {
			t.Fatalf("missing R2 configuration should fail before database connection: %v", err)
		}
	}
}
