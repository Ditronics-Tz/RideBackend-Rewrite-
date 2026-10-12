package store_test

import (
	"errors"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"ride-backend/db"
	"ride-backend/models"
	"ride-backend/store"
	"strings"
	"sync"
	"testing"
)

func TestPostgresConcurrentPromoReservations(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if os.Getenv("RUN_POSTGRES_PROMO_TESTS") != "1" || dsn == "" {
		t.Skip("Set RUN_POSTGRES_PROMO_TESTS=1 and POSTGRES_TEST_DSN for isolated-schema Postgres concurrency test; SQLite runs the SQL guard sequentially")
	}
	root, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	rootSQL, err := root.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer rootSQL.Close()
	schema := "promo_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := root.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	}()
	gdb, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(10)
	if err := db.AutoMigrate(gdb); err != nil {
		t.Fatal(err)
	}
	s := store.New(gdb)
	p := promoFixture()
	p.MaxRedemptions = int64ptr(5)
	if err := gdb.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	users := make([]*models.User, 30)
	for i := range users {
		users[i] = promoUser(t, s, models.RolePassenger)
	}
	start := make(chan struct{})
	results := make(chan error, len(users))
	var wg sync.WaitGroup
	for _, u := range users {
		wg.Add(1)
		go func(userID string) {
			defer wg.Done()
			<-start
			_, err := s.ReservePromo(userID, p.Code, 10000, uuid.NewString())
			results <- err
		}(u.ID)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		var invalid *store.PromoError
		if !errors.As(err, &invalid) || invalid.Message != "total limit reached" {
			t.Errorf("unexpected concurrent error: %v", err)
		}
	}
	if successes != 5 {
		t.Fatalf("exactly five must succeed, got %d", successes)
	}
	saved, err := s.GetPromo(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := gdb.Model(&models.PromoRedemption{}).Where("promo_id = ?", p.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if saved.RedeemedCount != 5 || count != 5 {
		t.Fatalf("counter %d and reservations %d must equal 5", saved.RedeemedCount, count)
	}
	if err := db.AutoMigrate(gdb); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}
