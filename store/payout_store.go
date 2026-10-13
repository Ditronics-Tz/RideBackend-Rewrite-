package store

import (
	"gorm.io/gorm/clause"
	"math/big"
	"ride-backend/models"
	"ride-backend/utils"
)

func (s *Store) RequestPayout(user, key, method, phone string, amount int64) (*models.DriverPayout, error) {
	if !MoneyValid(amount) {
		return nil, FieldError("amount", "positive whole TZS amount required")
	}
	if !mobileMethod(method) || !e164.MatchString(phone) {
		return nil, FieldError("method", "mobile money method and E.164 phone required")
	}
	k, err := idempotency(user, key)
	if err != nil {
		return nil, err
	}
	if s.Policy == nil || len(s.Policy.PaymentEncryptionKey) != 32 {
		return nil, OpError(503, "payment encryption is not configured")
	}
	var p models.DriverPayout
	err = s.Transaction(func(tx *Store) error {
		var u models.User
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", user).Error; err != nil {
			return err
		}
		if u.Role != models.RoleDriver || !u.IsActive || u.Status != "active" {
			return OpError(403, "active driver required")
		}
		if err := tx.db.First(&p, "idempotency_key = ?", k).Error; err == nil {
			if p.Amount != amount || p.Method != method || p.DestinationHash != tokenHash(string(tx.Policy.PaymentEncryptionKey)+"\x00"+phone) {
				return OpError(409, "idempotency key reused with different payout")
			}
			return nil
		} else if !notFound(err) {
			return err
		}
		var dp models.DriverProfile
		if err := tx.db.First(&dp, "user_id = ?", user).Error; err != nil {
			return err
		}
		if dp.VerificationStatus != models.Approved {
			return OpError(403, "driver approval required")
		}
		if err := tx.checkPayoutFraud(user); err != nil {
			return err
		}
		settings, err := tx.GetPaymentSettings()
		if err != nil {
			return err
		}
		if amount < settings.PayoutMin {
			return FieldError("amount", "payout below minimum")
		}
		w, err := tx.lockWallet(user)
		if err != nil {
			return err
		}
		r := tx.db.Model(&models.Wallet{}).Where("id = ? AND version = ? AND balance - held - ? >= ?", w.ID, w.Version, settings.DriverMaxNegativeBalance, amount).Updates(map[string]any{"held": w.Held + amount, "version": w.Version + 1})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return OpError(409, "insufficient available payout balance")
		}
		encrypted, err := utils.EncryptAESGCM(phone, tx.Policy.PaymentEncryptionKey)
		if err != nil {
			return err
		}
		p = models.DriverPayout{ID: newID(), DriverID: user, Amount: amount, Method: method, MaskedDestination: maskPhone(phone), DestinationEncrypted: encrypted, DestinationHash: tokenHash(string(tx.Policy.PaymentEncryptionKey) + "\x00" + phone), Status: "requested", RequestedAt: tx.now(), CreatedAt: tx.now(), IdempotencyKey: k}
		if amount <= settings.PayoutAutoApproveMax {
			p.Status = "approved"
		}
		if err := tx.db.Create(&p).Error; err != nil {
			return err
		}
		return tx.QueueFraudEvent(user, "payout_requested", nil, "payout:"+p.ID)
	})
	return &p, err
}

