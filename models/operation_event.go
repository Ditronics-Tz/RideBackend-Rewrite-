package models

import "time"

// OperationEvent is a metadata-only record of an API write. Request bodies,
// credentials, tokens and concrete URL paths are never stored here.
type OperationEvent struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid"`
	ActorType string    `json:"actor_type" gorm:"type:varchar(16);not null"`
	ActorID   string    `json:"actor_id" gorm:"type:varchar(64);index"`
	Action    string    `json:"action" gorm:"type:varchar(64);not null;index"`
	Method    string    `json:"method" gorm:"type:varchar(8);not null"`
	Route     string    `json:"route" gorm:"type:varchar(160);not null"`
	Status    int       `json:"status" gorm:"not null"`
	RequestID string    `json:"request_id" gorm:"type:varchar(64);not null;index"`
	TargetID  string    `json:"target_id" gorm:"type:varchar(64);index"`
	CreatedAt time.Time `json:"created_at" gorm:"not null;index"`
}

func (OperationEvent) TableName() string { return "operation_events" }
