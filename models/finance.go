package models

import (
	"errors"
	"gorm.io/gorm"
	"time"
)

type Wallet struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid"`
	UserID    string    `json:"user_id" gorm:"type:uuid;uniqueIndex"`
	Balance   int64     `json:"balance"`
	Held      int64     `json:"held"`
	Version   int64     `json:"version"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}
type LedgerEntry struct {
	ID             string    `json:"id" gorm:"primaryKey;type:uuid"`
	WalletID       *string   `json:"wallet_id" gorm:"type:uuid;index"`
	Account        string    `json:"account" gorm:"index"`
	Direction      string    `json:"direction"`
	Amount         int64     `json:"amount" gorm:"check:amount > 0"`
	BalanceAfter   int64     `json:"balance_after"`
	RefType        string    `json:"ref_type"`
	RefID          string    `json:"ref_id" gorm:"index"`
	IdempotencyKey string    `json:"idempotency_key" gorm:"uniqueIndex"`
	Memo           string    `json:"memo"`
	CreatedAt      time.Time `json:"created_at"`
}

func (*LedgerEntry) BeforeUpdate(*gorm.DB) error { return errors.New("ledger entries are immutable") }
func (*LedgerEntry) BeforeDelete(*gorm.DB) error { return errors.New("ledger entries are immutable") }

type PaymentTransaction struct {
	ID             string     `json:"id" gorm:"primaryKey;type:uuid"`
	RideID         *string    `json:"ride_id" gorm:"type:uuid;uniqueIndex:idx_paid_ride,where:status = 'initiated' OR status = 'pending' OR status = 'succeeded' OR status = 'partially_refunded' OR status = 'refunded'"`
	PayerID        string     `json:"payer_id" gorm:"type:uuid;index"`
	DriverID       *string    `json:"driver_id" gorm:"type:uuid;index"`
	Method         string     `json:"method" gorm:"index"`
	Amount         int64      `json:"amount"`
	Fare           int64      `json:"fare"`
	Commission     int64      `json:"commission"`
	DiscountAmount int64      `json:"discount_amount"`
	RefundedAmount int64      `json:"refunded_amount"`
	Status         string     `json:"status" gorm:"index"`
	Provider       string     `json:"provider"`
	ProviderRef    string     `json:"provider_ref" gorm:"index"`
	MaskedPhone    string     `json:"masked_phone"`
	PhoneEncrypted string     `json:"-"`
	FailureCode    string     `json:"failure_code"`
	IdempotencyKey string     `json:"idempotency_key" gorm:"uniqueIndex"`
	RequestHash    string     `json:"-"`
	WorkStatus     string     `json:"-" gorm:"index"`
	ClaimedAt      *time.Time `json:"-"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at" gorm:"index"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
type CommissionRule struct {
	ID            string    `json:"id" gorm:"primaryKey;type:uuid"`
	Scope         string    `json:"scope"`
	VehicleType   string    `json:"vehicle_type"`
	Percent       int       `json:"percent"`
	MinFee        int64     `json:"min_fee"`
	EffectiveFrom time.Time `json:"effective_from" gorm:"index"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
}
type PaymentSettings struct {
	ID                       string    `json:"id" gorm:"primaryKey;type:uuid"`
	DriverMaxNegativeBalance int64     `json:"driver_max_negative_balance"`
	PayoutMin                int64     `json:"payout_min"`
	PayoutAutoApproveMax     int64     `json:"payout_auto_approve_max"`
	RefundSupportLimit       int64     `json:"refund_support_limit"`
	UpdatedBy                string    `json:"updated_by"`
	CreatedAt                time.Time `json:"created_at" gorm:"index"`
}
type DriverPayout struct {
	DestinationHash      string     `json:"-" gorm:"index"`
	ID                   string     `json:"id" gorm:"primaryKey;type:uuid"`
	DriverID             string     `json:"driver_id" gorm:"type:uuid;index"`
	Amount               int64      `json:"amount"`
	Fee                  int64      `json:"fee"`
	Method               string     `json:"method"`
	MaskedDestination    string     `json:"masked_destination"`
	DestinationEncrypted string     `json:"-"`
	Status               string     `json:"status" gorm:"index"`
	RequestedAt          time.Time  `json:"requested_at"`
	ApprovedBy           *string    `json:"approved_by" gorm:"type:uuid"`
	RejectReason         string     `json:"reject_reason"`
	ProviderRef          string     `json:"provider_ref" gorm:"index"`
	BatchID              *string    `json:"batch_id" gorm:"type:uuid"`
	IdempotencyKey       string     `json:"idempotency_key" gorm:"uniqueIndex"`
	ClaimedAt            *time.Time `json:"-"`
	CreatedAt            time.Time  `json:"created_at" gorm:"index"`
}
type Refund struct {
	Destination    string     `json:"destination"`
	ProviderRef    string     `json:"provider_ref" gorm:"index"`
	WorkStatus     string     `json:"-" gorm:"index"`
	ClaimedAt      *time.Time `json:"-"`
	FailureCode    string     `json:"failure_code"`
	ID             string     `json:"id" gorm:"primaryKey;type:uuid"`
	TransactionID  string     `json:"transaction_id" gorm:"type:uuid;index"`
	Amount         int64      `json:"amount"`
	Reason         string     `json:"reason"`
	Status         string     `json:"status" gorm:"index"`
	RequestedBy    string     `json:"requested_by" gorm:"type:uuid"`
	ApprovedBy     *string    `json:"approved_by" gorm:"type:uuid"`
	IdempotencyKey string     `json:"idempotency_key" gorm:"uniqueIndex"`
	CreatedAt      time.Time  `json:"created_at"`
}
type WebhookEvent struct {
	ID          string    `json:"id" gorm:"primaryKey;type:uuid"`
	Provider    string    `json:"provider"`
	EventID     string    `json:"event_id" gorm:"uniqueIndex"`
	Reference   string    `json:"reference" gorm:"index"`
	PayloadHash string    `json:"payload_hash"`
	ProcessedAt time.Time `json:"processed_at"`
	CreatedAt   time.Time `json:"created_at"`
}
type ReconciliationRun struct {
	ClaimedAt  *time.Time `json:"-"`
	Key        string     `json:"key" gorm:"uniqueIndex"`
	ID         string     `json:"id" gorm:"primaryKey;type:uuid"`
	Status     string     `json:"status" gorm:"index"`
	Exceptions int        `json:"exceptions"`
	CreatedAt  time.Time  `json:"created_at"`
}
type ReconciliationException struct {
	ID            string    `json:"id" gorm:"primaryKey;type:uuid"`
	RunID         *string   `json:"run_id" gorm:"type:uuid;index"`
	TransactionID string    `json:"transaction_id" gorm:"index"`
	Type          string    `json:"type"`
	Status        string    `json:"status"`
	Note          string    `json:"note"`
	CreatedAt     time.Time `json:"created_at"`
}
