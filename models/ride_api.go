package models

import "time"

const UnassignedRideDriverID = "00000000-0000-0000-0000-000000000000"

type RidePoint struct {
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	Address string  `json:"address"`
}

type CreateRideInput struct {
	Pickup      RidePoint `json:"pickup"`
	Dropoff     RidePoint `json:"dropoff"`
	VehicleType string    `json:"vehicle_type"`
}

type RideParty struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type RideEventResponse struct {
	Type      string         `json:"type"`
	ActorID   *string        `json:"actor_id,omitempty"`
	ActorType string         `json:"actor_type"`
	At        time.Time      `json:"at"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type RideResponse struct {
	ID            string              `json:"id"`
	Status        string              `json:"status"`
	Passenger     RideParty           `json:"passenger"`
	Driver        *RideParty          `json:"driver"`
	Pickup        RidePoint           `json:"pickup"`
	Dropoff       RidePoint           `json:"dropoff"`
	VehicleType   string              `json:"vehicle_type"`
	EstimatedFare int64               `json:"estimated_fare"`
	FinalFare     *int64              `json:"final_fare,omitempty"`
	CancelReason  string              `json:"cancel_reason,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
	AcceptedAt    *time.Time          `json:"accepted_at,omitempty"`
	StartedAt     *time.Time          `json:"started_at,omitempty"`
	CompletedAt   *time.Time          `json:"completed_at,omitempty"`
	CancelledAt   *time.Time          `json:"cancelled_at,omitempty"`
	Events        []RideEventResponse `json:"events"`
}

type RideQuote struct {
	EstimatedFareTZS         int64  `json:"estimated_fare_tzs"`
	EstimatedDistanceMeters  int64  `json:"estimated_distance_meters"`
	EstimatedDurationSeconds int64  `json:"estimated_duration_seconds"`
	DistanceBasis            string `json:"distance_basis"`
	Currency                 string `json:"currency"`
	PaymentMethod            string `json:"payment_method"`
}
