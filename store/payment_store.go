package store

import (
	"encoding/json"
	"gorm.io/gorm/clause"
	"ride-backend/models"
	"ride-backend/payments"
	"ride-backend/utils"
	"time"
)

func mobileMethod(method string) bool {
	return method == "mpesa" || method == "tigopesa" || method == "airtelmoney"
}

type PayInput struct {
	Method    string `json:"method"`
	Phone     string `json:"phone"`
	PromoCode string `json:"promo_code"`
	Amount    int64  `json:"amount"`
}

func (s *Store) InitiatePayment(user, ride, key string, in PayInput) (*models.PaymentTransaction, error) {
	if !mobileMethod(in.Method) && in.Method != "wallet" && in.Method != "cash" {
		return nil, FieldError("method", "invalid payment method")
	}
	if ride == "" && !mobileMethod(in.Method) {
		return nil, FieldError("method", "top-ups require mobile money")
	}
	k, err := idempotency(user, key)
	if err != nil {
		return nil, err
	}
	fingerprint, err := json.Marshal(map[string]any{"ride": ride, "method": in.Method, "phone": in.Phone, "topup_amount": in.Amount, "promo": in.PromoCode})
	if err != nil {
		return nil, err
	}
	hash := tokenHash(string(fingerprint))
	var p models.PaymentTransaction
	err = s.Transaction(func(tx *Store) error {
		var u models.User
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", user).Error; err != nil {
			return err
		}
		if u.Role != models.RolePassenger {
			return OpError(403, "passenger account required")
		}
		if err := tx.db.First(&p, "idempotency_key = ?", k).Error; err == nil {
			if p.RequestHash != hash {
				return OpError(409, "idempotency key reused with different payment")
			}
			return nil
		} else if !notFound(err) {
			return err
		}
		p = models.PaymentTransaction{ID: newID(), PayerID: user, Method: in.Method, Status: "initiated", Provider: "internal", IdempotencyKey: k, RequestHash: hash, WorkStatus: "pending", CreatedAt: tx.now(), ExpiresAt: tx.now().Add(3 * time.Minute)}
		if ride != "" {
			r, err := tx.RideForUser(ride, user)
			if err != nil {
				return err
			}
			if r.PassengerID != user || r.Status != "completed" {
				return OpError(409, "payment requires a completed passenger ride")
			}
			if !MoneyValid(r.Fare) {
				return FieldError("fare", "server fare is invalid")
			}
			p.RideID = &r.ID
			p.DriverID = &r.DriverID
			p.Fare = r.Fare
			p.Commission, err = tx.rideCommission(r)
			if err != nil {
				return err
			}
			var active int64
			if err := tx.db.Model(&models.PaymentTransaction{}).Where("ride_id = ? AND status IN ?", ride, []string{"initiated", "pending", "succeeded", "partially_refunded", "refunded"}).Count(&active).Error; err != nil {
				return err
			}
			if active > 0 {
				return OpError(409, "this ride already has an active or completed payment")
			}
			if in.PromoCode != "" {
				redemption, err := tx.ReservePromo(user, in.PromoCode, p.Fare, ride)
				if err != nil {
					return err
				}
				p.DiscountAmount = redemption.DiscountAmount
			} else {
				var redemption models.PromoRedemption
				err := tx.db.First(&redemption, "ride_id = ? AND user_id = ? AND status IN ?", ride, user, []string{"reserved", "applied"}).Error
				if err == nil {
					p.DiscountAmount = redemption.DiscountAmount
				} else if !notFound(err) {
					return err
				}
			}
			p.Amount = p.Fare - p.DiscountAmount
		} else {
			if !MoneyValid(in.Amount) {
				return FieldError("amount", "top-up amount must be positive whole TZS")
			}
			p.Fare = in.Amount
			p.Amount = in.Amount
		}
		if mobileMethod(in.Method) && p.Amount > 0 {
			if !e164.MatchString(in.Phone) {
				return FieldError("phone", "phone must use E.164")
			}
			if tx.Policy == nil || len(tx.Policy.PaymentEncryptionKey) != 32 {
				return OpError(503, "payment phone encryption is not configured")
			}
			encrypted, err := utils.EncryptAESGCM(in.Phone, tx.Policy.PaymentEncryptionKey)
			if err != nil {
				return err
			}
			p.PhoneEncrypted = encrypted
			p.MaskedPhone = maskPhone(in.Phone)
			p.Provider = tx.Policy.PaymentProviderName
			if p.Provider == "" {
				p.Provider = "mock"
			}
		}
		if err := tx.db.Create(&p).Error; err != nil {
			return err
		}
		if in.Method == "cash" || in.Method == "wallet" || p.Amount == 0 {
			return tx.settlePayment(&p)
		}
		return nil
	})
	return &p, err
}
func maskPhone(phone string) string {
	if len(phone) < 7 {
		return "***"
	}
	return phone[:5] + "** *** " + phone[len(phone)-3:]
}
func (s *Store) settlePayment(p *models.PaymentTransaction) error {
	if p.Status == "succeeded" || p.Status == "refunded" || p.Status == "partially_refunded" {
		return nil
	}
	if p.Status == "failed" || p.Status == "expired" {
		return OpError(409, "late payment requires reconciliation")
	}
	prefix := "payment:" + p.ID
	if p.RideID == nil {
		if err := s.postWallet(p.PayerID, "passenger_wallet", "credit", p.Amount, 0, "topup", p.ID, prefix+":wallet"); err != nil {
			return err
		}
		if err := s.postSystem("provider_clearing", "debit", p.Amount, "topup", p.ID, prefix+":clearing"); err != nil {
			return err
		}
	} else {
		settings, err := s.GetPaymentSettings()
		if err != nil {
			return err
		}
		driver := *p.DriverID
		if p.Method == "cash" {
			if err := s.postWallet(driver, "driver_wallet", "credit", p.DiscountAmount, -MaxMoney, "payment", p.ID, prefix+":promo_credit"); err != nil {
				return err
			}
			if err := s.postWallet(driver, "driver_wallet", "debit", p.Commission, -settings.DriverMaxNegativeBalance, "payment", p.ID, prefix+":commission_debit"); err != nil {
				return err
			}
		} else {
			if p.Method == "wallet" {
				if err := s.postWallet(p.PayerID, "passenger_wallet", "debit", p.Amount, 0, "payment", p.ID, prefix+":payer"); err != nil {
					return err
				}
			} else {
				if err := s.postSystem("provider_clearing", "debit", p.Amount, "payment", p.ID, prefix+":clearing"); err != nil {
					return err
				}
			}
			if err := s.postWallet(driver, "driver_wallet", "credit", p.Fare-p.Commission, -MaxMoney, "payment", p.ID, prefix+":driver"); err != nil {
				return err
			}
		}
		if err := s.postSystem("platform_revenue", "credit", p.Commission, "payment", p.ID, prefix+":commission"); err != nil {
			return err
		}
		if err := s.postSystem("promo_subsidy", "debit", p.DiscountAmount, "payment", p.ID, prefix+":subsidy"); err != nil {
			return err
		}
		if err := s.ConfirmPromo(*p.RideID); err != nil {
			return err
		}
		if err := s.OnRideCompleted(*p.RideID, p.PayerID, p.Fare); err != nil {
			return err
		}
	}
	if err := s.db.Model(p).Updates(map[string]any{"status": "succeeded", "work_status": "done", "phone_encrypted": ""}).Error; err != nil {
		return err
	}
	p.Status = "succeeded"
	return s.enqueue(p.PayerID, "payment_succeeded", "", "")
}

