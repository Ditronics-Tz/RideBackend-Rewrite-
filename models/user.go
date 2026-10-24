package models

import (
	"fmt"
	"strings"
	"time"
)

// Role represents an app user role in the system (realm "app").
type Role string

const (
	RoleDriver    Role = "driver"
	RolePassenger Role = "passenger"
)

// AllRoles lists every supported app role.
var AllRoles = []Role{RoleDriver, RolePassenger}

// IsValidRole reports whether r is a known app role.
func IsValidRole(r Role) bool {
	switch r {
	case RoleDriver, RolePassenger:
		return true
	default:
		return false
	}
}

// ParseRole parses and validates an app role string strictly.
// Unknown role = error (which callers map to 400).
func ParseRole(s string) (Role, error) {
	clean := strings.ToLower(strings.TrimSpace(s))
	switch clean {
	case "driver", "dereva":
		return RoleDriver, nil
	case "passenger", "abiria":
		return RolePassenger, nil
	default:
		return "", fmt.Errorf("invalid app role %q: allowed roles are driver or passenger", s)
	}
}

// User is the core account record for realm "app".
type User struct {
	FailedLoginCount int        `json:"-" gorm:"not null;default:0"`
	LockedUntil      *time.Time `json:"-" gorm:"index"`
	ID               string     `json:"id" gorm:"primaryKey;type:uuid"`
	Name             string     `json:"name"`
	Email            string     `json:"email" gorm:"uniqueIndex;not null"`
	PasswordHash     string     `json:"-" gorm:"column:password_hash;default:''"`
	Role             Role       `json:"role" gorm:"type:varchar(20);not null;default:'passenger'"`
	IsActive         bool       `json:"is_active" gorm:"default:true"`
	Status           string     `json:"status" gorm:"not null;default:active;index"`
	SuspendedUntil   *time.Time `json:"suspended_until"`
	Phone            string     `json:"phone"`
	PhoneVerifiedAt  *time.Time `json:"phone_verified_at"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// DriverProfile holds driver-specific fields:
// licence, kadi ya gari (vehicle card), plate number + images.
type DriverProfile struct {
	ProfilePictureKey  string             `json:"-"`
	UserID             string             `json:"user_id" gorm:"primaryKey;type:uuid"`
	LicenceNumber      string             `json:"licence_number"`
	VehicleCardNumber  string             `json:"vehicle_card_number"` // kadi ya gari
	PlateNumber        string             `json:"plate_number"`
	VehicleType        string             `json:"vehicle_type" gorm:"size:50"`
	LicenceImageURL    string             `json:"licence_image_url"`
	ProfilePictureURL  string             `json:"profile_picture_url"`
	VerificationStatus VerificationStatus `json:"verification_status" gorm:"not null;default:pending_documents;index"`
	AssignedTo         *string            `json:"assigned_to" gorm:"type:uuid;index"`
	AssignedAt         *time.Time         `json:"assigned_at"`
	IsOnline           bool               `json:"is_online" gorm:"not null;default:false"`
	AverageRating      float64            `json:"average_rating" gorm:"default:0"`
	TotalRatings       int                `json:"total_ratings" gorm:"default:0"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

// PassengerProfile holds passenger-specific fields.
type PassengerProfile struct {
	ProfilePictureKey string    `json:"-"`
	UserID            string    `json:"user_id" gorm:"primaryKey;type:uuid"`
	FullName          string    `json:"full_name"`
	ProfileCompleted  bool      `json:"profile_completed" gorm:"-"`
	ProfilePictureURL string    `json:"profile_picture_url"`
	Phone             string    `json:"phone"`
	AverageRating     float64   `json:"average_rating" gorm:"default:0"`
	TotalRatings      int       `json:"total_ratings" gorm:"default:0"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// PublicDriverProfile deliberately excludes contact and compliance identifiers.
type PublicDriverProfile struct {
	UserID            string  `json:"user_id"`
	Name              string  `json:"name"`
	VehicleType       string  `json:"vehicle_type"`
	ProfilePictureURL string  `json:"profile_picture_url"`
	AverageRating     float64 `json:"average_rating"`
	TotalRatings      int     `json:"total_ratings"`
}

// PublicPassengerProfile excludes phone and other account-only fields.
type PublicPassengerProfile struct {
	UserID            string  `json:"user_id"`
	Name              string  `json:"name"`
	ProfilePictureURL string  `json:"profile_picture_url"`
	AverageRating     float64 `json:"average_rating"`
	TotalRatings      int     `json:"total_ratings"`
}

// Rating is a rating from one user to another (either direction).
type Rating struct {
	ID         string     `json:"id" gorm:"primaryKey;type:uuid"`
	RideID     *string    `json:"ride_id,omitempty" gorm:"type:uuid;uniqueIndex:idx_rating_ride_rater,where:ride_id IS NOT NULL,priority:1"`
	FromUserID string     `json:"from_user_id" gorm:"type:uuid;not null;index;uniqueIndex:idx_rating_ride_rater,where:ride_id IS NOT NULL,priority:2"`
	ToUserID   string     `json:"to_user_id" gorm:"type:uuid;not null;index"`
	Score      int        `json:"score" gorm:"not null"` // 1-5
	Comment    string     `json:"comment"`
	Hidden     bool       `json:"hidden" gorm:"-"`
	HiddenAt   *time.Time `json:"hidden_at,omitempty"`
	HiddenBy   *string    `json:"hidden_by,omitempty" gorm:"type:uuid"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ---------- Request inputs ----------

type RegisterInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role,omitempty"` // optional: driver|passenger (default passenger)
}

type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type UpdateRoleInput struct {
	Role string `json:"role"`
}

type DriverProfileInput struct {
	FullName          *string `json:"full_name"`
	PhoneNumber       *string `json:"phone_number"`
	LicenceNumber     string  `json:"licence_number"`
	VehicleCardNumber string  `json:"vehicle_card_number"`
	PlateNumber       string  `json:"plate_number"`
	VehicleType       string  `json:"vehicle_type"`
}

type PassengerProfileInput struct {
	FullName    *string `json:"full_name"`
	PhoneNumber *string `json:"phone_number"`
	Phone       *string `json:"phone"` // legacy alias
}

type RatingInput struct {
	ToUserID string `json:"to_user_id"`
	Score    int    `json:"score"`
	Comment  string `json:"comment"`
}

type UserResponse struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Email           string     `json:"email"`
	Role            Role       `json:"role"`
	IsActive        bool       `json:"is_active"`
	Status          string     `json:"status"`
	SuspendedUntil  *time.Time `json:"suspended_until"`
	Phone           string     `json:"phone"`
	PhoneVerifiedAt *time.Time `json:"phone_verified_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

func ToUserResponse(u *User) UserResponse {
	return UserResponse{
		ID:       u.ID,
		Name:     u.Name,
		Email:    u.Email,
		Role:     u.Role,
		IsActive: u.IsActive,
		Status:   u.Status, SuspendedUntil: u.SuspendedUntil, Phone: u.Phone, PhoneVerifiedAt: u.PhoneVerifiedAt, CreatedAt: u.CreatedAt,
	}
}

type GoogleUser struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"verified_email"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}
