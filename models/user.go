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
	ID           string    `json:"id" gorm:"primaryKey;type:uuid"`
	Name         string    `json:"name" gorm:"not null"`
	Email        string    `json:"email" gorm:"uniqueIndex;not null"`
	PasswordHash string    `json:"-" gorm:"column:password_hash;default:''"`
	Role         Role      `json:"role" gorm:"type:varchar(20);not null;default:'passenger'"`
	IsActive     bool      `json:"is_active" gorm:"default:true"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// DriverProfile holds driver-specific fields:
// licence, kadi ya gari (vehicle card), plate number + images.
type DriverProfile struct {
	UserID            string    `json:"user_id" gorm:"primaryKey;type:uuid"`
	LicenceNumber     string    `json:"licence_number"`
	VehicleCardNumber string    `json:"vehicle_card_number"` // kadi ya gari
	PlateNumber       string    `json:"plate_number"`
	LicenceImageURL   string    `json:"licence_image_url"`
	ProfilePictureURL string    `json:"profile_picture_url"`
	Verified          bool      `json:"verified" gorm:"default:false"`
	AverageRating     float64   `json:"average_rating" gorm:"default:0"`
	TotalRatings      int       `json:"total_ratings" gorm:"default:0"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// PassengerProfile holds passenger-specific fields.
type PassengerProfile struct {
	UserID            string    `json:"user_id" gorm:"primaryKey;type:uuid"`
	FullName          string    `json:"full_name"`
	ProfilePictureURL string    `json:"profile_picture_url"`
	Phone             string    `json:"phone"`
	AverageRating     float64   `json:"average_rating" gorm:"default:0"`
	TotalRatings      int       `json:"total_ratings" gorm:"default:0"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Rating is a rating from one user to another (either direction).
type Rating struct {
	ID         string    `json:"id" gorm:"primaryKey;type:uuid"`
	FromUserID string    `json:"from_user_id" gorm:"type:uuid;not null;index"`
	ToUserID   string    `json:"to_user_id" gorm:"type:uuid;not null;index"`
	Score      int       `json:"score" gorm:"not null"` // 1-5
	Comment    string    `json:"comment"`
	CreatedAt  time.Time `json:"created_at"`
}

// ---------- Request inputs ----------

type RegisterInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"` // optional: driver|passenger (default passenger)
}

type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type UpdateRoleInput struct {
	Role string `json:"role"`
}

type DriverProfileInput struct {
	LicenceNumber     string `json:"licence_number"`
	VehicleCardNumber string `json:"vehicle_card_number"`
	PlateNumber       string `json:"plate_number"`
}

type PassengerProfileInput struct {
	FullName string `json:"full_name"`
	Phone    string `json:"phone"`
}

type VerifyDriverInput struct {
	Verified bool `json:"verified"`
}

type RatingInput struct {
	ToUserID string `json:"to_user_id"`
	Score    int    `json:"score"`
	Comment  string `json:"comment"`
}

type UserResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     Role   `json:"role"`
	IsActive bool   `json:"is_active"`
}

func ToUserResponse(u *User) UserResponse {
	return UserResponse{
		ID:       u.ID,
		Name:     u.Name,
		Email:    u.Email,
		Role:     u.Role,
		IsActive: u.IsActive,
	}
}

type GoogleUser struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"verified_email"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}
