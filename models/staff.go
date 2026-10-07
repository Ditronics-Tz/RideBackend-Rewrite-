package models

import (
	"fmt"
	"strings"
	"time"
)

// StaffRole represents an administrative or support role (realm "staff").
type StaffRole string

const (
	StaffRoleAdmin   StaffRole = "admin"
	StaffRoleSupport StaffRole = "support"
)

var AllStaffRoles = []StaffRole{StaffRoleAdmin, StaffRoleSupport}

func IsValidStaffRole(r StaffRole) bool {
	switch r {
	case StaffRoleAdmin, StaffRoleSupport:
		return true
	default:
		return false
	}
}

func ParseStaffRole(s string) (StaffRole, error) {
	clean := strings.ToLower(strings.TrimSpace(s))
	switch clean {
	case "admin":
		return StaffRoleAdmin, nil
	case "support":
		return StaffRoleSupport, nil
	default:
		return "", fmt.Errorf("invalid staff role %q: allowed roles are admin or support", s)
	}
}

// StaffUser represents an admin or support member.
type StaffUser struct {
	ID                 string     `json:"id" gorm:"primaryKey;type:uuid"`
	Name               string     `json:"name" gorm:"not null"`
	Email              string     `json:"email" gorm:"uniqueIndex;not null"`
	PasswordHash       string     `json:"-" gorm:"column:password_hash;not null"`
	Role               StaffRole  `json:"role" gorm:"type:varchar(20);not null"`
	IsActive           bool       `json:"is_active" gorm:"not null;default:true"`
	MustChangePassword bool       `json:"must_change_password" gorm:"not null"`
	FailedAttempts     int        `json:"failed_attempts" gorm:"not null;default:0"`
	LockedUntil        *time.Time `json:"locked_until,omitempty" gorm:"index"`
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"`
	TOTPSecretEnc      *string    `json:"-" gorm:"column:totp_secret_enc"`
	TOTPEnabled        bool       `json:"totp_enabled" gorm:"not null;default:false"`
	CreatedBy          *string    `json:"created_by,omitempty" gorm:"type:uuid"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func (s *StaffUser) TableName() string {
	return "staff_users"
}

// StaffSession represents a refresh token session for a staff member.
type StaffSession struct {
	ID         string     `json:"id" gorm:"primaryKey;type:uuid"`
	StaffID    string     `json:"staff_id" gorm:"type:uuid;not null;index"`
	TokenHash  string     `json:"token_hash" gorm:"type:varchar(64);not null;index"`
	FamilyID   string     `json:"family_id" gorm:"type:uuid;not null;index"`
	UserAgent  string     `json:"user_agent"`
	IP         string     `json:"ip"`
	ExpiresAt  time.Time  `json:"expires_at" gorm:"not null;index"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty" gorm:"index"`
	ReplacedBy *string    `json:"replaced_by,omitempty" gorm:"type:uuid"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (s *StaffSession) TableName() string {
	return "staff_sessions"
}

// StaffAuditLog logs high-security actions in the staff realm.
type StaffAuditLog struct {
	ID         string    `json:"id" gorm:"primaryKey;type:uuid"`
	ActorID    string    `json:"actor_id" gorm:"type:varchar(64);not null;index"`
	Action     string    `json:"action" gorm:"type:varchar(100);not null;index"`
	TargetType string    `json:"target_type" gorm:"type:varchar(50);not null"`
	TargetID   string    `json:"target_id" gorm:"type:varchar(64);not null;index"`
	IP         string    `json:"ip"`
	Meta       string    `json:"meta" gorm:"type:jsonb"`
	CreatedAt  time.Time `json:"created_at" gorm:"index"`
}

func (s *StaffAuditLog) TableName() string {
	return "staff_audit_logs"
}

// StaffUserResponse is safe to return to clients.
type StaffUserResponse struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Email              string     `json:"email"`
	Role               StaffRole  `json:"role"`
	IsActive           bool       `json:"is_active"`
	MustChangePassword bool       `json:"must_change_password"`
	TOTPEnabled        bool       `json:"totp_enabled"`
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func ToStaffUserResponse(u *StaffUser) StaffUserResponse {
	return StaffUserResponse{
		ID:                 u.ID,
		Name:               u.Name,
		Email:              u.Email,
		Role:               u.Role,
		IsActive:           u.IsActive,
		MustChangePassword: u.MustChangePassword,
		TOTPEnabled:        u.TOTPEnabled,
		LastLoginAt:        u.LastLoginAt,
		CreatedAt:          u.CreatedAt,
		UpdatedAt:          u.UpdatedAt,
	}
}

// ---------- Request Inputs ----------

type StaffLoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

type StaffRefreshInput struct {
	RefreshToken string `json:"refresh_token"`
}

type StaffChangePasswordInput struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type Staff2FAVerifyInput struct {
	Code string `json:"code"`
}

type Staff2FADisableInput struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

type CreateStaffInput struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

type UpdateStaffInput struct {
	Name     *string `json:"name"`
	IsActive *bool   `json:"is_active"`
}

type UpdateStaffRoleInput struct {
	Role string `json:"role"`
}
