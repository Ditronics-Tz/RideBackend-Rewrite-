package models

import "time"

// Money is whole TZS. DiscountValue is a percentage only when DiscountType=percent.
type PromoCode struct {
	ID             string    `json:"id" gorm:"primaryKey;type:uuid"`
	Code           string    `json:"code" gorm:"not null;uniqueIndex;size:20"`
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	DiscountType   string    `json:"discount_type" gorm:"not null"`
	DiscountValue  int64     `json:"discount_value" gorm:"not null"`
	MaxDiscount    *int64    `json:"max_discount"`
	MinFare        int64     `json:"min_fare" gorm:"not null;default:0"`
	Audience       string    `json:"audience" gorm:"not null;index"`
	FirstRideOnly  bool      `json:"first_ride_only"`
	StartsAt       time.Time `json:"starts_at"`
	ExpiresAt      time.Time `json:"expires_at" gorm:"index"`
	MaxRedemptions *int64    `json:"max_redemptions"`
	MaxPerUser     int64     `json:"max_per_user" gorm:"not null;default:1"`
	RedeemedCount  int64     `json:"redeemed_count" gorm:"not null;default:0"`
	Status         string    `json:"status" gorm:"not null;index"`
	Source         string    `json:"source" gorm:"not null;default:admin"`
	CreatedBy      *string   `json:"created_by" gorm:"type:uuid"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (p PromoCode) EffectiveStatus(now time.Time) string {
	if p.Status != "active" {
		return p.Status
	}
	if !now.Before(p.ExpiresAt) {
		return "expired"
	}
	if now.Before(p.StartsAt) {
		return "scheduled"
	}
	return "active"
}

type PromoAudience struct {
	PromoID    string     `json:"promo_id" gorm:"primaryKey;type:uuid"`
	UserID     string     `json:"user_id" gorm:"primaryKey;type:uuid"`
	GrantedBy  *string    `json:"granted_by" gorm:"type:uuid"`
	GrantedAt  time.Time  `json:"granted_at"`
	NotifiedAt *time.Time `json:"notified_at"`
}
type PromoRedemption struct {
	ID             string    `json:"id" gorm:"primaryKey;type:uuid"`
	PromoID        string    `json:"promo_id" gorm:"type:uuid;not null;index:idx_promo_user,priority:1"`
	UserID         string    `json:"user_id" gorm:"type:uuid;not null;index:idx_promo_user,priority:2"`
	RideID         *string   `json:"ride_id" gorm:"type:uuid;uniqueIndex"`
	DiscountAmount int64     `json:"discount_amount" gorm:"not null"`
	Status         string    `json:"status" gorm:"not null;index"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
