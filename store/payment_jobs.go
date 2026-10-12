package store

import (
	"context"
	"errors"
	"gorm.io/gorm/clause"
	"ride-backend/models"
	"ride-backend/payments"
	"ride-backend/utils"
	"time"
)

func (s *Store) ProcessPayments() error {
	if s.PaymentProvider == nil {
		return OpError(503, "payment provider not configured")
	}
	if err := s.db.Model(&models.PaymentTransaction{}).Where("work_status = ? AND claimed_at < ? AND status IN ?", "processing", s.now().Add(-time.Minute), []string{"initiated", "pending"}).Update("work_status", "pending").Error; err != nil {
		return err
	}
	rows := []models.PaymentTransaction{}
	if err := s.db.Where("status IN ?", []string{"initiated", "pending"}).Order("created_at ASC,id ASC").Limit(100).Find(&rows).Error; err != nil {
		return err
	}
	for _, p := range rows {
		if p.WorkStatus == "pending" {
			r := s.db.Model(&models.PaymentTransaction{}).Where("id = ? AND work_status = ? AND status = ?", p.ID, "pending", "initiated").Updates(map[string]any{"work_status": "processing", "claimed_at": s.now()})
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected == 0 {
				continue
			}
			phone, err := utils.DecryptAESGCM(p.PhoneEncrypted, s.Policy.PaymentEncryptionKey)
			if err != nil {
				return err
			}
			result, err := s.PaymentProvider.InitiateCollection(context.Background(), payments.Request{Amount: p.Amount, Phone: phone, Method: p.Method, Reference: p.ID, IdempotencyKey: p.IdempotencyKey})
			if err != nil {
				if updateErr := s.db.Model(&p).Update("work_status", "pending").Error; updateErr != nil {
					return updateErr
				}
				if s.now().Before(p.ExpiresAt) {
					continue
				}
			} else {
				if err := s.db.Model(&p).Where("status = ?", "initiated").Updates(map[string]any{"provider_ref": result.Reference, "status": "pending", "work_status": "done", "phone_encrypted": ""}).Error; err != nil {
					return err
				}
				p.ProviderRef = result.Reference
				p.Status = "pending"
			}
		}
		if p.Status == "pending" && s.now().Sub(p.CreatedAt) >= time.Minute {
			ref := p.ProviderRef
			if ref == "" {
				ref = p.ID
			}
			result, err := s.PaymentProvider.Status(context.Background(), ref)
			if err == nil && result.Status != "pending" {
				if err := s.processPaymentStatus(p, result); err != nil {
					return err
				}
				continue
			}
		}
		if !s.now().Before(p.ExpiresAt) {
			if err := s.Transaction(func(tx *Store) error {
				r := tx.db.Model(&models.PaymentTransaction{}).Where("id = ? AND status IN ?", p.ID, []string{"initiated", "pending"}).Updates(map[string]any{"status": "expired", "work_status": "done", "phone_encrypted": ""})
				if r.Error != nil {
					return r.Error
				}
				if r.RowsAffected == 0 {
					return nil
				}
				if p.RideID != nil {
					if err := tx.ReleasePromo(*p.RideID); err != nil {
						return err
					}
				}
				return tx.enqueue(p.PayerID, "payment_failed", "", "")
			}); err != nil {
				return err
			}
		}
	}
	payouts := []models.DriverPayout{}
	if err := s.db.Where("status IN ?", []string{"approved", "processing"}).Order("created_at ASC,id ASC").Limit(100).Find(&payouts).Error; err != nil {
		return err
	}
	for _, p := range payouts {
		if p.Status == "approved" || (p.Status == "processing" && p.ProviderRef == "" && (p.ClaimedAt == nil || p.ClaimedAt.Before(s.now().Add(-time.Minute)))) {
			claimed := false
			err := s.Transaction(func(tx *Store) error {
				if err := tx.EvaluateFraud(context.Background(), models.FraudEvent{UserID: p.DriverID, Type: "payout_requested"}); err != nil {
					return err
				}
				if err := tx.checkPayoutFraud(p.DriverID); err != nil {
					var op *OperationError
					if errors.As(err, &op) && op.Status == 403 {
						return nil
					}
					return err
				}
				var dp models.DriverProfile
				if err := tx.db.First(&dp, "user_id = ?", p.DriverID).Error; err != nil {
					return err
				}
				if dp.VerificationStatus != models.Approved {
					return nil
				}
				var u models.User
				if err := tx.db.First(&u, "id = ?", p.DriverID).Error; err != nil {
					return err
				}
				if !u.IsActive || u.Status != "active" {
					return nil
				}
				r := tx.db.Model(&models.DriverPayout{}).Where("id = ? AND ((status = ?) OR (status = ? AND provider_ref = '' AND (claimed_at IS NULL OR claimed_at < ?)))", p.ID, "approved", "processing", s.now().Add(-time.Minute)).Updates(map[string]any{"status": "processing", "claimed_at": s.now()})
				claimed = r.RowsAffected == 1
				return r.Error
			})
			if err != nil {
				return err
			}
			if !claimed {
				continue
			}
			phone, err := utils.DecryptAESGCM(p.DestinationEncrypted, s.Policy.PaymentEncryptionKey)
			if err != nil {
				return err
			}
			result, err := s.PaymentProvider.Disburse(context.Background(), payments.Request{Amount: p.Amount, Phone: phone, Method: p.Method, Reference: p.ID, IdempotencyKey: p.IdempotencyKey})
			if err != nil {
				continue
			}
			if err := s.db.Model(&p).Update("provider_ref", result.Reference).Error; err != nil {
				return err
			}
			p.ProviderRef = result.Reference
			p.Status = "processing"
		}
		if p.Status == "processing" && p.ProviderRef != "" {
			result, err := s.PaymentProvider.Status(context.Background(), p.ProviderRef)
			if err == nil && result.Status != "pending" {
				provider := "mock"
				if s.Policy != nil && s.Policy.PaymentProviderName != "" {
					provider = s.Policy.PaymentProviderName
				}
				if err := s.ProcessPaymentEvent(provider, PaymentEvent{EventID: "payoutpoll:" + p.ID + ":" + result.Status, Type: "payout", Reference: p.ID, Status: result.Status, Amount: result.Amount}, tokenHash(p.ID+result.Status)); err != nil {
					return err
				}
			}
		}
	}
	return s.ProcessProviderRefunds()
}
func (s *Store) Reconcile(key string) (*models.ReconciliationRun, error) {
	run := models.ReconciliationRun{ID: newID(), Key: key, Status: "pending", CreatedAt: s.now()}
	r := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&run)
	if r.Error != nil {
		return nil, r.Error
	}
	if r.RowsAffected == 0 {
		run = models.ReconciliationRun{}
		if err := s.db.First(&run, "key = ?", key).Error; err != nil {
			return nil, err
		}
	}
	return &run, nil
}
func (s *Store) ProcessReconciliation() error {
	if err := s.db.Model(&models.ReconciliationRun{}).Where("status = ? AND claimed_at < ?", "processing", s.now().Add(-5*time.Minute)).Update("status", "pending").Error; err != nil {
		return err
	}
	runs := []models.ReconciliationRun{}
	if err := s.db.Where("status = ?", "pending").Order("created_at ASC,id ASC").Limit(10).Find(&runs).Error; err != nil {
		return err
	}
	for _, run := range runs {
		r := s.db.Model(&run).Where("status = ?", "pending").Updates(map[string]any{"status": "processing", "claimed_at": s.now()})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			continue
		}
		if err := s.reconcileRun(&run); err != nil {
			if updateErr := s.db.Model(&run).Update("status", "pending").Error; updateErr != nil {
				return updateErr
			}
			return err
		}
	}
	return nil
}
func (s *Store) reconcileRun(run *models.ReconciliationRun) error {
	recordsProvider, ok := s.PaymentProvider.(payments.RecordsProvider)
	if !ok {
		return OpError(503, "provider does not supply reconciliation records")
	}
	records, err := recordsProvider.Records(context.Background(), s.now().Add(-24*time.Hour), s.now())
	if err != nil {
		return err
	}
	remote := map[string]payments.Result{}
	for _, r := range records {
		remote[r.Reference] = r
	}
	return s.Transaction(func(tx *Store) error {
		if err := tx.db.Where("run_id = ?", run.ID).Delete(&models.ReconciliationException{}).Error; err != nil {
			return err
		}
		count := 0
		add := func(id, kind string) error {
			count++
			return tx.db.Create(&models.ReconciliationException{ID: newID(), RunID: &run.ID, TransactionID: id, Type: kind, Status: "open", CreatedAt: tx.now()}).Error
		}
		rows := []models.PaymentTransaction{}
		if err := tx.db.Where("created_at >= ? AND method IN ?", tx.now().Add(-24*time.Hour), []string{"mpesa", "tigopesa", "airtelmoney"}).Find(&rows).Error; err != nil {
			return err
		}
		for _, p := range rows {
			ref := p.ProviderRef
			if ref == "" {
				ref = p.ID
			}
			remoteRow, ok := remote[ref]
			if !ok {
				if err := add(p.ID, "missing_provider"); err != nil {
					return err
				}
			} else {
				delete(remote, ref)
				if remoteRow.Amount != p.Amount {
					if err := add(p.ID, "amount_mismatch"); err != nil {
						return err
					}
				}
				if remoteRow.Status == "succeeded" && p.Status != "succeeded" && p.Status != "refunded" && p.Status != "partially_refunded" {
					if err := add(p.ID, "status_mismatch"); err != nil {
						return err
					}
				}
			}
			if (p.Status == "pending" || p.Status == "initiated") && tx.now().Sub(p.CreatedAt) > 3*time.Minute {
				if err := add(p.ID, "stuck_pending"); err != nil {
					return err
				}
			}
		}
		for ref := range remote {
			var n int64
			if err := tx.db.Model(&models.DriverPayout{}).Where("CAST(id AS TEXT) = ? OR provider_ref = ?", ref, ref).Count(&n).Error; err != nil {
				return err
			}
			if n == 0 {
				if err := tx.db.Model(&models.Refund{}).Where("CAST(id AS TEXT) = ? OR provider_ref = ?", ref, ref).Count(&n).Error; err != nil {
					return err
				}
			}
			if n == 0 {
				if err := add(ref, "missing_local"); err != nil {
					return err
				}
			}
		}
		wallets := []models.Wallet{}
		if err := tx.db.Find(&wallets).Error; err != nil {
			return err
		}
		for _, w := range wallets {
			var total int64
			if err := tx.db.Model(&models.LedgerEntry{}).Where("wallet_id = ?", w.ID).Select("COALESCE(SUM(CASE WHEN direction = 'credit' THEN amount ELSE -amount END),0)").Scan(&total).Error; err != nil {
				return err
			}
			if total != w.Balance {
				if err := add(w.ID, "wallet_balance_mismatch"); err != nil {
					return err
				}
			}
		}
		return tx.db.Model(run).Updates(map[string]any{"status": "completed", "exceptions": count}).Error
	})
}
