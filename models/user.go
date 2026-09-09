package models

import "time"

// Role represents a user role in the system.
type Role string

const (
	RoleMzee      Role = "mzee"
	RoleDriver    Role = "driver"
	RolePassenger Role = "passenger"
	RoleSupport   Role = "support"
	RoleAdmin     Role = "admin"
)

// AllRoles lists every supported role.
var AllRoles = []Role{RoleMzee, RoleDriver, RolePassenger, RoleSupport, RoleAdmin}

// IsValidRole reports whether r is a known role.
func IsValidRole(r Role) bool {
	switch r {
	case RoleMzee, RoleDriver, RolePassenger, RoleSupport, RoleAdmin:
		return true
	default:
		return false
	}
}

// NormalizeRole lowercases/validates a role string, defaulting to passenger.
func NormalizeRole(s string) Role {
	r := Role(s)
	if IsValidRole(r) {
		return r
	}
	// Accept common variants / Swahili mix-ups
	switch s {
	case "dereva", "drivr", "drivers":
		return RoleDriver
	case "abiria":
		return RolePassenger
	case "msaada":
		return RoleSupport
	case "mzee ":
		return RoleMzee
	default:
		return RolePassenger
	}
}

// User is the core account record.
type User struct {
	ID           string    `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	Name         string    `json:"name" gorm:"not null"`
	Email        string    `json:"email" gorm:"uniqueIndex;not null"`
	PasswordHash string    `json:"-" gorm:"column:password_hash;default:''"`
	Role         Role      `json:"role" gorm:"type:varchar(20);not null;default:'passenger'"`
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
	ID         string    `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
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
	Role     string `json:"role"` // optional: mzee|driver|passenger|support|admin (default passenger)
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
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  Role   `json:"role"`
}

func ToUserResponse(u *User) UserResponse {
	return UserResponse{ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role}
}

type GoogleUser struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"verified_email"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}
