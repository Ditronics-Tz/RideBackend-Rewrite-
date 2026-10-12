package models

import "time"

type EmergencyContact struct {
	ID                string    `json:"id" gorm:"primaryKey;type:uuid"`
	UserID            string    `json:"user_id" gorm:"type:uuid;index"`
	Name              string    `json:"name"`
	Phone             string    `json:"phone"`
	Relationship      string    `json:"relationship"`
	NotifyOnSOS       bool      `json:"notify_on_sos"`
	NotifyOnTripShare bool      `json:"notify_on_trip_share"`
	CreatedAt         time.Time `json:"created_at"`
}
type SOSAlert struct {
	ID               string     `json:"id" gorm:"primaryKey;type:uuid"`
	RideID           string     `json:"ride_id" gorm:"type:uuid;index;uniqueIndex:idx_open_sos,where:resolved_at IS NULL,priority:1"`
	TriggeredBy      string     `json:"triggered_by" gorm:"type:uuid;index;uniqueIndex:idx_open_sos,where:resolved_at IS NULL,priority:2"`
	Role             string     `json:"role"`
	Status           string     `json:"status" gorm:"index"`
	Lat              float64    `json:"lat"`
	Lng              float64    `json:"lng"`
	Accuracy         float64    `json:"accuracy"`
	LastLat          float64    `json:"last_lat"`
	LastLng          float64    `json:"last_lng"`
	LastLocationAt   *time.Time `json:"last_location_at"`
	AssignedTo       *string    `json:"assigned_to" gorm:"type:uuid"`
	AcknowledgedAt   *time.Time `json:"acknowledged_at"`
	ResolvedAt       *time.Time `json:"resolved_at"`
	Outcome          string     `json:"outcome"`
	ResolutionNote   string     `json:"resolution_note"`
	EscalationLevel  int        `json:"escalation_level"`
	LastEscalatedAt  *time.Time `json:"last_escalated_at"`
	UserReportedSafe bool       `json:"user_reported_safe"`
	Silent           bool       `json:"silent"`
	CreatedAt        time.Time  `json:"created_at" gorm:"index"`
}
type SOSLocationPing struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid"`
	AlertID   string    `json:"alert_id" gorm:"type:uuid;index"`
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	Accuracy  float64   `json:"accuracy"`
	Speed     float64   `json:"speed"`
	At        time.Time `json:"at" gorm:"index"`
	CreatedAt time.Time `json:"created_at"`
}
type SOSEvent struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid"`
	AlertID   string    `json:"alert_id" gorm:"type:uuid;index"`
	Type      string    `json:"type"`
	By        string    `json:"by"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}
type ShareToken struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid"`
	RideID    string    `json:"ride_id" gorm:"type:uuid;index"`
	TokenHash string    `json:"-" gorm:"uniqueIndex"`
	ExpiresAt time.Time `json:"expires_at" gorm:"index"`
	CreatedBy string    `json:"created_by" gorm:"type:uuid;index"`
	CreatedAt time.Time `json:"created_at"`
}
type IncidentReport struct {
	ID             string    `json:"id" gorm:"primaryKey;type:uuid"`
	ReporterID     string    `json:"reporter_id" gorm:"type:uuid;index"`
	RideID         string    `json:"ride_id" gorm:"type:uuid;index"`
	Category       string    `json:"category" gorm:"index"`
	Severity       string    `json:"severity"`
	Description    string    `json:"description"`
	OccurredAt     time.Time `json:"occurred_at"`
	Location       string    `json:"location"`
	Status         string    `json:"status" gorm:"index"`
	AssignedTo     *string   `json:"assigned_to" gorm:"type:uuid"`
	ResolutionNote string    `json:"resolution_note"`
	CreatedAt      time.Time `json:"created_at"`
}
type IncidentAttachment struct {
	ID         string    `json:"id" gorm:"primaryKey;type:uuid"`
	IncidentID string    `json:"incident_id" gorm:"type:uuid;index"`
	UserID     string    `json:"-" gorm:"type:uuid;index"`
	FilePath   string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
}
type IncidentNote struct {
	ID         string    `json:"id" gorm:"primaryKey;type:uuid"`
	IncidentID string    `json:"incident_id" gorm:"type:uuid;index"`
	StaffID    string    `json:"staff_id" gorm:"type:uuid"`
	Note       string    `json:"note"`
	CreatedAt  time.Time `json:"created_at"`
}
