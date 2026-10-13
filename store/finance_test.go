package store_test

import (
	"context"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"math/rand"
	"os"
	"ride-backend/config"
	backenddb "ride-backend/db"
	"ride-backend/models"
	"ride-backend/payments"
	"ride-backend/store"
	"ride-backend/testutil"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFinanceLedgerIdempotencyRefundAndPayoutGuard(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg.PaymentProviderName = "mock"
	cfg.PaymentEncryptionKey = []byte("12345678901234567890123456789012")
	s.ConfigureOperations(cfg)
	now := time.Now().UTC()
	s.Clock = func() time.Time { return now }
	a, err := s.CreateUser("Passenger", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateUser("Driver", uuid.NewString()+"@test.invalid", "", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	admin := &models.StaffUser{ID: uuid.NewString(), Role: models.StaffRoleAdmin}
	if err := s.DB().Model(&models.DriverProfile{}).Where("user_id = ?", b.ID).Update("verification_status", models.Approved).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		delta := int64(rand.New(rand.NewSource(int64(i))).Intn(100) + 100)
		key := uuid.NewString()
		if err := s.AdjustWallet(a.ID, admin.ID, key, "opening correction", delta); err != nil {
			t.Fatal(err)
		}
		if err := s.AdjustWallet(a.ID, admin.ID, key, "retry", delta); err != nil {
			t.Fatal(err)
		}
	}
	wallet, err := s.GetWallet(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	if err := s.DB().Model(&models.LedgerEntry{}).Where("wallet_id = ?", wallet.ID).Select("SUM(CASE WHEN direction='credit' THEN amount ELSE -amount END)").Scan(&sum).Error; err != nil {
		t.Fatal(err)
	}
	if wallet.Balance != sum {
		t.Fatal("ledger balance mismatch")
	}
	var entry models.LedgerEntry
	if err := s.DB().First(&entry).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.DB().Model(&entry).Update("amount", 1).Error; err == nil {
		t.Fatal("ledger mutable")
	}
	if err := s.DB().Delete(&entry).Error; err == nil {
		t.Fatal("ledger deletable")
	}
	ride := models.Ride{ID: uuid.NewString(), PassengerID: a.ID, DriverID: b.ID, Status: "completed", Fare: 3000, CompletedAt: &now}
	if err := s.DB().Create(&ride).Error; err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	p, err := s.InitiatePayment(a.ID, ride.ID, key, store.PayInput{Method: "cash", Amount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if p.Amount != 3000 || p.Status != "succeeded" {
		t.Fatal("client amount trusted or not settled", p)
	}
	again, err := s.InitiatePayment(a.ID, ride.ID, key, store.PayInput{Method: "cash", Amount: 1})
	if err != nil || p.ID != again.ID {
		t.Fatal("not idempotent", err)
	}
	if _, err := s.RequestRefund(p.ID, uuid.NewString(), "refund", 3001, admin); err == nil {
		t.Fatal("over refund accepted")
	}
	refund, err := s.RequestRefund(p.ID, uuid.NewString(), "refund", 1500, admin)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WorkRefund(refund.ID, "approve", "", admin); err != nil {
		t.Fatal(err)
	}
	if err := s.WorkRefund(refund.ID, "approve", "", admin); err != nil {
		t.Fatal(err)
	}
	if err := s.AdjustWallet(b.ID, admin.ID, uuid.NewString(), "seed earned balance", 25000); err != nil {
		t.Fatal(err)
	}
	success := 0
	for i := 0; i < 10; i++ {
		if _, err := s.RequestPayout(b.ID, uuid.NewString(), "mpesa", "+255712345678", 5000); err == nil {
			success++
		}
	}
	if success != 2 {
		t.Fatalf("sequential payout SQL guard: expected 2 got %d", success)
	}
}

func TestPostgresConcurrentPayoutRequestsCannotOverspend(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if os.Getenv("RUN_POSTGRES_FINANCE_TESTS") != "1" || dsn == "" {
		t.Skip("Set RUN_POSTGRES_FINANCE_TESTS=1 and POSTGRES_TEST_DSN for isolated-schema payout concurrency test; SQLite exercises the guard sequentially")
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
	schema := "finance_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	sqlDB.SetMaxOpenConns(12)
	if err := backenddb.AutoMigrate(gdb); err != nil {
		t.Fatal(err)
	}
	s := store.New(gdb)
	cfg := &config.Config{PaymentEncryptionKey: []byte("12345678901234567890123456789012"), PaymentProviderName: "mock"}
	s.ConfigureOperations(cfg)
	driver, err := s.CreateUser("Driver", uuid.NewString()+"@test.invalid", "", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&models.DriverProfile{}).Where("user_id = ?", driver.ID).Update("verification_status", models.Approved).Error; err != nil {
		t.Fatal(err)
	}
	adminID := uuid.NewString()
	if err := s.AdjustWallet(driver.ID, adminID, uuid.NewString(), "seed balance", 20000); err != nil {
		t.Fatal(err)
	}
	settings := models.PaymentSettings{ID: uuid.NewString(), DriverMaxNegativeBalance: 0, PayoutMin: 1000, PayoutAutoApproveMax: 1000, RefundSupportLimit: 5000, UpdatedBy: adminID, CreatedAt: time.Now().UTC()}
	if err := gdb.Create(&settings).Error; err != nil {
		t.Fatal(err)
	}
	const workers = 20
	start := make(chan struct{})
	results := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, e := s.RequestPayout(driver.ID, uuid.NewString(), "mpesa", "+255712345678", 5000)
			results <- e
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if op, ok := err.(*store.OperationError); !ok || op.Status != 409 {
			t.Errorf("unexpected payout request error: %v", err)
		}
	}
	if accepted != 4 {
		t.Fatalf("exactly four 5000 TZS payouts should fit a 20000 TZS balance, got %d", accepted)
	}
	wallet, err := s.GetWallet(driver.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Held != 20000 || wallet.Balance-wallet.Held != 0 {
		t.Fatalf("payout holds overspent wallet: balance=%d held=%d", wallet.Balance, wallet.Held)
	}
}
func TestCommissionRounding(t *testing.T) {
	for _, fare := range []int64{1, 3, 9, 99, 101, 99999, store.MaxMoney} {
		for _, percent := range []int{0, 1, 15, 50, 100} {
			commission, err := store.Commission(fare, percent, 0)
			if err != nil {
				t.Fatal(err)
			}
			if commission < 0 || commission > fare || commission+(fare-commission) != fare {
				t.Fatal("commission invariant")
			}
		}
	}
}
func TestRidePaymentsAreCashOnlyAndTopupsAreUnavailable(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	passenger, err := s.CreateUser("P", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := s.CreateUser("D", uuid.NewString()+"@test.invalid", "", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ride := models.Ride{ID: uuid.NewString(), PassengerID: passenger.ID, DriverID: driver.ID, Status: "completed", Fare: 5000, CompletedAt: &now}
	if err := s.DB().Create(&ride).Error; err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"wallet", "mpesa", "tigopesa", "airtelmoney"} {
		t.Run(method, func(t *testing.T) {
			if _, err := s.InitiatePayment(passenger.ID, ride.ID, uuid.NewString(), store.PayInput{Method: method}); err == nil {
				t.Fatalf("unsupported ride payment method %q was accepted", method)
			}
		})
	}
	if _, err := s.InitiatePayment(passenger.ID, "", uuid.NewString(), store.PayInput{Method: "mpesa", Amount: 5000, Phone: "+255712345678"}); err == nil {
		t.Fatal("wallet top-up was accepted without an integrated local provider")
	}
}

type reconciliationProvider struct {
	*payments.MockProvider
	records []payments.Result
}

func (p reconciliationProvider) Records(context.Context, time.Time, time.Time) ([]payments.Result, error) {
	return p.records, nil
}
func TestReconciliationExceptionKindsAndIdempotency(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureOperations(cfg)
	now := time.Now().UTC()
	s.Clock = func() time.Time { return now }
	u, err := s.CreateUser("Reconcile", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	remote := []payments.Result{{Reference: uuid.NewString(), Amount: 100, Status: "succeeded"}}
	for _, kind := range []string{"missing_provider", "amount_mismatch", "status_mismatch", "stuck_pending"} {
		p := models.PaymentTransaction{ID: uuid.NewString(), PayerID: u.ID, Method: "mpesa", Status: "succeeded", Amount: 5000, IdempotencyKey: uuid.NewString(), CreatedAt: now.Add(-5 * time.Minute)}
		if kind == "stuck_pending" || kind == "status_mismatch" {
			p.Status = "pending"
		}
		if err := s.DB().Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		if kind != "missing_provider" {
			amount := p.Amount
			status := p.Status
			if kind == "amount_mismatch" {
				amount++
			}
			if kind == "status_mismatch" {
				status = "succeeded"
			}
			remote = append(remote, payments.Result{Reference: p.ID, Status: status, Amount: amount})
		}
	}
	w, err := s.GetWallet(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DB().Model(w).Update("balance", 100).Error; err != nil {
		t.Fatal(err)
	}
	s.PaymentProvider = reconciliationProvider{&payments.MockProvider{}, remote}
	run, err := s.Reconcile("daily-test")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.ProcessReconciliation(); err != nil {
			t.Fatal(err)
		}
	}
	again, err := s.Reconcile("daily-test")
	if err != nil || again.ID != run.ID {
		t.Fatal("duplicate run", err)
	}
	var rows []models.ReconciliationException
	if err := s.DB().Where("run_id = ?", run.ID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Type] = true
	}
	for _, kind := range []string{"missing_provider", "missing_local", "amount_mismatch", "status_mismatch", "stuck_pending", "wallet_balance_mismatch"} {
		if !seen[kind] {
			t.Error("missing exception", kind)
		}
	}
}
func TestWalletTopupsUnavailableUntilLocalProviderIntegration(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.CreateUser("Passenger", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.InitiatePayment(user.ID, "", uuid.NewString(), store.PayInput{Amount: 5000, Method: "mpesa", Phone: "+255712345678"}); err == nil {
		t.Fatal("wallet top-up was accepted without an integrated local provider")
	}
}
func TestCashLimitAndSupportRefundCap(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureOperations(cfg)
	driver, err := s.CreateUser("Driver", uuid.NewString()+"@test.invalid", "", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	passenger, err := s.CreateUser("Passenger", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	admin := &models.StaffUser{ID: uuid.NewString(), Role: models.StaffRoleAdmin}
	support := &models.StaffUser{ID: uuid.NewString(), Role: models.StaffRoleSupport}
	now := time.Now().UTC()
	ride := models.Ride{ID: uuid.NewString(), DriverID: driver.ID, PassengerID: passenger.ID, Status: "completed", Fare: 100000, CreatedAt: now}
	if err := s.DB().Create(&ride).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.InitiatePayment(passenger.ID, ride.ID, uuid.NewString(), store.PayInput{Method: "cash"}); err == nil {
		t.Fatal("cash commission exceeded floor")
	}
	if err := s.AdjustWallet(driver.ID, admin.ID, uuid.NewString(), "Seed driver balance", 100000); err != nil {
		t.Fatal(err)
	}
	p, err := s.InitiatePayment(passenger.ID, ride.ID, uuid.NewString(), store.PayInput{Method: "cash"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.RequestRefund(p.ID, uuid.NewString(), "Complaint", 6000, support)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WorkRefund(r.ID, "approve", "", support); err == nil {
		t.Fatal("support approved over limit")
	}
	if err := s.WorkRefund(r.ID, "approve", "", admin); err != nil {
		t.Fatal(err)
	}
	if err := s.AdjustWallet(driver.ID, admin.ID, uuid.NewString(), "Balance correction", -100000); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDriverOnline(driver.ID, true); err == nil {
		t.Fatal("cash cutoff not enforced online")
	}
}
