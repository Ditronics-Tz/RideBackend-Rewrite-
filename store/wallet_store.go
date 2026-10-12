package store

import (
	"gorm.io/gorm/clause"
	"ride-backend/config"
	"ride-backend/models"
	"ride-backend/payments"
	"strings"
)

const MaxMoney int64 = 1000000000000

func (s *Store) ConfigureOperations(cfg *config.Config) {
	s.Policy = cfg
	if s.PaymentProvider == nil {
		if cfg.PaymentProviderName == "gateway" {
			s.PaymentProvider = &payments.GatewayProvider{URL: cfg.PaymentGatewayURL, Key: cfg.PaymentGatewayKey, Secret: cfg.PaymentWebhookSecret, PreviousSecret: cfg.PaymentWebhookPreviousSecret}
		} else {
			s.PaymentProvider = &payments.MockProvider{Mode: "success", Secret: cfg.PaymentWebhookSecret, PreviousSecret: cfg.PaymentWebhookPreviousSecret}
		}
	}
}
func MoneyValid(amount int64) bool { return amount > 0 && amount <= MaxMoney }
func idempotency(user, key string) (string, error) {
	key = strings.TrimSpace(key)
	if len(key) < 8 || len(key) > 100 {
		return "", FieldError("Idempotency-Key", "Idempotency-Key must contain 8-100 characters")
	}
	return user + ":" + key, nil
}
func (s *Store) GetPaymentSettings() (*models.PaymentSettings, error) {
	var settings models.PaymentSettings
	err := s.db.Order("created_at DESC,id DESC").First(&settings).Error
	if err == nil {
		return &settings, nil
	}
	if !notFound(err) {
		return nil, err
	}
	settings = models.PaymentSettings{DriverMaxNegativeBalance: 10000, PayoutMin: 1000, PayoutAutoApproveMax: 10000, RefundSupportLimit: 5000}
	if s.Policy != nil {
		if s.Policy.DriverMaxNegativeBalance > 0 {
			settings.DriverMaxNegativeBalance = int64(s.Policy.DriverMaxNegativeBalance)
		}
		if s.Policy.PayoutMin > 0 {
			settings.PayoutMin = int64(s.Policy.PayoutMin)
		}
		if s.Policy.PayoutAutoApproveMax > 0 {
			settings.PayoutAutoApproveMax = int64(s.Policy.PayoutAutoApproveMax)
		}
		if s.Policy.RefundSupportLimit > 0 {
			settings.RefundSupportLimit = int64(s.Policy.RefundSupportLimit)
		}
	}
	return &settings, nil
}
func (s *Store) lockWallet(user string) (*models.Wallet, error) {
	w := models.Wallet{ID: newID(), UserID: user, Status: "active", CreatedAt: s.now()}
	if err := s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoNothing: true}).Create(&w).Error; err != nil {
		return nil, err
	}
	w = models.Wallet{}
	if err := s.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&w, "user_id = ?", user).Error; err != nil {
		return nil, err
	}
	if w.Status != "active" {
		return nil, OpError(409, "wallet unavailable")
	}
	return &w, nil
}
func (s *Store) GetWallet(user string) (*models.Wallet, error) {
	var result *models.Wallet
	err := s.Transaction(func(tx *Store) error { var err error; result, err = tx.lockWallet(user); return err })
	return result, err
}
func (s *Store) postWallet(user, account, direction string, amount, floor int64, refType, ref, key string) error {
	if amount == 0 {
		return nil
	}
	if !MoneyValid(amount) {
		return FieldError("amount", "invalid money amount")
	}
	w, err := s.lockWallet(user)
	if err != nil {
		return err
	}
	delta := amount
	if direction == "debit" {
		delta = -amount
	}
	after := w.Balance + delta
	if after < floor || after > MaxMoney || after-w.Held < floor {
		return OpError(409, "insufficient available wallet balance")
	}
	r := s.db.Model(&models.Wallet{}).Where("id = ? AND version = ?", w.ID, w.Version).Updates(map[string]any{"balance": after, "version": w.Version + 1})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return OpError(409, "wallet changed; retry with the same idempotency key")
	}
	return s.db.Create(&models.LedgerEntry{ID: newID(), WalletID: &w.ID, Account: account, Direction: direction, Amount: amount, BalanceAfter: after, RefType: refType, RefID: ref, IdempotencyKey: key, Memo: refType, CreatedAt: s.now()}).Error
}
func (s *Store) postSystem(account, direction string, amount int64, refType, ref, key string) error {
	if amount == 0 {
		return nil
	}
	if !MoneyValid(amount) {
		return FieldError("amount", "invalid money amount")
	}
	return s.db.Create(&models.LedgerEntry{ID: newID(), Account: account, Direction: direction, Amount: amount, RefType: refType, RefID: ref, IdempotencyKey: key, Memo: refType, CreatedAt: s.now()}).Error
}
func Commission(fare int64, percent int, minFee int64) (int64, error) {
	if !MoneyValid(fare) || percent < 0 || percent > 100 || minFee < 0 || minFee > MaxMoney {
		return 0, FieldError("commission", "invalid commission rule")
	}
	amount := (fare/100)*int64(percent) + (fare%100*int64(percent)+50)/100
	if amount < minFee {
		amount = minFee
	}
	if amount > fare {
		amount = fare
	}
	return amount, nil
}
func (s *Store) rideCommission(r *models.Ride) (int64, error) {
	var rule models.CommissionRule
	err := s.db.Where("effective_from <= ? AND (scope = ? OR (scope = ? AND vehicle_type = ?))", s.now(), "default", "vehicle_type", r.VehicleType).Order("scope DESC,effective_from DESC,created_at DESC,id DESC").First(&rule).Error
	if notFound(err) {
		return Commission(r.Fare, 15, 0)
	}
	if err != nil {
		return 0, err
	}
	return Commission(r.Fare, rule.Percent, rule.MinFee)
}
func (s *Store) AdjustWallet(user, actor, key, reason string, delta int64) error {
	if err := cleanNote(reason, 2000); err != nil {
		return err
	}
	if delta == 0 || delta > MaxMoney || delta < -MaxMoney {
		return FieldError("amount", "adjustment must be a nonzero whole TZS amount")
	}
	k, err := idempotency(actor, key)
	if err != nil {
		return err
	}
	return s.Transaction(func(tx *Store) error {
		var e models.LedgerEntry
		err := tx.db.First(&e, "idempotency_key = ?", k+":wallet").Error
		if err == nil {
			if e.RefID != user || e.Amount != absMoney(delta) || (e.Direction == "credit") != (delta > 0) {
				return OpError(409, "idempotency key reused with different adjustment")
			}
			return nil
		}
		if !notFound(err) {
			return err
		}
		var u models.User
		if err := tx.db.First(&u, "id = ?", user).Error; err != nil {
			return err
		}
		before, err := tx.lockWallet(user)
		if err != nil {
			return err
		}
		floor := int64(0)
		account := "passenger_wallet"
		if u.Role == models.RoleDriver {
			floor = -MaxMoney
			account = "driver_wallet"
		}
		dir, offset := "credit", "debit"
		if delta < 0 {
			dir, offset = "debit", "credit"
		}
		if err := tx.postWallet(user, account, dir, absMoney(delta), floor, "adjustment", user, k+":wallet"); err != nil {
			return err
		}
		if err := tx.postSystem("platform_revenue", offset, absMoney(delta), "adjustment", user, k+":offset"); err != nil {
			return err
		}
		return tx.CreateAuditLog(actor, "wallet_adjusted", "user", user, "", map[string]any{"severity": "high", "before": before.Balance, "after": before.Balance + delta, "amount": delta})
	})
}
func absMoney(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
func (s *Store) checkFinancialEligibility(driver string) error {
	settings, err := s.GetPaymentSettings()
	if err != nil {
		return err
	}
	var w models.Wallet
	err = s.db.First(&w, "user_id = ?", driver).Error
	if notFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if w.Balance-w.Held < -settings.DriverMaxNegativeBalance {
		return OpError(403, "cash limit reached")
	}
	return nil
}
func (s *Store) UpdatePaymentSettings(actor string, input models.PaymentSettings, rules []models.CommissionRule) error {
	for _, n := range []int64{input.DriverMaxNegativeBalance, input.PayoutMin, input.PayoutAutoApproveMax, input.RefundSupportLimit} {
		if !MoneyValid(n) {
			return FieldError("settings", "thresholds must be positive whole TZS within the supported range")
		}
	}
	return s.Transaction(func(tx *Store) error {
		before, err := tx.GetPaymentSettings()
		if err != nil {
			return err
		}
		input.ID = newID()
		input.UpdatedBy = actor
		input.CreatedAt = tx.now()
		if err := tx.db.Create(&input).Error; err != nil {
			return err
		}
		for _, r := range rules {
			if r.Scope != "default" && r.Scope != "vehicle_type" {
				return FieldError("scope", "invalid commission scope")
			}
			if r.Scope == "vehicle_type" && strings.TrimSpace(r.VehicleType) == "" {
				return FieldError("vehicle_type", "vehicle type required")
			}
			if _, err := Commission(100, r.Percent, r.MinFee); err != nil {
				return err
			}
			if r.EffectiveFrom.IsZero() {
				r.EffectiveFrom = tx.now()
			}
			r.ID = newID()
			r.CreatedBy = actor
			r.CreatedAt = tx.now()
			if err := tx.db.Create(&r).Error; err != nil {
				return err
			}
		}
		return tx.CreateAuditLog(actor, "update_payment_settings", "settings", input.ID, "", map[string]any{"before": map[string]int64{"payout_min": before.PayoutMin, "refund_support_limit": before.RefundSupportLimit}, "after": map[string]int64{"payout_min": input.PayoutMin, "refund_support_limit": input.RefundSupportLimit}})
	})
}
