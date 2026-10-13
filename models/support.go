package models

import "time"

type SupportTicket struct {
	ID                string     `json:"id" gorm:"primaryKey;type:uuid"`
	UserID            string     `json:"user_id" gorm:"type:uuid;not null;index"`
	Subject           string     `json:"subject" gorm:"size:160;not null"`
	Description       string     `json:"description" gorm:"type:text;not null"`
	Category          string     `json:"category" gorm:"size:40;not null;index"`
	Status            string     `json:"status" gorm:"size:24;not null;index"`
	Priority          string     `json:"priority" gorm:"size:24;not null;index"`
	AssignedTo        *string    `json:"assigned_to" gorm:"type:uuid;index"`
	ResolutionOutcome string     `json:"resolution_outcome"`
	ResolutionNote    string     `json:"resolution_note" gorm:"type:text"`
	ResolvedBy        *string    `json:"resolved_by" gorm:"type:uuid"`
	ResolvedAt        *time.Time `json:"resolved_at"`
	CreatedAt         time.Time  `json:"created_at" gorm:"index"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type SupportTicketNote struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid"`
	TicketID  string    `json:"ticket_id" gorm:"type:uuid;not null;index"`
	StaffID   string    `json:"staff_id" gorm:"type:uuid;not null"`
	Note      string    `json:"note" gorm:"type:text;not null"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateSupportTicketInput struct {
	Subject     string `json:"subject"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

type RateRideInput struct {
	Score   int    `json:"score"`
	Comment string `json:"comment"`
}

type ModerateRatingInput struct {
	Reason string `json:"reason"`
}
