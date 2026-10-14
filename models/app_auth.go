package models

import "time"

type AppSession struct {
	ID                string     `json:"id" gorm:"primaryKey;type:uuid"`
	UserID            string     `json:"user_id" gorm:"type:uuid;not null;index:idx_app_session_user_expiry,priority:1"`
	FamilyID          string     `json:"-" gorm:"type:uuid;not null;index"`
	TokenHash         string     `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	DeviceID          string     `json:"device_id" gorm:"size:128"`
	Platform          string     `json:"platform" gorm:"size:32"`
	IP                string     `json:"ip" gorm:"size:45"`
	CreatedAt         time.Time  `json:"created_at" gorm:"not null"`
	LastUsedAt        time.Time  `json:"last_used_at" gorm:"not null"`
	ExpiresAt         time.Time  `json:"expires_at" gorm:"not null;index:idx_app_session_user_expiry,priority:2"`
	AbsoluteExpiresAt time.Time  `json:"absolute_expires_at" gorm:"not null"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty" gorm:"index"`
	ReplacedBy        *string    `json:"replaced_by,omitempty" gorm:"type:uuid"`
}

type AppSecurityEvent struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid"`
	UserID    string    `json:"user_id" gorm:"type:uuid;not null;index:idx_app_security_user_time,priority:1"`
	Type      string    `json:"type" gorm:"size:64;not null;index"`
	IP        string    `json:"ip,omitempty" gorm:"size:45"`
	CreatedAt time.Time `json:"created_at" gorm:"not null;index:idx_app_security_user_time,priority:2"`
}

// AuthRateBucket stores only a digest of the account/IP key. Buckets expire at
// the bounded window end and are cleaned in small batches by the limiter.
type AuthRateBucket struct {
	Scope       string    `json:"scope" gorm:"primaryKey;size:64"`
	KeyHash     string    `json:"-" gorm:"primaryKey;type:varchar(64)"`
	Count       int       `json:"count" gorm:"not null"`
	WindowStart time.Time `json:"window_start" gorm:"not null"`
	ExpiresAt   time.Time `json:"expires_at" gorm:"not null;index"`
}
