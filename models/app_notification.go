package models

import "time"

// AppNotification is the durable passenger inbox. Push is only a delivery hint.
type AppNotification struct {
	ID        string     `json:"id" gorm:"primaryKey;type:uuid"`
	UserID    string     `json:"-" gorm:"type:uuid;not null"`
	Kind      string     `json:"kind" gorm:"type:varchar(24);not null"`
	Title     string     `json:"title" gorm:"type:varchar(160);not null"`
	Body      string     `json:"body" gorm:"type:varchar(500);not null"`
	Data      string     `json:"-" gorm:"type:jsonb;not null"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at" gorm:"not null"`
}

type PushDevice struct {
	ID              string    `json:"id" gorm:"primaryKey;type:uuid"`
	UserID          string    `json:"-" gorm:"type:uuid;not null"`
	SessionFamilyID string    `json:"-" gorm:"type:uuid;not null"`
	TokenHash       string    `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	TokenCiphertext string    `json:"-" gorm:"type:text;not null"`
	Platform        string    `json:"platform" gorm:"type:varchar(16);not null"`
	Active          bool      `json:"-" gorm:"not null"`
	CreatedAt       time.Time `json:"created_at" gorm:"not null"`
	UpdatedAt       time.Time `json:"-" gorm:"not null"`
}

type PushDelivery struct {
	NotificationID string     `json:"-" gorm:"primaryKey;type:uuid"`
	DeviceID       string     `json:"-" gorm:"primaryKey;type:uuid"`
	Status         string     `json:"status" gorm:"type:varchar(16);not null"`
	Attempts       int        `json:"attempts" gorm:"not null"`
	ClaimedAt      *time.Time `json:"-"`
	NextAttemptAt  time.Time  `json:"-" gorm:"not null"`
	SentAt         *time.Time `json:"sent_at"`
}
