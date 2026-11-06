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
	Completed int64     `json:"completed,omitempty"`
	ID        string    `gorm:"type:uuid;primaryKey" json:"id"`
	DriverID  string    `gorm:"type:uuid;index" json:"-"`
	Pickup    RidePoint `gorm:"embedded;embeddedPrefix:pickup_" json:"pickup"`
	Dropoff   RidePoint `gorm:"embedded;embeddedPrefix:dropoff_" json:"dropoff"`
	Vehicle   string    `json:"vehicle"`
	Capacity  int       `json:"capacity"`
	Seats     int       `json:"seats"`
	Price     int64     `json:"price"`
	Fee       int64     `json:"fee"`
	Status    string    `json:"status"`
	Deadline  int64     `json:"deadline"`
	Created   int64     `json:"created"`
}

type SharedSeat struct {
	ID          string `gorm:"type:uuid;primaryKey" json:"id"`
	TripID      string `gorm:"type:uuid;index" json:"trip_id"`
	PassengerID string `gorm:"type:uuid;index" json:"-"`
	Status      string `json:"status"`
	Expires     int64  `json:"expires"`
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
}

type SharedView struct {
	SharedTrip
	Available  int          `json:"available"`
	Held       int          `json:"held"`
	Confirmed  int          `json:"confirmed"`
	ServerTime int64        `json:"server_time"`
	Manifest   []SharedSeat `json:"manifest,omitempty"`
	MySeat     *SharedSeat  `json:"my_seat,omitempty"`
	PIN        string       `json:"pin,omitempty"`
}
