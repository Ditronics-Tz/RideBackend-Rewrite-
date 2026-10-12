package models

import "time"

type VerificationStatus string

const (
	PendingDocuments VerificationStatus = "pending_documents"
	Submitted        VerificationStatus = "submitted"
	UnderReview      VerificationStatus = "under_review"
	Approved         VerificationStatus = "approved"
	Rejected         VerificationStatus = "rejected"
	Suspended        VerificationStatus = "suspended"
	Expired          VerificationStatus = "expired"
)

// Four required uploads plus optional insurance are Rida's onboarding policy.
var RequiredDocumentTypes = []string{"driving_licence", "vehicle_card", "national_id", "vehicle_photo"}
var RejectionCodes = []string{"blurry_image", "expired", "name_mismatch", "number_mismatch", "wrong_document", "incomplete_document", "suspected_fraud", "other"}

func ValidDocumentType(value string) bool {
	if value == "insurance" {
		return true
	}
	for _, item := range RequiredDocumentTypes {
		if item == value {
			return true
		}
	}
	return false
}
func ValidRejectionCode(value string) bool {
	for _, item := range RejectionCodes {
		if item == value {
			return true
		}
	}
	return false
}
func RequiresExpiry(kind string) bool { return kind == "driving_licence" || kind == "insurance" }

// Replacement uploads create new rows. Old files and decisions remain addressable.
type DriverDocument struct {
	ID            string     `json:"id" gorm:"primaryKey;type:uuid"`
	DriverID      string     `json:"driver_id" gorm:"type:uuid;not null;index:idx_document_driver_type,priority:1"`
	Type          string     `json:"type" gorm:"not null;index:idx_document_driver_type,priority:2"`
	FilePath      string     `json:"-" gorm:"not null"`
	FileURL       string     `json:"file_url"`
	Status        string     `json:"status" gorm:"not null;default:submitted"`
	IsCurrent     bool       `json:"is_current" gorm:"not null"`
	RejectionCode string     `json:"rejection_code"`
	RejectionNote string     `json:"rejection_note"`
	ExpiresAt     *time.Time `json:"expires_at" gorm:"index"`
	ReviewedBy    *string    `json:"reviewed_by" gorm:"type:uuid"`
	ReviewedAt    *time.Time `json:"reviewed_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

type VerificationReview struct {
	ID           string    `json:"id" gorm:"primaryKey;type:uuid"`
	DriverID     string    `json:"driver_id" gorm:"type:uuid;not null;index:idx_review_driver_time,priority:1"`
	DocumentID   *string   `json:"document_id" gorm:"type:uuid;index"`
	ReviewerID   *string   `json:"reviewer_id" gorm:"type:uuid"`
	ReviewerName string    `json:"reviewer_name"`
	Decision     string    `json:"decision" gorm:"not null"`
	ReasonCode   string    `json:"reason_code"`
	Note         string    `json:"note"`
	CreatedAt    time.Time `json:"created_at" gorm:"index:idx_review_driver_time,priority:2"`
}

type DriverDuplicateFlag struct {
	ID              string    `json:"id" gorm:"primaryKey;type:uuid"`
	DriverID        string    `json:"driver_id" gorm:"type:uuid;index;uniqueIndex:idx_duplicate,priority:1"`
	MatchedDriverID string    `json:"matched_driver_id" gorm:"type:uuid;index;uniqueIndex:idx_duplicate,priority:2"`
	Field           string    `json:"field" gorm:"uniqueIndex:idx_duplicate,priority:3"`
	Value           string    `json:"value" gorm:"uniqueIndex:idx_duplicate,priority:4"`
	CreatedAt       time.Time `json:"created_at"`
}

type DriverNotification struct {
	ID         string    `json:"id" gorm:"primaryKey;type:uuid"`
	DriverID   string    `json:"driver_id" gorm:"type:uuid;not null;index"`
	DocumentID string    `json:"document_id" gorm:"type:uuid;uniqueIndex"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
}

type VerificationDetail struct {
	User           *UserResponse         `json:"user,omitempty"`
	Profile        *DriverProfile        `json:"profile"`
	Documents      []DriverDocument      `json:"documents"`
	Reviews        []VerificationReview  `json:"reviews"`
	DuplicateFlags []DriverDuplicateFlag `json:"duplicate_flags,omitempty"`
	RequiredTypes  []string              `json:"required_types"`
	RejectionCodes []string              `json:"rejection_codes"`
}
