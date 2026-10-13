package models

import "time"

// Ride is the authoritative integration record. Dispatch is deliberately a
// separate module; never populate these fields from a payment/report request.
type Ride struct {
	// Optional authoritative route polyline as JSON [[lat,lng],...]; never accepted from payment requests.
	RouteGeometry    string     `json:"-"`
	AcceptedAt       *time.Time `json:"accepted_at"`
	CancelledBy      *string    `json:"cancelled_by" gorm:"type:uuid"`
	ID               string     `json:"id" gorm:"primaryKey;type:uuid"`
	PassengerID      string     `json:"passenger_id" gorm:"type:uuid;index;not null;uniqueIndex:idx_active_passenger_ride,where: status <> 'completed' AND status <> 'cancelled' AND status <> 'cancelled_by_passenger' AND status <> 'cancelled_by_driver' AND status <> 'cancelled_by_admin' AND status <> 'no_driver_found' AND status <> 'expired'"`
	DriverID         string     `json:"driver_id" gorm:"type:uuid;index;not null;uniqueIndex:idx_active_driver_ride,where: driver_id <> '00000000-0000-0000-0000-000000000000' AND status <> 'completed' AND status <> 'cancelled' AND status <> 'cancelled_by_passenger' AND status <> 'cancelled_by_driver' AND status <> 'cancelled_by_admin' AND status <> 'no_driver_found' AND status <> 'expired'"`
	Status           string     `json:"status" gorm:"index;not null"`
	Fare             int64      `json:"fare"`
	EstimatedFare    int64      `json:"estimated_fare"`
	VehicleType      string     `json:"vehicle_type"`
	Pickup           string     `json:"pickup"`
	Dropoff          string     `json:"dropoff"`
	PickupLat        float64    `json:"pickup_lat"`
	PickupLng        float64    `json:"pickup_lng"`
	DropoffLat       float64    `json:"dropoff_lat"`
	DropoffLng       float64    `json:"dropoff_lng"`
	ExpectedDistance float64    `json:"expected_distance_m"`
	LastLat          float64    `json:"last_lat"`
	LastLng          float64    `json:"last_lng"`
	LastLocationAt   *time.Time `json:"last_location_at"`
	StartedAt        *time.Time `json:"started_at"`
	CompletedAt      *time.Time `json:"completed_at"`
	CancelledAt      *time.Time `json:"cancelled_at"`
	OfferExpiresAt   *time.Time `json:"offer_expires_at"`
	CancelReason     string     `json:"cancel_reason"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type RideEvent struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid"`
	RideID    string    `json:"ride_id" gorm:"type:uuid;not null;index:idx_ride_events_timeline,priority:1"`
	Type      string    `json:"type" gorm:"size:64;not null"`
	ActorID   *string   `json:"actor_id,omitempty" gorm:"type:uuid;index"`
	ActorType string    `json:"actor_type" gorm:"size:16;not null"`
	At        time.Time `json:"at" gorm:"not null;index:idx_ride_events_timeline,priority:2"`
	Meta      string    `json:"meta,omitempty" gorm:"type:text"`
}

type DriverLocation struct {
	DriverID  string    `json:"driver_id" gorm:"primaryKey;type:uuid"`
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	Accuracy  float64   `json:"accuracy"`
	UpdatedAt time.Time `json:"updated_at" gorm:"index"`
}

type RideIdempotencyKey struct {
	ActorID     string    `json:"-" gorm:"primaryKey;type:uuid"`
	Key         string    `json:"-" gorm:"primaryKey;size:100"`
	Operation   string    `json:"-" gorm:"size:32;not null"`
	RequestHash string    `json:"-" gorm:"size:64;not null"`
	RideID      string    `json:"-" gorm:"type:uuid;not null"`
	CreatedAt   time.Time `json:"-" gorm:"not null;index"`
}
type UserBlock struct {
	BlockerID string    `json:"blocker_id" gorm:"primaryKey;type:uuid"`
	BlockedID string    `json:"blocked_id" gorm:"primaryKey;type:uuid;index"`
	CreatedAt time.Time `json:"created_at"`
}
type Report struct {
	ID                string     `json:"id" gorm:"primaryKey;type:uuid"`
	ReporterID        string     `json:"reporter_id" gorm:"type:uuid;index;uniqueIndex:idx_report_pair_ride,priority:1"`
	ReportedID        string     `json:"reported_id" gorm:"type:uuid;index;uniqueIndex:idx_report_pair_ride,priority:2"`
	RideID            string     `json:"ride_id" gorm:"type:uuid;index;uniqueIndex:idx_report_pair_ride,priority:3"`
	Category          string     `json:"category" gorm:"index"`
	Description       string     `json:"description"`
	Status            string     `json:"status" gorm:"index"`
	Priority          string     `json:"priority" gorm:"index"`
	AssignedTo        *string    `json:"assigned_to" gorm:"type:uuid;index"`
	ResolutionOutcome string     `json:"resolution_outcome"`
	ResolutionNote    string     `json:"resolution_note"`
	ResolvedBy        *string    `json:"resolved_by" gorm:"type:uuid"`
	ResolvedAt        *time.Time `json:"resolved_at"`
	CreatedAt         time.Time  `json:"created_at" gorm:"index"`
}
type ReportNote struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid"`
	ReportID  string    `json:"report_id" gorm:"type:uuid;index"`
	StaffID   string    `json:"staff_id" gorm:"type:uuid"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}
type AccountFlag struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid"`
	UserID    string    `json:"user_id" gorm:"type:uuid;index;uniqueIndex:idx_open_account_flag,where:status = 'open',priority:1"`
	Reason    string    `json:"reason"`
	Source    string    `json:"source" gorm:"uniqueIndex:idx_open_account_flag,where:status = 'open',priority:2"`
	Status    string    `json:"status" gorm:"index"`
	CreatedAt time.Time `json:"created_at"`
}

// Outbox stores delivery intent in the same transaction as the domain action.
type NotificationDelivery struct {
	SOSAlertID *string    `json:"-" gorm:"type:uuid;index"`
	ID         string     `json:"id" gorm:"primaryKey;type:uuid"`
	UserID     string     `json:"-" gorm:"index"`
	Template   string     `json:"template"`
	Language   string     `json:"language"`
	Phone      string     `json:"-"`
	Link       string     `json:"-"`
	Status     string     `json:"status" gorm:"index"`
	Attempts   int        `json:"attempts"`
	ClaimedAt  *time.Time `json:"claimed_at"`
	CreatedAt  time.Time  `json:"created_at"`
}
