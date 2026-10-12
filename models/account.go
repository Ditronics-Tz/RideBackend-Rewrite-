package models

import "time"

type UserStatusEvent struct {
	ID         string     `json:"id" gorm:"primaryKey;type:uuid"`
	UserID     string     `json:"user_id" gorm:"type:uuid;not null;index"`
	ActorID    *string    `json:"actor_id" gorm:"type:uuid"`
	FromStatus string     `json:"from_status"`
	ToStatus   string     `json:"to_status"`
	Reason     string     `json:"reason"`
	Until      *time.Time `json:"until"`
	CreatedAt  time.Time  `json:"created_at"`
}

type SupportNote struct {
	ID         string    `json:"id" gorm:"primaryKey;type:uuid"`
	UserID     string    `json:"user_id" gorm:"type:uuid;not null;index"`
	AuthorID   string    `json:"author_id" gorm:"type:uuid;not null"`
	AuthorName string    `json:"author_name"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
}

type PhoneChallenge struct {
	UserID    string `gorm:"primaryKey;type:uuid"`
	Phone     string
	CodeHash  string
	Attempts  int
	ExpiresAt time.Time
	SentAt    time.Time
}
