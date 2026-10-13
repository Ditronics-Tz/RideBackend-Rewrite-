package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"ride-backend/config"
	"ride-backend/migrations"
)

var errCommand = errors.New("schema migration command failed; database details and personal data are redacted")

func main() {
	timeout := flag.Duration("timeout", 30*time.Minute, "maximum time for migration or status")
	flag.Parse()
	command := "up"
	if flag.NArg() > 0 {
		command = flag.Arg(0)
	}
	if flag.NArg() > 1 || *timeout <= 0 {
		fmt.Fprintln(os.Stderr, errCommand)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, deadlineCancel := context.WithTimeout(ctx, *timeout)
	defer deadlineCancel()
	if err := run(ctx, command, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, command string, output io.Writer) error {
	if command != "up" && command != "status" {
		return errCommand
	}
	cfg, err := databaseConfigFromEnv()
	if err != nil {
		return errCommand
	}
	db, err := sql.Open("pgx", cfg.DSN()+" connect_timeout=10")
	if err != nil {
		return errCommand
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	if err := db.PingContext(ctx); err != nil {
		return errCommand
	}
	if command == "up" {
		if err := migrations.Up(ctx, db); err != nil {
			if errors.Is(err, migrations.ErrIncompatibleSchema) {
				return migrations.ErrIncompatibleSchema
			}
			return errCommand
		}
	}
	version, err := migrations.Version(ctx, db)
	if err != nil {
		return errCommand
	}
	result := struct {
		Version       int64 `json:"version"`
		LatestVersion int64 `json:"latest_version"`
		Pending       bool  `json:"pending"`
	}{version, migrations.LatestVersion, version < migrations.LatestVersion}
	if err := json.NewEncoder(output).Encode(result); err != nil {
		return errCommand
	}
	return nil
}

// Only injected database settings are read. Authentication, payment and storage
// credentials are unnecessary; .env loading and AutoMigrate are never invoked.
func databaseConfigFromEnv() (*config.Config, error) {
	setting := func(name, fallback string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return fallback
		}
		return value
	}
	appEnv := setting("APP_ENV", "development")
	if appEnv != "development" && appEnv != "production" {
		return nil, errCommand
	}
	sslDefault := "disable"
	if appEnv == "production" {
		sslDefault = "require"
	}
	return &config.Config{AppEnv: appEnv, DBHost: setting("DB_HOST", "127.0.0.1"), DBPort: setting("DB_PORT", "5432"), DBUser: setting("DB_USER", "postgres"), DBPassword: os.Getenv("DB_PASSWORD"), DBName: setting("DB_NAME", "ride_db"), DBSSLMode: setting("DB_SSLMODE", sslDefault)}, nil
}
