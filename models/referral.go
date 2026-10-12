package models

import "time"

type ReferralSettings struct {
	ID                    int    `json:"id" gorm:"primaryKey;check:referral_singleton,id = 1"`
	Enabled               bool   `json:"enabled"`
	RefereeRewardType     string `json:"referee_reward_type"`
	RefereeRewardValue    int64  `json:"referee_reward_value"`
	ReferrerRewardType    string `json:"referrer_reward_type"`
	ReferrerRewardValue   int64  `json:"referrer_reward_value"`
	QualifyingMinFare     int64  `json:"qualifying_min_fare"`
	MaxRewardsPerReferrer int64  `json:"max_rewards_per_referrer"`
	RewardValidDays       int    `json:"reward_valid_days"`
}
type UserReferralCode struct {
	UserID string `json:"user_id" gorm:"primaryKey;type:uuid"`
	Code   string `json:"code" gorm:"uniqueIndex;not null;size:8"`
}
type Referral struct {
	ID                   string    `json:"id" gorm:"primaryKey;type:uuid"`
	ReferrerID           string    `json:"referrer_id" gorm:"type:uuid;not null;index"`
	RefereeID            string    `json:"referee_id" gorm:"type:uuid;not null;uniqueIndex"`
	Status               string    `json:"status" gorm:"not null;index"`
	RejectReason         string    `json:"reject_reason"`
	QualifyingRideID     *string   `json:"qualifying_ride_id" gorm:"type:uuid"`
	ReferrerRewardStatus string    `json:"referrer_reward_status"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// Callback receipts provide idempotency/first-ride evidence, not a ride domain model.
// Fare, route, driver, dispatch and ride lifecycle belong to the future rides module.
type RideCompletionReceipt struct {
	RideID      string `gorm:"primaryKey;type:uuid"`
	PassengerID string `gorm:"type:uuid;not null;index"`
	CreatedAt   time.Time
}

// Never delete these claims when a user changes phone or a reward expires.
type ReferralRewardPhone struct {
	Phone      string `gorm:"primaryKey"`
	ReferralID string `gorm:"type:uuid;not null;uniqueIndex"`
	CreatedAt  time.Time
}
