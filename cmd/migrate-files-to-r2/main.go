package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"ride-backend/config"
	"ride-backend/storage"
	"ride-backend/store"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "report counts without contacting R2 or changing database rows")
	root := flag.String("root", "./uploads", "local uploads directory")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, *root, *dryRun, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "file migration failed; database, provider and file details are redacted")
		os.Exit(1)
	}
}

var errMigration = errors.New("file migration failed; database, provider and file details are redacted")

func run(ctx context.Context, root string, dryRun bool, output io.Writer) (runErr error) {
	// Read injected environment only; never load .env or run schema migrations.
	cfg, err := migrationConfigFromEnv()
	if err != nil {
		return errMigration
	}
	var destination storage.Storage
	if !dryRun {
		if cfg.StorageDriver != "r2" {
			return errMigration
		}
		destination, err = storage.NewR2(storage.R2Config{AccountID: cfg.R2AccountID, AccessKeyID: cfg.R2AccessKeyID, SecretAccessKey: cfg.R2SecretAccessKey, PublicBucket: cfg.R2PublicBucket, PrivateBucket: cfg.R2PrivateBucket, PublicBaseURL: cfg.R2PublicBaseURL})
		if err != nil {
			return errMigration
		}
	}
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return errMigration
	}
	sqlDB, err := db.DB()
	if err != nil {
		return errMigration
	}
	defer func() {
		if err := sqlDB.Close(); runErr == nil && err != nil {
			runErr = errMigration
		}
	}()
	counts, err := store.New(db).MigrateFilesToR2(ctx, root, destination, dryRun)
	if encodeErr := json.NewEncoder(output).Encode(counts); encodeErr != nil {
		return errMigration
	}
	if err != nil {
		return errMigration
	}
	return nil
}

// Authentication and payment settings are unrelated to a one-shot file copy.
// A dry-run therefore only needs database settings and the local source tree.
func migrationConfigFromEnv() (*config.Config, error) {
	setting := func(name, fallback string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return fallback
		}
		return value
	}
	appEnv := setting("APP_ENV", "development")
	if appEnv != "development" && appEnv != "production" {
		return nil, errMigration
	}
	sslDefault := "disable"
	if appEnv == "production" {
		sslDefault = "require"
	}
	return &config.Config{
		AppEnv: appEnv, DBHost: setting("DB_HOST", "127.0.0.1"), DBPort: setting("DB_PORT", "5432"), DBUser: setting("DB_USER", "postgres"), DBPassword: os.Getenv("DB_PASSWORD"), DBName: setting("DB_NAME", "ride_db"), DBSSLMode: setting("DB_SSLMODE", sslDefault),
		StorageDriver: setting("STORAGE_DRIVER", "local"), R2AccountID: setting("R2_ACCOUNT_ID", ""), R2AccessKeyID: os.Getenv("R2_ACCESS_KEY_ID"), R2SecretAccessKey: os.Getenv("R2_SECRET_ACCESS_KEY"), R2PublicBucket: setting("R2_PUBLIC_BUCKET", ""), R2PrivateBucket: setting("R2_PRIVATE_BUCKET", ""), R2PublicBaseURL: setting("R2_PUBLIC_BASE_URL", ""),
	}, nil
}
