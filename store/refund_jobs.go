package store

import (
	"context"
	"gorm.io/gorm/clause"
	"ride-backend/models"
	"ride-backend/payments"
	"time"
)

func (s *Store) ProcessProviderRefunds() error {
	if err := s.db.Model(&models.Refund{}).Where("status = ? AND work_status = ? AND claimed_at < ?", "approved", "processing", s.now().Add(-time.Minute)).Update("work_status", "pending").Error; err != nil {
		return err
	}
	rows := []models.Refund{}
	if err := s.db.Where("status = ? AND destination = ?", "approved", "mobile_money").Order("created_at ASC,id ASC").Limit(100).Find(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		if r.ProviderRef == "" {
			provider, ok := s.PaymentProvider.(payments.IdempotentRefundProvider)
			if !ok {
				return OpError(503, "provider requires an idempotent refund adapter")
			}
			claim := s.db.Model(&models.Refund{}).Where("id = ? AND status = ? AND work_status = ?", r.ID, "approved", "pending").Updates(map[string]any{"work_status": "processing", "claimed_at": s.now()})
			if claim.Error != nil {
				return claim.Error
			}
			if claim.RowsAffected == 0 {
				continue
			}
			var p models.PaymentTransaction
			if err := s.db.First(&p, "id = ?", r.TransactionID).Error; err != nil {
				return err
			}
			ref := p.ProviderRef
			if ref == "" {
				ref = p.ID
			}
			result, err := provider.RefundWithKey(context.Background(), ref, r.Amount, r.ID)
			if err != nil {
				if updateErr := s.db.Model(&r).Update("work_status", "pending").Error; updateErr != nil {
					return updateErr
				}
				continue
			}
			if result.Reference == "" {
				return OpError(502, "provider refund reference is missing")
			}
			if err := s.db.Model(&r).Updates(map[string]any{"provider_ref": result.Reference, "work_status": "done"}).Error; err != nil {
				return err
			}
			r.ProviderRef = result.Reference
			if result.Status != "pending" {
				if err := s.ProcessPaymentEvent(p.Provider, PaymentEvent{EventID: "refund-init:" + r.ID + ":" + result.Status, Type: "refund", Reference: r.ID, Status: result.Status, Amount: result.Amount}, tokenHash(r.ID+result.Status)); err != nil {
					return err
				}
				continue
			}
		}
		if r.ProviderRef != "" {
			result, err := s.PaymentProvider.Status(context.Background(), r.ProviderRef)
			if err != nil {
				continue
			}
			if result.Status != "pending" {
				if err := s.ProcessPaymentEvent("refund-poll", PaymentEvent{EventID: r.ID + ":" + result.Status, Type: "refund", Reference: r.ID, Status: result.Status, Amount: result.Amount}, tokenHash(r.ID+result.Status)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (s *Store) completeProviderRefund(event PaymentEvent) error {
	var r models.Refund
	if err := s.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("CAST(id AS TEXT) = ? OR provider_ref = ?", event.Reference, event.Reference).First(&r).Error; err != nil {
		if notFound(err) {
			return s.financeException("", "unknown_refund", "")
		}
		return err
	}
	if r.Amount != event.Amount {
		return s.financeException(r.TransactionID, "refund_amount_mismatch", "")
	}
	if r.Status == "processed" || r.Status == "rejected" {
		return nil
	}
	if r.Status != "approved" || r.Destination != "mobile_money" {
		return s.financeException(r.TransactionID, "unexpected_refund_status", "")
	}
	if event.Status == "pending" {
		return nil
	}
	var p models.PaymentTransaction
	if err := s.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, "id = ?", r.TransactionID).Error; err != nil {
		return err
	}
	prefix := "refund:" + r.ID
	if err := s.postSystem("refunds", "debit", r.Amount, "refund", r.ID, prefix+":release"); err != nil {
		return err
	}
	if event.Status == "failed" {
		if err := s.postWallet(p.PayerID, "passenger_wallet", "credit", r.Amount, 0, "refund", r.ID, prefix+":restore"); err != nil {
			return err
		}
		return s.db.Model(&r).Updates(map[string]any{"status": "rejected", "failure_code": "provider_failed", "work_status": "done"}).Error
	}
	if r.Amount > p.Amount-p.RefundedAmount {
		return OpError(409, "refund exceeds remaining paid amount")
	}
	if err := s.postSystem("provider_clearing", "credit", r.Amount, "refund", r.ID, prefix+":provider"); err != nil {
		return err
	}
	status := "partially_refunded"
	if p.RefundedAmount+r.Amount == p.Amount {
		status = "refunded"
	}
	if err := s.db.Model(&p).Updates(map[string]any{"refunded_amount": p.RefundedAmount + r.Amount, "status": status}).Error; err != nil {
		return err
	}
	if err := s.db.Model(&r).Updates(map[string]any{"status": "processed", "work_status": "done"}).Error; err != nil {
		return err
	}
	actor := ""
	if r.ApprovedBy != nil {
		actor = *r.ApprovedBy
	}
	if err := s.CreateAuditLog(actor, "refund_processed", "refund", r.ID, "", map[string]any{"amount": r.Amount, "before": p.RefundedAmount, "after": p.RefundedAmount + r.Amount}); err != nil {
		return err
	}
	return s.enqueue(p.PayerID, "refund_processed", "", "")
}
