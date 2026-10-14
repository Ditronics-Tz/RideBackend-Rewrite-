package models

import "time"

// SavedPlace belongs to one passenger. Home and work are single slots; custom
// places may be added independently.
type SavedPlace struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid"`
	UserID    string    `json:"-" gorm:"type:uuid;not null;index"`
	Kind      string    `json:"kind" gorm:"size:16;not null"`
	Label     string    `json:"label" gorm:"size:80;not null"`
	Address   string    `json:"address" gorm:"size:250;not null"`
	Lat       float64   `json:"lat" gorm:"type:numeric;not null"`
	Lng       float64   `json:"lng" gorm:"type:numeric;not null"`
	CreatedAt time.Time `json:"created_at" gorm:"not null"`
	UpdatedAt time.Time `json:"updated_at" gorm:"not null"`
}

type SavedPlaceInput struct {
	Kind    string  `json:"kind"`
	Label   string  `json:"label"`
	Address string  `json:"address"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
}

// PassengerPreferences contains the values shown in the passenger settings
// screens. Defaults are returned without creating a row on first read.
type PassengerPreferences struct {
	UserID          string    `json:"-" gorm:"primaryKey;type:uuid"`
	QuietRide       bool      `json:"quiet_ride" gorm:"not null"`
	ExtraPickupTime bool      `json:"extra_pickup_time" gorm:"not null"`
	ShareTripStatus bool      `json:"share_trip_status" gorm:"not null"`
	PreciseLocation bool      `json:"precise_location" gorm:"not null"`
	RideUpdates     bool      `json:"ride_updates" gorm:"not null"`
	TripMessages    bool      `json:"trip_messages" gorm:"not null"`
	Promotions      bool      `json:"promotions" gorm:"not null"`
	EmailEnabled    bool      `json:"email_enabled" gorm:"not null"`
	SMSEnabled      bool      `json:"sms_enabled" gorm:"not null"`
	PushEnabled     bool      `json:"push_enabled" gorm:"not null"`
	Language        string    `json:"language" gorm:"size:8;not null"`
	UpdatedAt       time.Time `json:"updated_at" gorm:"not null"`
}

type ScheduledRide struct {
	ID             string     `json:"id" gorm:"primaryKey;type:uuid"`
	PassengerID    string     `json:"-" gorm:"type:uuid;not null;index;uniqueIndex:idx_scheduled_request,priority:1"`
	PickupAddress  string     `json:"-" gorm:"size:250;not null"`
	PickupLat      float64    `json:"-" gorm:"type:numeric;not null"`
	PickupLng      float64    `json:"-" gorm:"type:numeric;not null"`
	DropoffAddress string     `json:"-" gorm:"size:250;not null"`
	DropoffLat     float64    `json:"-" gorm:"type:numeric;not null"`
	DropoffLng     float64    `json:"-" gorm:"type:numeric;not null"`
	VehicleType    string     `json:"vehicle_type" gorm:"size:50;not null"`
	ScheduledFor   time.Time  `json:"scheduled_for" gorm:"not null;index"`
	Status         string     `json:"status" gorm:"size:24;not null;index"`
	RideID         *string    `json:"ride_id,omitempty" gorm:"type:uuid"`
	FailureCode    string     `json:"failure_code,omitempty" gorm:"size:40;not null"`
	RequestKey     string     `json:"-" gorm:"size:100;not null;uniqueIndex:idx_scheduled_request,priority:2"`
	RequestHash    string     `json:"-" gorm:"size:64;not null"`
	ClaimedUntil   *time.Time `json:"-"`
	AttemptCount   int        `json:"-" gorm:"not null"`
	CreatedAt      time.Time  `json:"created_at" gorm:"not null"`
	UpdatedAt      time.Time  `json:"updated_at" gorm:"not null"`
}

type ScheduledRideInput struct {
	Pickup       RidePoint `json:"pickup"`
	Dropoff      RidePoint `json:"dropoff"`
	VehicleType  string    `json:"vehicle_type"`
	ScheduledFor time.Time `json:"scheduled_for"`
}

type ScheduledRideResponse struct {
	ID           string    `json:"id"`
	Pickup       RidePoint `json:"pickup"`
	Dropoff      RidePoint `json:"dropoff"`
	VehicleType  string    `json:"vehicle_type"`
	ScheduledFor time.Time `json:"scheduled_for"`
	Status       string    `json:"status"`
	RideID       *string   `json:"ride_id,omitempty"`
	FailureCode  string    `json:"failure_code,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

func (r ScheduledRide) Response() ScheduledRideResponse {
	return ScheduledRideResponse{
		ID:          r.ID,
		Pickup:      RidePoint{Lat: r.PickupLat, Lng: r.PickupLng, Address: r.PickupAddress},
		Dropoff:     RidePoint{Lat: r.DropoffLat, Lng: r.DropoffLng, Address: r.DropoffAddress},
		VehicleType: r.VehicleType, ScheduledFor: r.ScheduledFor, Status: r.Status,
		RideID: r.RideID, FailureCode: r.FailureCode, CreatedAt: r.CreatedAt,
	}
}
