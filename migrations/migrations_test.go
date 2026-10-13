package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func baselineStatements(t *testing.T) []string {
	t.Helper()
	content, err := files.ReadFile("00001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Split(string(content), "-- +goose Down")[0]
	lines := []string{}
	for _, line := range strings.Split(up, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			lines = append(lines, line)
		}
	}
	statements := []string{}
	for _, statement := range strings.Split(strings.Join(lines, "\n"), ";") {
		if strings.TrimSpace(statement) != "" {
			statements = append(statements, statement)
		}
	}
	return statements
}

func TestFrozenBaselineDefinesAllRecordedColumnsAndCanAdoptTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	statements := baselineStatements(t)
	for attempt := 0; attempt < 2; attempt++ {
		for _, statement := range statements {
			if err := db.Exec(statement).Error; err != nil {
				t.Fatalf("baseline statement cannot be applied/adopted: %v", err)
			}
		}
	}
	schema, err := baseline()
	if err != nil {
		t.Fatal(err)
	}
	if len(schema) != 56 {
		t.Fatalf("frozen table inventory unexpectedly changed: %d", len(schema))
	}
	for table, expected := range schema {
		var rows []struct{ Name string }
		if err := db.Raw("PRAGMA table_info(\"" + table + "\")").Scan(&rows).Error; err != nil {
			t.Fatal(err)
		}
		columns := map[string]bool{}
		for _, row := range rows {
			columns[row.Name] = true
		}
		for _, column := range expected {
			if !columns[column.Name] {
				t.Errorf("missing frozen column %s.%s", table, column.Name)
			}
		}
	}
}

func TestMigrationSourcesAreVersionedAndRollbackRefusesDataLoss(t *testing.T) {
	p, err := goose.NewProvider(goose.DialectPostgres, &sql.DB{}, files, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	sources := p.ListSources()
	if len(sources) != int(LatestVersion) {
		t.Fatalf("source versions: %d", len(sources))
	}
	for _, source := range sources {
		content, err := files.ReadFile(source.Path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "-- +goose Down") || !strings.Contains(string(content), "RAISE EXCEPTION") {
			t.Fatal("unsafe rollback definition")
		}
		if strings.Contains(string(content), "DROP TABLE") || strings.Contains(string(content), "DROP COLUMN") {
			t.Fatal("destructive migration operation")
		}
	}
}

func TestMigrationRejectsMissingDatabase(t *testing.T) {
	if err := Up(context.Background(), nil); !errors.Is(err, ErrMigration) {
		t.Fatal(err)
	}
	if _, err := Version(context.Background(), nil); !errors.Is(err, ErrMigration) {
		t.Fatal(err)
	}
}

func TestPostgresTypeAliases(t *testing.T) {
	for _, pair := range [][2]string{{"bigserial", "bigint"}, {"serial", "integer"}, {"timestamptz", "timestamp with time zone"}, {"varchar(20)", "character varying(20)"}, {"decimal", "numeric"}} {
		if normalizeType(pair[0]) != normalizeType(pair[1]) {
			t.Fatalf("mismatched baseline type %s", pair[0])
		}
	}
}

func postgresTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if os.Getenv("RUN_POSTGRES_MIGRATION_TESTS") != "1" || dsn == "" {
		t.Skip("Set RUN_POSTGRES_MIGRATION_TESTS=1 and POSTGRES_TEST_DSN for isolated-schema migration tests")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test PostgreSQL configuration")
	}
	admin := stdlib.OpenDB(*config)
	schema := "migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		admin.Close()
		t.Fatal("could not create isolated migration test schema")
	}
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { db.Close(); admin.Exec("DROP SCHEMA " + schema + " CASCADE"); admin.Close() })
	return db
}

func TestPostgresFreshMigrationAndRestart(t *testing.T) {
	db := postgresTestDB(t)
	ctx := context.Background()
	version, err := Version(ctx, db)
	if err != nil || version != 0 {
		t.Fatal("fresh status failed")
	}
	if err := Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := Up(ctx, db); err != nil {
		t.Fatal("migration restart failed")
	}
	version, err = Version(ctx, db)
	if err != nil || version != LatestVersion {
		t.Fatal("latest version not recorded")
	}
}

func TestPostgresAdoptsExistingSchemaAndBackfillsLegacyData(t *testing.T) {
	db := postgresTestDB(t)
	ctx := context.Background()
	for _, statement := range baselineStatements(t) {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal("baseline test fixture failed")
		}
	}
	for table, columns := range storageColumns {
		for name := range columns {
			if _, err := db.Exec(fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", table, name)); err != nil {
				t.Fatal("legacy storage fixture failed")
			}
		}
	}
	driverID, passengerID := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO users (id,name,email,is_active,status) VALUES ($1,'Legacy driver','driver@test.invalid',true,'active'),($2,'Legacy passenger','passenger@test.invalid',false,'active')`, driverID, passengerID); err != nil {
		t.Fatal("legacy users fixture failed")
	}
	if _, err := db.Exec(`INSERT INTO driver_profiles (user_id,licence_number,plate_number,licence_image_url) VALUES ($1,' AB-12 ',' ZZ-99 ','/api/v1/files/licences/legacy.jpg')`, driverID); err != nil {
		t.Fatal("legacy driver fixture failed")
	}
	if _, err := db.Exec(`INSERT INTO passenger_profiles (user_id,phone) VALUES ($1,'0712 345-678')`, passengerID); err != nil {
		t.Fatal("legacy passenger fixture failed")
	}
	if err := Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	var phone, status string
	if err := db.QueryRow("SELECT phone,status FROM users WHERE id=$1", passengerID).Scan(&phone, &status); err != nil || phone != "+255712345678" || status != "suspended" {
		t.Fatal("legacy account backfill failed")
	}
	var licence string
	if err := db.QueryRow("SELECT licence_number FROM driver_profiles WHERE user_id=$1", driverID).Scan(&licence); err != nil || licence != "AB12" {
		t.Fatal("legacy licence normalization failed")
	}
	var documents, reviews int
	if err := db.QueryRow("SELECT COUNT(*) FROM driver_documents WHERE driver_id=$1", driverID).Scan(&documents); err != nil || documents != 1 {
		t.Fatal("legacy licence import failed")
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM verification_reviews WHERE driver_id=$1", driverID).Scan(&reviews); err != nil || reviews != 1 {
		t.Fatal("legacy review history import failed")
	}
	if err := Up(ctx, db); err != nil {
		t.Fatal("adoption restart failed")
	}
}

func TestPostgresRejectsIncompatibleExistingSchemaBeforeVersioning(t *testing.T) {
	db := postgresTestDB(t)
	if _, err := db.Exec("CREATE TABLE users (id text PRIMARY KEY)"); err != nil {
		t.Fatal("incompatible fixture failed")
	}
	if err := Up(context.Background(), db); !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatal("incompatible schema was accepted")
	}
	version, err := Version(context.Background(), db)
	if err != nil || version != 0 {
		t.Fatal("incompatible schema was marked migrated")
	}
}
