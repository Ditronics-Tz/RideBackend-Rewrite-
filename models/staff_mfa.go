package models

import "time"

// StaffMFAChallenge is a short-lived login or enrollment attempt.
// It is not a staff session and cannot call protected APIs.
type StaffMFAChallenge struct {
	ID             string     `json:"-" gorm:"primaryKey;type:uuid"`
	StaffID        string     `json:"-" gorm:"type:uuid;not null;index"`
	Purpose        string     `json:"-" gorm:"type:varchar(16);not null"`
	ExpiresAt      time.Time  `json:"-" gorm:"not null;index"`
	ConsumedAt     *time.Time `json:"-"`
	FailedAttempts int64      `json:"-" gorm:"not null;default:0"`
	CreatedAt      time.Time  `json:"-" gorm:"not null"`
}

func (StaffMFAChallenge) TableName() string { return "staff_mfa_challenges" }

// StaffMFARecoveryCode stores only a hash of a one-time recovery code.
type StaffMFARecoveryCode struct {
	ID        string     `json:"-" gorm:"primaryKey;type:uuid"`
	StaffID   string     `json:"-" gorm:"type:uuid;not null;index"`
	CodeHash  string     `json:"-" gorm:"type:varchar(64);not null"`
	UsedAt    *time.Time `json:"-"`
	CreatedAt time.Time  `json:"-" gorm:"not null"`
}

func (StaffMFARecoveryCode) TableName() string { return "staff_mfa_recovery_codes" }
