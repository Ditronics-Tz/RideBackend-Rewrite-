package models

import "time"

type AccountDeletionRequest struct {
	ID               string     `json:"id" gorm:"primaryKey;type:uuid"`
	UserID           string     `json:"user_id" gorm:"type:uuid;not null;index;uniqueIndex:idx_active_deletion,where:status = 'pending' OR status = 'processing' OR status = 'on_hold' OR status = 'blocked'"`
	Status           string     `json:"status" gorm:"not null;index;uniqueIndex:idx_active_deletion,where:status = 'pending' OR status = 'processing' OR status = 'on_hold' OR status = 'blocked'"`
	Source           string     `json:"source" gorm:"not null"`
	RequestedAt      time.Time  `json:"requested_at"`
	ScheduledFor     time.Time  `json:"scheduled_for"`
	CompletedAt      *time.Time `json:"completed_at"`
	CancelledAt      *time.Time `json:"cancelled_at"`
	BlockReason      string     `json:"block_reason,omitempty"`
	HoldReason       string     `json:"hold_reason,omitempty"`
	HoldBy           *string    `json:"hold_by,omitempty" gorm:"type:uuid"`
	Error            string     `json:"error,omitempty"`
	PreviousStatus   string     `json:"-"`
	VerificationNote string     `json:"verification_note,omitempty"`
	CleanupPaths     string     `json:"-" gorm:"type:text"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type DataExportRequest struct {
	ID            string     `json:"id" gorm:"primaryKey;type:uuid"`
	UserID        string     `json:"user_id" gorm:"type:uuid;not null;index;uniqueIndex:idx_active_export,where:status = 'pending' OR status = 'processing' OR status = 'ready'"`
	Status        string     `json:"status" gorm:"not null;index;uniqueIndex:idx_active_export,where:status = 'pending' OR status = 'processing' OR status = 'ready'"`
	RequestedAt   time.Time  `json:"requested_at"`
	ReadyAt       *time.Time `json:"ready_at"`
	ExpiresAt     *time.Time `json:"expires_at" gorm:"index"`
	FilePath      string     `json:"-"`
	FileSize      int64      `json:"file_size"`
	DownloadCount int        `json:"download_count"`
	Error         string     `json:"error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}