type PaymentEvent struct {
	EventID   string `json:"event_id"`
	Type      string `json:"type"`
	Reference string `json:"reference"`
	Status    string `json:"status"`
	Amount    int64  `json:"amount"`
}

func (s *Store) ProcessPaymentEvent(provider string, event PaymentEvent, hash string) error {
	if event.EventID == "" || len(event.EventID) > 200 || event.Reference == "" || !map[string]bool{"succeeded": true, "failed": true, "pending": true}[event.Status] {
		return FieldError("event", "invalid payment event")
	}
	return s.Transaction(func(tx *Store) error {
		e := models.WebhookEvent{ID: newID(), Provider: provider, EventID: provider + ":" + event.EventID, Reference: event.Reference, PayloadHash: hash, ProcessedAt: tx.now(), CreatedAt: tx.now()}
		result := tx.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&e)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if event.Type == "refund" {
			return tx.completeProviderRefund(event)
		}
		if event.Type == "payout" {
			return tx.completePayout(event)
		}
		var p models.PaymentTransaction
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("provider = ? AND (CAST(id AS TEXT) = ? OR provider_ref = ?)", provider, event.Reference, event.Reference).First(&p).Error; err != nil {
			if notFound(err) {
				return tx.financeException("", "unknown_reference", event.Reference)
			}
			return err
		}
		if p.Amount != event.Amount {
			return tx.financeException(p.ID, "amount_mismatch", "")
		}
		if event.Status == "pending" {
			return nil
		}
		if p.Status == "succeeded" || p.Status == "refunded" || p.Status == "partially_refunded" {
			return nil
		}
		if p.Status == "expired" || p.Status == "failed" {
			if event.Status == "succeeded" {
				return tx.financeException(p.ID, "late_success", "")
			}
			return nil
		}
		if event.Status == "succeeded" {
			return tx.settlePayment(&p)
		}
		if err := tx.db.Model(&p).Updates(map[string]any{"status": "failed", "failure_code": "provider_failed", "work_status": "done", "phone_encrypted": ""}).Error; err != nil {
			return err
		}
		if p.RideID != nil {
			if err := tx.ReleasePromo(*p.RideID); err != nil {
				return err
			}
		}
		return tx.enqueue(p.PayerID, "payment_failed", "", "")
	})
}
func (s *Store) financeException(transaction, kind, reference string) error {
	e := models.ReconciliationException{ID: newID(), TransactionID: transaction, Type: kind, Status: "open", CreatedAt: s.now()}
	if err := s.db.Create(&e).Error; err != nil {
		return err
	}
	return s.enqueue("staff:admin", "fraud_review", "", "")
}

// Replay-safe status checks use the same settlement transaction as webhooks.
func (s *Store) processPaymentStatus(p models.PaymentTransaction, result payments.Result) error {
	return s.ProcessPaymentEvent(p.Provider, PaymentEvent{EventID: "poll:" + p.ID + ":" + result.Status, Reference: p.ID, Status: result.Status, Amount: result.Amount}, tokenHash(p.ID+result.Status))
}
