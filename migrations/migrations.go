// Package migrations applies embedded, versioned PostgreSQL schema changes.
// The HTTP server must not call Up; run cmd/migrate as a deployment step.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed *.sql schema*.json
var files embed.FS

const LatestVersion int64 = 11

var ErrMigration = errors.New("database migration failed; database details and personal data are redacted")
var ErrIncompatibleSchema = errors.New("existing database schema is incompatible with the frozen baseline; review schema drift before migrating")

type columnSpec struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Primary bool   `json:"primary"`
	NotNull bool   `json:"not_null"`
}

var storageColumns = map[string]map[string]bool{
	"driver_profiles":    {"profile_picture_key": true},
	"passenger_profiles": {"profile_picture_key": true},
	"driver_documents":   {"object_key": true, "staging_object_key": true, "content_type": true, "expected_size": true, "upload_expires_at": true},
}

var appAuthColumns = map[string]map[string]bool{
	"users":        {"failed_login_count": true, "locked_until": true},
	"staff_users":  {"auth_version": true, "pending_totp_secret_enc": true, "pending_totp_expires_at": true, "last_accepted_totp_step": true, "totp_verified_at": true},
	"app_sessions": {"absolute_expires_at": true},
}

var publicProfileColumns = map[string]map[string]bool{
	"driver_profiles": {"vehicle_type": true},
}

var rideColumns = map[string]map[string]bool{
	"rides": {"estimated_fare": true, "started_at": true, "cancelled_at": true, "offer_expires_at": true, "cancel_reason": true},
}

var supportColumns = map[string]map[string]bool{
	"ratings": {"ride_id": true, "hidden_at": true, "hidden_by": true},
}

func provider(db *sql.DB) (*goose.Provider, error) {
	if db == nil {
		return nil, ErrMigration
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, ErrMigration
	}
	return goose.NewProvider(goose.DialectPostgres, db, files,
		goose.WithDisableGlobalRegistry(true), goose.WithSessionLocker(locker),
		goose.WithSlog(slog.New(slog.NewTextHandler(io.Discard, nil))))
}

// Up adopts compatible existing tables, adds explicit storage columns, and
// applies frozen backfills under PostgreSQL advisory locking. The caller owns db.
func Up(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return ErrMigration
	}
	if err := validateSchema(ctx, db, true); err != nil {
		return err
	}
	p, err := provider(db)
	if err != nil {
		return ErrMigration
	}
	if _, err := p.Up(ctx); err != nil {
		return ErrMigration
	}
	return validateSchema(ctx, db, false)
}

// Version reports the latest recorded migration without creating a version table.
func Version(ctx context.Context, db *sql.DB) (int64, error) {
	if db == nil {
		return 0, ErrMigration
	}
	var present bool
	if err := db.QueryRowContext(ctx, "SELECT to_regclass('goose_db_version') IS NOT NULL").Scan(&present); err != nil {
		return 0, ErrMigration
	}
	if !present {
		return 0, nil
	}
	var version int64
	if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = true").Scan(&version); err != nil {
		return 0, ErrMigration
	}
	return version, nil
}

func baseline() (map[string][]columnSpec, error) {
	data, err := files.ReadFile("schema.json")
	if err != nil {
		return nil, ErrMigration
	}
	var schema map[string][]columnSpec
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, ErrMigration
	}
	return schema, nil
}

// Addon manifests validate later versions while the original baseline remains
// frozen. Additive columns of an existing table are appended to its baseline.
func latestSchema() (map[string][]columnSpec, error) {
	schema, err := baseline()
	if err != nil {
		return nil, err
	}
	entries, err := files.ReadDir(".")
	if err != nil {
		return nil, ErrMigration
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "schema_") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		body, err := files.ReadFile(entry.Name())
		if err != nil {
			return nil, ErrMigration
		}
		var addon map[string][]columnSpec
		if err := json.Unmarshal(body, &addon); err != nil {
			return nil, ErrMigration
		}
		for table, columns := range addon {
			schema[table] = append(schema[table], columns...)
		}
	}
	return schema, nil
}

func validateSchema(ctx context.Context, db *sql.DB, adopting bool) error {
	expected, err := latestSchema()
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT c.relname, a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull,
EXISTS (SELECT 1 FROM pg_constraint p WHERE p.conrelid = c.oid AND p.contype = 'p' AND a.attnum = ANY(p.conkey))
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace JOIN pg_attribute a ON a.attrelid = c.oid
WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p') AND a.attnum > 0 AND NOT a.attisdropped`)
	if err != nil {
		return ErrMigration
	}
	defer rows.Close()
	type actualColumn struct {
		kind             string
		notNull, primary bool
	}
	actual := map[string]map[string]actualColumn{}
	for rows.Next() {
		var table, name, kind string
		var notNull, primary bool
		if err := rows.Scan(&table, &name, &kind, &notNull, &primary); err != nil {
			return ErrMigration
		}
		if actual[table] == nil {
			actual[table] = map[string]actualColumn{}
		}
		actual[table][name] = actualColumn{kind, notNull, primary}
	}
	if rows.Err() != nil {
		return ErrMigration
	}
	for table, columns := range expected {
		found, exists := actual[table]
		if !exists && adopting {
			continue
		}
		if !exists {
			return ErrIncompatibleSchema
		}
		for _, column := range columns {
			current, exists := found[column.Name]
			if !exists && adopting && (storageColumns[table][column.Name] || appAuthColumns[table][column.Name] || publicProfileColumns[table][column.Name] || rideColumns[table][column.Name] || supportColumns[table][column.Name]) {
				continue
			}
			if !exists || normalizeType(current.kind) != normalizeType(column.Type) || column.NotNull && !current.notNull || column.Primary && !current.primary {
				return ErrIncompatibleSchema
			}
		}
	}
	return nil
}

func normalizeType(kind string) string {
	kind = strings.ToLower(kind)
	switch {
	case kind == "bigserial":
		return "bigint"
	case kind == "serial":
		return "integer"
	case kind == "smallserial":
		return "smallint"
	case kind == "timestamptz":
		return "timestamp with time zone"
	case kind == "decimal":
		return "numeric"
	case strings.HasPrefix(kind, "varchar("):
		return "character varying" + strings.TrimPrefix(kind, "varchar")
	}
	return kind
}
