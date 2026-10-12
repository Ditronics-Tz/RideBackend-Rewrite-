package models

import "time"

type UserDevice struct {
	ID         string    `json:"id" gorm:"primaryKey;type:uuid"`
	UserID     string    `json:"user_id" gorm:"type:uuid;index;uniqueIndex:idx_user_device,priority:1"`
	DeviceHash string    `json:"-" gorm:"index;uniqueIndex:idx_user_device,priority:2"`
	Platform   string    `json:"platform"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen" gorm:"index"`
	LastIP     string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
}
type RideLocationPoint struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid"`
	RideID    string    `json:"ride_id" gorm:"type:uuid;index"`
	UserID    string    `json:"user_id" gorm:"type:uuid;index"`
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	Accuracy  float64   `json:"accuracy"`
	At        time.Time `json:"at" gorm:"index"`
	CreatedAt time.Time `json:"created_at"`
}
type FraudFlag struct {
	ID              string     `json:"id" gorm:"primaryKey;type:uuid"`
	UserID          string     `json:"user_id" gorm:"type:uuid;index"`
	RuleKey         string     `json:"rule_key" gorm:"index"`
	Type            string     `json:"type" gorm:"index"`
	Severity        string     `json:"severity" gorm:"index"`
	Score           int        `json:"score"`
	Status          string     `json:"status" gorm:"index"`
	Evidence        string     `json:"evidence" gorm:"type:jsonb"`
	RelatedUserIDs  string     `json:"related_user_ids" gorm:"type:jsonb"`
	DedupeKey       string     `json:"dedupe_key" gorm:"uniqueIndex:idx_open_fraud,where:status = 'open' OR status = 'reviewing'"`
	AssignedTo      *string    `json:"assigned_to" gorm:"type:uuid"`
	ResolutionNote  string     `json:"resolution_note"`
	ResolvedBy      *string    `json:"resolved_by" gorm:"type:uuid"`
	ResolvedAt      *time.Time `json:"resolved_at"`
	SuppressedUntil *time.Time `json:"suppressed_until"`
	CreatedAt       time.Time  `json:"created_at" gorm:"index"`
}
type FraudRule struct {
	Key            string    `json:"key" gorm:"primaryKey"`
	Enabled        bool      `json:"enabled"`
	Params         string    `json:"params" gorm:"type:jsonb"`
	Severity       string    `json:"severity"`
	AutoMitigation string    `json:"auto_mitigation"`
	UpdatedBy      *string   `json:"updated_by" gorm:"type:uuid"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type FraudAction struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid"`
	FlagID    string    `json:"flag_id" gorm:"type:uuid;index"`
	Action    string    `json:"action"`
	By        string    `json:"by"`
	At        time.Time `json:"at"`
	CreatedAt time.Time `json:"created_at"`
}
type FraudNote struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid"`
	FlagID    string    `json:"flag_id" gorm:"type:uuid;index"`
	StaffID   string    `json:"staff_id" gorm:"type:uuid"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}
type FraudMitigation struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid"`
	FlagID    string    `json:"flag_id" gorm:"type:uuid;uniqueIndex:idx_flag_mitigation,priority:1"`
	UserID    string    `json:"user_id" gorm:"type:uuid;index"`
	Type      string    `json:"type" gorm:"uniqueIndex:idx_flag_mitigation,priority:2"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}
type FraudEvent struct {
	ID        string     `json:"id" gorm:"primaryKey;type:uuid"`
	UserID    string     `json:"user_id" gorm:"type:uuid;index"`
	Type      string     `json:"type"`
	RideID    *string    `json:"ride_id" gorm:"type:uuid"`
	Status    string     `json:"status" gorm:"index"`
	DedupeKey string     `json:"-" gorm:"uniqueIndex"`
	ClaimedAt *time.Time `json:"-"`
	CreatedAt time.Time  `json:"created_at"`
}
