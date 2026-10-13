package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"ride-backend/migrations"
	"ride-backend/models"
)

func postgresAuthStore(t *testing.T) (*Store, *models.User) {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if os.Getenv("RUN_POSTGRES_AUTH_TESTS") != "1" || dsn == "" {
		t.Skip("Set RUN_POSTGRES_AUTH_TESTS=1 and POSTGRES_TEST_DSN for isolated-schema authentication concurrency tests")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test configuration")
	}
	admin := stdlib.OpenDB(*config)
	schema := "auth_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		admin.Close()
		t.Fatal("isolated authentication schema unavailable")
	}
	config.RuntimeParams["search_path"] = schema
	connection := stdlib.OpenDB(*config)
	t.Cleanup(func() { connection.Close(); admin.Exec("DROP SCHEMA " + schema + " CASCADE"); admin.Close() })
	if err := migrations.Up(context.Background(), connection); err != nil {
		t.Fatal("isolated authentication migrations failed")
	}
	database, err := gorm.Open(postgres.New(postgres.Config{Conn: connection}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("test connection unavailable")
	}
	s := New(database)
	user := &models.User{ID: uuid.NewString(), Name: "Concurrency fixture", Email: uuid.NewString() + "@example.test", Role: models.RolePassenger, IsActive: true, Status: "active"}
	if err := database.Create(user).Error; err != nil {
		t.Fatal("test user unavailable")
	}
	return s, user
}

func TestAppSessionPostgresConcurrentRefreshDetectsReuse(t *testing.T) {
	s, user := postgresAuthStore(t)
	issued := issueForTest(t, s, user)
	start := make(chan struct{})
	outcomes := make(chan error, 2)
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := s.RotateAppSession(context.Background(), issued.RefreshToken, SessionDevice{})
			outcomes <- err
		}()
	}
	close(start)
	workers.Wait()
	close(outcomes)
	success, reuse := 0, 0
	for err := range outcomes {
		if err == nil {
			success++
		} else if errors.Is(err, ErrInvalidCredentials) {
			reuse++
		} else {
			t.Fatal("unexpected PostgreSQL refresh failure")
		}
	}
	if success != 1 || reuse != 1 {
		t.Fatal("PostgreSQL refresh race created multiple replacements")
	}
	_, active, err := s.ListAppSessions(context.Background(), user.ID, Page{})
	if err != nil || active != 0 {
		t.Fatal("PostgreSQL reuse family revocation failed")
	}
}

func TestAppLimiterPostgresAtomicLimitAcrossInstances(t *testing.T) {
	s, _ := postgresAuthStore(t)
	rate := AuthRate{Limit: 5, Window: time.Minute}
	results := make(chan AuthRateDecision, 20)
	failures := make(chan error, 20)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			decision, err := NewSQLAuthLimiter(s).Allow(context.Background(), "app_login_ip", "192.0.2.55", rate)
			if err != nil {
				failures <- err
				return
			}
			results <- decision
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(failures)
	for range failures {
		t.Fatal("concurrent PostgreSQL limiter operation failed")
	}
	allowed := 0
	for decision := range results {
		if decision.Allowed {
			allowed++
		} else if decision.RetryAfter < 1 {
			t.Fatal("rejected limiter response lacks retry delay")
		}
	}
	if allowed != 5 {
		t.Fatal("persistent limiter exceeded configured concurrent limit")
	}
}

func TestAppSessionPostgresIssuanceWaitsForAccountTransition(t *testing.T) {
	s, user := postgresAuthStore(t)
	transaction, err := s.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := transaction.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		t.Fatal("test transition unavailable")
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", user.ID); err != nil {
		t.Fatal("user lock unavailable")
	}
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() { close(started); _, err := s.IssueAppSession(ctx, user.ID, SessionDevice{}); result <- err }()
	<-started
	if _, err := tx.ExecContext(ctx, "UPDATE users SET is_active=false,status='suspended' WHERE id=$1", user.ID); err != nil {
		t.Fatal("account transition unavailable")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("account transition commit failed")
	}
	if err := <-result; !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("concurrent issuance ignored suspension")
	}
}
