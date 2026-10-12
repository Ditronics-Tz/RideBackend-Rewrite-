package store

import (
	"gorm.io/gorm"
	"ride-backend/models"
)

func init() {
	RegisterDataExporter(func(s *Store) DataExporter { return financePrivacy{s} })
	RegisterAnonymizer(financePrivacy{})
	RegisterDeletionBlocker("financial_obligations", func(s *Store, id string) ([]string, error) {
		var count int64
		if err := s.db.Model(&models.Wallet{}).Where("user_id = ? AND (balance <> 0 OR held <> 0)", id).Count(&count).Error; err != nil {
			return nil, err
		}
		if count > 0 {
			return []string{"wallet balance or held funds require settlement"}, nil
		}
		if err := s.db.Model(&models.DriverPayout{}).Where("driver_id = ? AND status IN ?", id, []string{"requested", "approved", "processing"}).Count(&count).Error; err != nil {
			return nil, err
		}
		if count > 0 {
			return []string{"pending payout"}, nil
		}
		if err := s.db.Model(&models.PaymentTransaction{}).Where("payer_id = ? AND status IN ?", id, []string{"initiated", "pending"}).Count(&count).Error; err != nil {
			return nil, err
		}
		if count > 0 {
			return []string{"pending payment"}, nil
		}
		if err := s.db.Model(&models.Refund{}).Where("status = ? AND transaction_id IN (?)", "approved", s.db.Model(&models.PaymentTransaction{}).Select("id").Where("payer_id = ?", id)).Count(&count).Error; err != nil {
			return nil, err
		}
		if count > 0 {
			return []string{"pending refund"}, nil
		}
		return nil, nil
	})
}

type financePrivacy struct{ s *Store }

func (financePrivacy) Name() string { return "finance" }
func (financePrivacy) Tables() []string {
	return []string{"wallets", "ledger_entries", "payment_transactions", "commission_rules", "payment_settings", "driver_payouts", "refunds", "webhook_events", "reconciliation_runs", "reconciliation_exceptions"}
}
func (financePrivacy) Description() string {
	return "retains immutable anonymised money ledger and financial IDs pending legally approved retention; clears encrypted/masked phones and refund/payout/reconciliation free text; global financial settings have no user PII"
}
func (e financePrivacy) Export(id string) (any, []string, error) {
	wallets := []models.Wallet{}
	if err := e.s.db.Where("user_id = ?", id).Find(&wallets).Error; err != nil {
		return nil, nil, err
	}
	entries := []models.LedgerEntry{}
	if err := e.s.db.Where("wallet_id IN (?)", e.s.db.Model(&models.Wallet{}).Select("id").Where("user_id = ?", id)).Find(&entries).Error; err != nil {
		return nil, nil, err
	}
	payments := []models.PaymentTransaction{}
	if err := e.s.db.Where("payer_id = ? OR driver_id = ?", id, id).Find(&payments).Error; err != nil {
		return nil, nil, err
	}
	for i := range payments {
		payments[i].DriverID = nil
		payments[i].PayerID = ""
	}
	payouts := []models.DriverPayout{}
	if err := e.s.db.Where("driver_id = ?", id).Find(&payouts).Error; err != nil {
		return nil, nil, err
	}
	for i := range payouts {
		payouts[i].RejectReason = ""
		payouts[i].ApprovedBy = nil
	}
	return map[string]any{"wallet": wallets, "ledger": entries, "payments": payments, "payouts": payouts}, nil, nil
}
func (financePrivacy) Anonymize(tx *gorm.DB, id string) error {
	paymentIDs := tx.Model(&models.PaymentTransaction{}).Select("id").Where("payer_id = ? OR driver_id = ?", id, id)
	if err := tx.Model(&models.Refund{}).Where("transaction_id IN (?)", paymentIDs).Update("reason", "").Error; err != nil {
		return err
	}
	if err := tx.Model(&models.ReconciliationException{}).Where("transaction_id IN (?) OR transaction_id IN (?)", paymentIDs, tx.Model(&models.Wallet{}).Select("id").Where("user_id = ?", id)).Update("note", "").Error; err != nil {
		return err
	}
	if err := tx.Model(&models.PaymentTransaction{}).Where("payer_id = ? OR driver_id = ?", id, id).Updates(map[string]any{"phone_encrypted": "", "masked_phone": "", "request_hash": ""}).Error; err != nil {
		return err
	}
	return tx.Model(&models.DriverPayout{}).Where("driver_id = ?", id).Updates(map[string]any{"destination_encrypted": "", "masked_destination": "", "reject_reason": ""}).Error
}