func (s *Store) releasePayoutHold(p *models.DriverPayout) error {
	w, err := s.lockWallet(p.DriverID)
	if err != nil {
		return err
	}
	if w.Held < p.Amount {
		return OpError(409, "payout hold invariant failed")
	}
	r := s.db.Model(&models.Wallet{}).Where("id = ? AND version = ?", w.ID, w.Version).Updates(map[string]any{"held": w.Held - p.Amount, "version": w.Version + 1})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return OpError(409, "wallet changed")
	}
	return nil
}
func (s *Store) WorkPayout(id, action, actor, user, reason string) error {
	return s.Transaction(func(tx *Store) error {
		var p models.DriverPayout
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, "id = ?", id).Error; err != nil {
			return err
		}
		before := p.Status
		switch action {
		case "approve":
			if p.Status == "approved" {
				return nil
			}
			if p.Status != "requested" {
				return OpError(409, "payout is not requested")
			}
			if err := tx.checkPayoutFraud(p.DriverID); err != nil {
				return err
			}
			p.Status = "approved"
			p.ApprovedBy = &actor
		case "reject", "cancel":
			if p.Status == "rejected" || p.Status == "cancelled" {
				return nil
			}
			if p.Status != "requested" && !(action == "reject" && p.Status == "approved") {
				return OpError(409, "payout can no longer be changed")
			}
			if action == "cancel" && p.DriverID != user {
				return OpError(404, "payout not found")
			}
			if action == "reject" {
				if err := cleanNote(reason, 2000); err != nil {
					return err
				}
				p.Status = "rejected"
				p.RejectReason = reason
			} else {
				p.Status = "cancelled"
			}
			p.DestinationEncrypted = ""
			if err := tx.releasePayoutHold(&p); err != nil {
				return err
			}
		default:
			return OpError(404, "unknown payout action")
		}
		if err := tx.db.Save(&p).Error; err != nil {
			return err
		}
		if actor != "" {
			return tx.CreateAuditLog(actor, "payout_"+action, "payout", p.ID, "", map[string]any{"before": before, "after": p.Status, "amount": p.Amount})
		}
		return nil
	})
}
func (s *Store) completePayout(event PaymentEvent) error {
	var p models.DriverPayout
	if err := s.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("CAST(id AS TEXT) = ? OR provider_ref = ?", event.Reference, event.Reference).First(&p).Error; err != nil {
		if notFound(err) {
			return s.financeException("", "unknown_payout", "")
		}
		return err
	}
	if event.Amount != p.Amount {
		return s.financeException(p.ID, "amount_mismatch", "")
	}
	if p.Status == "paid" || p.Status == "failed" {
		return nil
	}
	if p.Status != "processing" {
		return s.financeException(p.ID, "unexpected_payout_status", "")
	}
	if event.Status == "pending" {
		return nil
	}
	if err := s.releasePayoutHold(&p); err != nil {
		return err
	}
	status := "failed"
	if event.Status == "succeeded" {
		status = "paid"
		if err := s.postWallet(p.DriverID, "driver_wallet", "debit", p.Amount, 0, "payout", p.ID, "payout:"+p.ID+":wallet"); err != nil {
			return err
		}
		if err := s.postSystem("payouts_pending", "credit", p.Amount, "payout", p.ID, "payout:"+p.ID+":clearing"); err != nil {
			return err
		}
		if err := s.enqueue(p.DriverID, "payout_paid", "", ""); err != nil {
			return err
		}
	}
	return s.db.Model(&p).Updates(map[string]any{"status": status, "destination_encrypted": ""}).Error
}
func mulDiv(a, b, c int64) int64 {
	var x, y, z big.Int
	x.SetInt64(a)
	y.SetInt64(b)
	z.SetInt64(c)
	x.Mul(&x, &y)
	x.Div(&x, &z)
	return x.Int64()
}
func (s *Store) RequestRefund(transaction, key, reason string, amount int64, actor *models.StaffUser) (*models.Refund, error) {
	if !MoneyValid(amount) {
		return nil, FieldError("amount", "positive whole TZS refund required")
	}
	if err := cleanNote(reason, 2000); err != nil {
		return nil, err
	}
	k, err := idempotency(actor.ID, key)
	if err != nil {
		return nil, err
	}
	var refund models.Refund
	err = s.Transaction(func(tx *Store) error {
		var p models.PaymentTransaction
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, "id = ?", transaction).Error; err != nil {
			return err
		}
		if err := tx.db.First(&refund, "idempotency_key = ?", k).Error; err == nil {
			if refund.TransactionID != transaction || refund.Amount != amount {
				return OpError(409, "idempotency key reused with different refund")
			}
			return nil
		} else if !notFound(err) {
			return err
		}
		if p.Status != "succeeded" && p.Status != "partially_refunded" {
			return OpError(409, "payment is not refundable")
		}
		var reserved int64
		if err := tx.db.Model(&models.Refund{}).Where("transaction_id = ? AND status IN ?", p.ID, []string{"requested", "approved", "processed"}).Select("COALESCE(SUM(amount),0)").Scan(&reserved).Error; err != nil {
			return err
		}
		if amount > p.Amount-reserved {
			return OpError(409, "refund exceeds remaining paid amount")
		}
		destination := "wallet"
		if p.RideID == nil {
			destination = "mobile_money"
		}
		refund = models.Refund{Destination: destination, ID: newID(), TransactionID: p.ID, Amount: amount, Reason: reason, Status: "requested", RequestedBy: actor.ID, IdempotencyKey: k, CreatedAt: tx.now()}
		if err := tx.db.Create(&refund).Error; err != nil {
			return err
		}
		if err := tx.CreateAuditLog(actor.ID, "refund_requested", "refund", refund.ID, "", map[string]any{"amount": amount, "before": p.RefundedAmount, "after": p.RefundedAmount}); err != nil {
			return err
		}
		return nil
	})
	return &refund, err
}
func (s *Store) WorkRefund(id, action, note string, actor *models.StaffUser) error {
	return s.Transaction(func(tx *Store) error {
		var r models.Refund
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&r, "id = ?", id).Error; err != nil {
			return err
		}
		settings, err := tx.GetPaymentSettings()
		if err != nil {
			return err
		}
		if actor.Role != models.StaffRoleAdmin && r.Amount > settings.RefundSupportLimit {
			return OpError(403, "refund requires admin approval")
		}
		if r.Status == "processed" && action == "approve" {
			return nil
		}
		if r.Status != "requested" {
			return OpError(409, "refund is not requested")
		}
		if action == "reject" {
			if err := cleanNote(note, 2000); err != nil {
				return err
			}
			if err := tx.db.Model(&r).Update("status", "rejected").Error; err != nil {
				return err
			}
			return tx.CreateAuditLog(actor.ID, "refund_rejected", "refund", id, "", map[string]any{"amount": r.Amount})
		}
		if action != "approve" {
			return OpError(404, "unknown refund action")
		}
		var p models.PaymentTransaction
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, "id = ?", r.TransactionID).Error; err != nil {
			return err
		}
		if r.Amount > p.Amount-p.RefundedAmount {
			return OpError(409, "refund exceeds remaining paid amount")
		}
		prefix := "refund:" + r.ID
		if p.RideID == nil {
			// The top-up is already credited to the wallet. Reserve that balance, then
			// return it to the original provider. Never manufacture a second wallet credit.
			if err := tx.postWallet(p.PayerID, "passenger_wallet", "debit", r.Amount, 0, "refund", r.ID, prefix+":topup_reserve"); err != nil {
				return err
			}
			if err := tx.postSystem("refunds", "credit", r.Amount, "refund", r.ID, prefix+":reserve"); err != nil {
				return err
			}
			if err := tx.db.Model(&r).Updates(map[string]any{"status": "approved", "approved_by": actor.ID, "work_status": "pending"}).Error; err != nil {
				return err
			}
			return tx.CreateAuditLog(actor.ID, "refund_approved", "refund", id, "", map[string]any{"amount": r.Amount, "before": "requested", "after": "approved"})
		} else {
			before := p.RefundedAmount
			after := before + r.Amount
			beforeSubsidy := mulDiv(p.DiscountAmount, before, p.Amount)
			afterSubsidy := mulDiv(p.DiscountAmount, after, p.Amount)
			subsidy := afterSubsidy - beforeSubsidy
			// Allocate commission from the rounded gross fare being reversed.
			// Independent rounding against the paid amount can make the driver
			// portion negative for a partial refund of a discounted ride.
			var reversedCommission int64
			processedRefunds := tx.db.Model(&models.Refund{}).Select("id").Where("transaction_id = ? AND status = ?", p.ID, "processed")
			if err := tx.db.Model(&models.LedgerEntry{}).Where("ref_type = ? AND ref_id IN (?) AND account = ?", "refund", processedRefunds, "platform_revenue").Select("COALESCE(SUM(CASE WHEN direction = 'debit' THEN amount ELSE -amount END),0)").Scan(&reversedCommission).Error; err != nil {
				return err
			}
			commission := mulDiv(p.Commission, after+afterSubsidy, p.Fare) - reversedCommission
			// Earlier partial refunds may have used the previous rounding rule.
			// Respect their immutable entries and carry rounding differences forward.
			if commission < 0 {
				commission = 0
			}
			if commission > r.Amount+subsidy {
				commission = r.Amount + subsidy
			}
			if err := tx.postWallet(*p.DriverID, "driver_wallet", "debit", r.Amount+subsidy-commission, -MaxMoney, "refund", r.ID, prefix+":driver"); err != nil {
				return err
			}
			if err := tx.postSystem("platform_revenue", "debit", commission, "refund", r.ID, prefix+":commission"); err != nil {
				return err
			}
			if err := tx.postSystem("promo_subsidy", "credit", subsidy, "refund", r.ID, prefix+":subsidy"); err != nil {
				return err
			}
			if err := tx.postWallet(p.PayerID, "passenger_wallet", "credit", r.Amount, 0, "refund", r.ID, prefix+":payer"); err != nil {
				return err
			}
		}
		status := "partially_refunded"
		if p.RefundedAmount+r.Amount == p.Amount {
			status = "refunded"
		}
		if err := tx.db.Model(&p).Updates(map[string]any{"refunded_amount": p.RefundedAmount + r.Amount, "status": status}).Error; err != nil {
			return err
		}
		if err := tx.db.Model(&r).Updates(map[string]any{"status": "processed", "approved_by": actor.ID}).Error; err != nil {
			return err
		}
		if err := tx.enqueue(p.PayerID, "refund_processed", "", ""); err != nil {
			return err
		}
		return tx.CreateAuditLog(actor.ID, "refund_processed", "refund", id, "", map[string]any{"amount": r.Amount, "before": p.RefundedAmount, "after": p.RefundedAmount + r.Amount})
	})
}
