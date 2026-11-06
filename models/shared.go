package models

// TransportSettings are versioned so concurrent administrators cannot overwrite each other.
type TransportSettings struct {
	ID          int   `gorm:"primaryKey;autoIncrement:false" json:"-"`
	Version     int   `json:"version"`
	Enabled     bool  `json:"enabled"`
	SharedFee   int64 `json:"shared_fee"`
	HailPercent int   `json:"hail_percent"`
}

type SharedTrip struct {
	Version    int64     `json:"version"`
	FeeVersion int       `json:"fee_version"`
	Completed  int64     `json:"completed,omitempty"`
	ID         string    `gorm:"type:uuid;primaryKey" json:"id"`
	DriverID   string    `gorm:"type:uuid;index" json:"-"`
	Pickup     RidePoint `gorm:"embedded;embeddedPrefix:pickup_" json:"pickup"`
	Dropoff    RidePoint `gorm:"embedded;embeddedPrefix:dropoff_" json:"dropoff"`
	Vehicle    string    `json:"vehicle"`
	Capacity   int       `json:"capacity"`
	Seats      int       `json:"seats"`
	Price      int64     `json:"price"`
	Fee        int64     `json:"fee"`
	Status     string    `json:"status"`
	Deadline   int64     `json:"deadline"`
	Created    int64     `json:"created"`
}

type SharedSeat struct {
	ID          string `gorm:"type:uuid;primaryKey" json:"id"`
	TripID      string `gorm:"type:uuid;index" json:"trip_id"`
	PassengerID string `gorm:"type:uuid;index" json:"-"`
	Status      string `json:"status"`
	Expires     int64  `json:"expires"`
	Created     int64  `json:"created"`
	ConfirmedAt int64  `json:"confirmed_at,omitempty"`
	CompletedAt int64  `json:"completed_at,omitempty"`
	Failures    int    `json:"-"`
}

type SharedRequest struct {
	Actor  string `gorm:"type:uuid;primaryKey"`
	Key    string `gorm:"primaryKey"`
	Hash   string
	TripID string `gorm:"type:uuid"`
}

type SharedPublish struct {
	Pickup      RidePoint `json:"pickup"`
	Dropoff     RidePoint `json:"dropoff"`
	Capacity    int       `json:"capacity"`
	Seats       int       `json:"seats"`
	Price       int64     `json:"price"`
	WaitMinutes int       `json:"wait_minutes"`
}

type SharedAction struct {
	SeatID string `json:"seat_id"`
	PIN    string `json:"pin"`
	Reason string `json:"reason,omitempty"`
}

type SharedView struct {
	SharedTrip
	Available      int                  `json:"available"`
	Held           int                  `json:"held"`
	Confirmed      int                  `json:"confirmed"`
	ServerTime     int64                `json:"server_time"`
	Manifest       []SharedSeat         `json:"manifest,omitempty"`
	MySeat         *SharedSeat          `json:"my_seat,omitempty"`
	PIN            string               `json:"pin,omitempty"`
	Receipt        *TransportAssessment `json:"receipt,omitempty"`
	DriverName     string               `json:"driver_name,omitempty"`
	DriverRating   float64              `json:"driver_rating,omitempty"`
	PassengerNames map[string]string    `gorm:"-" json:"passenger_names,omitempty"`
}

// TransportCursor serializes event allocation with commit, so reconnect cursors
// cannot skip a lower sequence that committed after a higher sequence.
type TransportCursor struct {
	ID       int `gorm:"primaryKey;autoIncrement:false"`
	Revision int64
}

// TransportEvent is a durable invalidation outbox, read directly by long polls.
// Delivery is pull-based and replayable; it never stores PINs or contact details.
type TransportEvent struct {
	ID      int64  `gorm:"primaryKey;autoIncrement:false" json:"id"`
	TripID  string `gorm:"type:uuid;index" json:"trip_id"`
	Version int64  `json:"version"`
	Type    string `json:"type"`
	Reason  string `json:"-"`
	ActorID string `gorm:"type:uuid" json:"-"`
	At      int64  `json:"at"`
}

type TransportAssessment struct {
	Mode       string `gorm:"primaryKey" json:"mode"`
	TripID     string `gorm:"type:uuid;primaryKey" json:"trip_id"`
	DriverID   string `gorm:"type:uuid;index" json:"-"`
	Gross      int64  `json:"gross"`
	Fee        int64  `json:"fee"`
	Net        int64  `json:"net"`
	Passengers int    `json:"passengers"`
	FeeVersion int    `json:"fee_version"`
	Status     string `gorm:"index" json:"status"`
	Created    int64  `json:"created"`
	Updated    int64  `json:"updated"`
}
