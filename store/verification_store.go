package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/models"
)

var ErrVerificationConflict = errors.New("verification conflict")
var ErrVerificationInvalid = errors.New("invalid verification request")

func invalid(message string) error  { return fmt.Errorf("%w: %s", ErrVerificationInvalid, message) }
func conflict(message string) error { return fmt.Errorf("%w: %s", ErrVerificationConflict, message) }

func lockDriver(tx *gorm.DB, id string) (*models.DriverProfile, error) {
	var p models.DriverProfile
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, "user_id = ?", id).Error; err != nil {
		return nil, err
	}
	var user models.User
	if err := tx.First(&user, "id = ? AND role = ?", id, models.RoleDriver).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func currentDocuments(tx *gorm.DB, id string) ([]models.DriverDocument, error) {
	docs := []models.DriverDocument{}
	err := tx.Where("driver_id = ? AND is_current = ?", id, true).Order("type ASC").Find(&docs).Error
	return docs, err
}

func completeDocuments(p *models.DriverProfile, docs []models.DriverDocument, now time.Time, approved bool) error {
	if p.LicenceNumber == "" || p.PlateNumber == "" || p.VehicleCardNumber == "" {
		return invalid("licence number, vehicle card number and plate number are required")
	}
	for _, kind := range models.RequiredDocumentTypes {
		found := false
		for _, doc := range docs {
			if doc.Type != kind {
				continue
			}
			found = true
			if models.RequiresExpiry(kind) && doc.ExpiresAt == nil {
				return invalid(kind + " requires an expiry date")
			}
			if doc.ExpiresAt != nil && !doc.ExpiresAt.After(now) {
				return invalid(kind + " has expired")
			}
			if doc.Status == "rejected" || doc.Status == "expired" {
				return invalid(kind + " must be replaced")
			}
			if approved && doc.Status != "approved" {
				return invalid(kind + " is not approved")
			}
		}
		if !found {
			return invalid("missing " + kind)
		}
	}
	return nil
}

func appendReview(tx *gorm.DB, driverID string, docID *string, actor *models.StaffUser, decision, code, note string) error {
	r := models.VerificationReview{ID: newID(), DriverID: driverID, DocumentID: docID, Decision: decision, ReasonCode: code, Note: note, CreatedAt: time.Now().UTC()}
	if actor != nil {
		r.ReviewerID = &actor.ID
		r.ReviewerName = actor.Name
	}
	return tx.Create(&r).Error
}

func (s *Store) VerificationDetail(id string, staff bool) (*models.VerificationDetail, error) {
	// Enforce expiry at read time as well as in the scheduled sweep.
	if err := s.ExpireDriverDocuments(time.Now().UTC(), id); err != nil {
		return nil, err
	}
	p, ok := s.GetDriverProfile(id)
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	docs, err := currentDocuments(s.db, id)
	if err != nil {
		return nil, err
	}
	reviews := []models.VerificationReview{}
	if err := s.db.Where("driver_id = ?", id).Order("created_at DESC, id DESC").Find(&reviews).Error; err != nil {
		return nil, err
	}
	result := &models.VerificationDetail{Profile: p, Documents: docs, Reviews: reviews, RequiredTypes: models.RequiredDocumentTypes, RejectionCodes: models.RejectionCodes}
	if u, ok := s.GetUserByID(id); ok {
		response := models.ToUserResponse(u)
		result.User = &response
	}
	if staff {
		result.DuplicateFlags = []models.DriverDuplicateFlag{}
		if err := s.db.Where("driver_id = ? OR matched_driver_id = ?", id, id).Order("created_at DESC").Find(&result.DuplicateFlags).Error; err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Store) AddDriverDocument(id, kind, filePath string, expires *time.Time) (*models.DriverDocument, error) {
	if !models.ValidDocumentType(kind) {
		return nil, invalid("unknown document type")
	}
	if models.RequiresExpiry(kind) && expires == nil {
		return nil, invalid("expiry date is required")
	}
	if expires != nil && !expires.After(time.Now()) {
		return nil, invalid("expiry date must be in the future")
	}
	doc := &models.DriverDocument{ID: newID(), DriverID: id, Type: kind, FilePath: filePath, Status: "submitted", IsCurrent: true, ExpiresAt: expires, CreatedAt: time.Now().UTC()}
	doc.FileURL = "/api/v1/files/documents/" + doc.ID
	err := s.db.Transaction(func(tx *gorm.DB) error {
		p, err := lockDriver(tx, id)
		if err != nil {
			return err
		}
		if p.AssignedTo != nil {
			return conflict("review is claimed; wait for the reviewer to finish or release it")
		}
		if err := tx.Model(&models.DriverDocument{}).Where("driver_id = ? AND type = ? AND is_current = ?", id, kind, true).Update("is_current", false).Error; err != nil {
			return err
		}
		if err := tx.Create(doc).Error; err != nil {
			return err
		}
		if p.VerificationStatus != models.Suspended {
			p.VerificationStatus = models.PendingDocuments
		}
		p.IsOnline = false
		if err := tx.Save(p).Error; err != nil {
			return err
		}
		return appendReview(tx, id, &doc.ID, nil, "uploaded", "", kind)
	})
	return doc, err
}

func (s *Store) SubmitDriverDocuments(id string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		p, err := lockDriver(tx, id)
		if err != nil {
			return err
		}
		if p.AssignedTo != nil || p.VerificationStatus == models.Suspended || p.VerificationStatus == models.Approved {
			return conflict("driver cannot submit in the current state")
		}
		docs, err := currentDocuments(tx, id)
		if err != nil {
			return err
		}
		if err := completeDocuments(p, docs, time.Now(), false); err != nil {
			return err
		}
		p.VerificationStatus = models.Submitted
		p.IsOnline = false
		if err := tx.Save(p).Error; err != nil {
			return err
		}
		return appendReview(tx, id, nil, nil, "submitted", "", "")
	})
}

func (s *Store) ClaimDriver(id string, actor *models.StaffUser, release bool) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		p, err := lockDriver(tx, id)
		if err != nil {
			return err
		}
		if release {
			if p.AssignedTo == nil {
				return conflict("review is not claimed")
			}
			if *p.AssignedTo != actor.ID && actor.Role != models.StaffRoleAdmin {
				return conflict("another reviewer owns this claim")
			}
			p.AssignedTo = nil
			p.AssignedAt = nil
			p.VerificationStatus = models.Submitted
			if err := tx.Save(p).Error; err != nil {
				return err
			}
			return appendReview(tx, id, nil, actor, "released", "", "")
		}
		if p.AssignedTo != nil {
			if *p.AssignedTo == actor.ID {
				return nil
			}
			return conflict("driver is already claimed by another reviewer")
		}
		if p.VerificationStatus != models.Submitted {
			return conflict("only submitted drivers can be claimed")
		}
		now := time.Now().UTC()
		p.AssignedTo = &actor.ID
		p.AssignedAt = &now
		p.VerificationStatus = models.UnderReview
		if err := tx.Save(p).Error; err != nil {
			return err
		}
		return appendReview(tx, id, nil, actor, "claimed", "", "")
	})
}

func (s *Store) ReviewDriverDocument(driverID, docID string, actor *models.StaffUser, decision, code, note string) error {
	if decision != "approved" && decision != "rejected" {
		return invalid("decision must be approved or rejected")
	}
	if len(note) > 2000 {
		return invalid("note must not exceed 2000 characters")
	}
	if decision == "rejected" && (!models.ValidRejectionCode(code) || strings.TrimSpace(note) == "") {
		return invalid("rejection requires a valid reason code and a note")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		p, err := lockDriver(tx, driverID)
		if err != nil {
			return err
		}
		var doc models.DriverDocument
		if err := tx.First(&doc, "id = ? AND driver_id = ? AND is_current = ?", docID, driverID, true).Error; err != nil {
			return err
		}
		override := doc.Status == "rejected" || doc.Status == "approved"
		if override {
			if p.AssignedTo != nil && *p.AssignedTo != actor.ID {
				return conflict("release the existing claim before overriding a decision")
			}
			if actor.Role != models.StaffRoleAdmin || strings.TrimSpace(note) == "" {
				return conflict("only an admin can override or revoke a document decision, with a reason")
			}
			if doc.Status == decision {
				return conflict("document already has this decision")
			}
			p.VerificationStatus = models.UnderReview
			p.AssignedTo = &actor.ID
			p.IsOnline = false
		} else if doc.Status != "submitted" {
			return conflict("replace expired documents before reviewing")
		}
		if p.VerificationStatus != models.UnderReview || p.AssignedTo == nil || *p.AssignedTo != actor.ID {
			return conflict("claim this driver before reviewing documents")
		}
		if decision == "approved" && doc.ExpiresAt != nil && !doc.ExpiresAt.After(time.Now()) {
			return invalid("cannot approve an expired document")
		}
		now := time.Now().UTC()
		doc.Status = decision
		doc.ReviewedBy = &actor.ID
		doc.ReviewedAt = &now
		doc.RejectionCode = ""
		doc.RejectionNote = ""
		if decision == "rejected" {
			doc.RejectionCode = code
			doc.RejectionNote = strings.TrimSpace(note)
		}
		if err := tx.Save(&doc).Error; err != nil {
			return err
		}
		if err := appendReview(tx, driverID, &doc.ID, actor, decision, doc.RejectionCode, strings.TrimSpace(note)); err != nil {
			return err
		}
		docs, err := currentDocuments(tx, driverID)
		if err != nil {
			return err
		}
		pending, rejected := false, false
		for _, d := range docs {
			if d.Status == "submitted" {
				pending = true
			}
			if d.Status == "rejected" || d.Status == "expired" {
				rejected = true
			}
		}
		if !pending && rejected {
			p.VerificationStatus = models.Rejected
			p.AssignedTo = nil
			p.AssignedAt = nil
			if err := tx.Save(p).Error; err != nil {
				return err
			}
			return appendReview(tx, driverID, nil, actor, string(p.VerificationStatus), "", "All current documents reviewed")
		}
		if override {
			p.IsOnline = false
			return tx.Save(p).Error
		}
		return nil
	})
}

func (s *Store) ApproveDriver(id string, actor *models.StaffUser) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		p, err := lockDriver(tx, id)
		if err != nil {
			return err
		}
		if p.VerificationStatus != models.UnderReview || p.AssignedTo == nil || *p.AssignedTo != actor.ID {
			return conflict("claim this driver before final approval")
		}
		var u models.User
		if err := tx.First(&u, "id = ?", id).Error; err != nil {
			return err
		}
		if !u.IsActive || u.Status != "active" {
			return conflict("account must be active before final approval")
		}
		docs, err := currentDocuments(tx, id)
		if err != nil {
			return err
		}
		if err := completeDocuments(p, docs, time.Now(), true); err != nil {
			return err
		}
		for _, d := range docs {
			if d.Status != "approved" {
				return invalid("review every uploaded document before final approval")
			}
		}
		p.VerificationStatus = models.Approved
		p.AssignedTo = nil
		p.AssignedAt = nil
		if err := tx.Save(p).Error; err != nil {
			return err
		}
		return appendReview(tx, id, nil, actor, "approved", "", "Final approval after document review")
	})
}

func (s *Store) SuspendDriverVerification(id string, actor *models.StaffUser, suspend bool, note string) error {
	if strings.TrimSpace(note) == "" || len(note) > 2000 {
		return invalid("a reason of at most 2000 characters is required")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		p, err := lockDriver(tx, id)
		if err != nil {
			return err
		}
		if suspend {
			p.VerificationStatus = models.Suspended
		} else {
			if p.VerificationStatus != models.Suspended {
				return conflict("driver is not suspended")
			}
			// Reinstatement requires a fresh submission and claim, even with approved documents.
			p.VerificationStatus = models.PendingDocuments
			if err := tx.Model(&models.DriverDocument{}).Where("driver_id = ? AND is_current = ? AND status = ?", id, true, "approved").Updates(map[string]interface{}{"status": "submitted", "reviewed_by": nil, "reviewed_at": nil}).Error; err != nil {
				return err
			}
		}
		p.AssignedTo = nil
		p.AssignedAt = nil
		p.IsOnline = false
		if err := tx.Save(p).Error; err != nil {
			return err
		}
		decision := "suspended"
		if !suspend {
			decision = "reinstated"
		}
		return appendReview(tx, id, nil, actor, decision, "", strings.TrimSpace(note))
	})
}

func CanonicalIdentifier(value string) string {
	return strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "\t", "", "\n", "", "\r", "").Replace(strings.TrimSpace(value)))
}

func (s *Store) UpdateDriverIdentifiers(id string, input models.DriverProfileInput) (*models.DriverProfile, error) {
	return s.updateDriverIdentifiers(id, input, true)
}

func (s *Store) updateDriverIdentifiers(id string, input models.DriverProfileInput, retry bool) (*models.DriverProfile, error) {
	values := map[string]string{"licence_number": CanonicalIdentifier(input.LicenceNumber), "plate_number": CanonicalIdentifier(input.PlateNumber)}
	duplicates := []models.DriverDuplicateFlag{}
	for field, value := range values {
		if value == "" {
			continue
		}
		var matches []models.DriverProfile
		if err := s.db.Where(field+" = ? AND user_id <> ?", value, id).Find(&matches).Error; err != nil {
			return nil, err
		}
		for _, match := range matches {
			duplicates = append(duplicates, models.DriverDuplicateFlag{ID: newID(), DriverID: id, MatchedDriverID: match.UserID, Field: field, Value: value, CreatedAt: time.Now().UTC()})
		}
	}
	if len(duplicates) > 0 {
		if err := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&duplicates).Error; err != nil {
			return nil, err
		}
		return nil, conflict("licence or plate number already belongs to another account; flagged for staff review")
	}
	var result *models.DriverProfile
	err := s.db.Transaction(func(tx *gorm.DB) error {
		p, err := lockDriver(tx, id)
		if err != nil {
			return err
		}
		if p.AssignedTo != nil {
			return conflict("profile cannot change during a claimed review")
		}
		changed := false
		if v := values["licence_number"]; v != "" && v != p.LicenceNumber {
			p.LicenceNumber = v
			changed = true
		}
		if v := values["plate_number"]; v != "" && v != p.PlateNumber {
			p.PlateNumber = v
			changed = true
		}
		if v := strings.TrimSpace(input.VehicleCardNumber); v != "" && v != p.VehicleCardNumber {
			p.VehicleCardNumber = v
			changed = true
		}
		if changed {
			if p.VerificationStatus != models.Suspended {
				p.VerificationStatus = models.PendingDocuments
			}
			p.IsOnline = false
			// Identity changes invalidate existing document approvals.
			if err := tx.Model(&models.DriverDocument{}).Where("driver_id = ? AND is_current = ? AND status = ?", id, true, "approved").Updates(map[string]interface{}{"status": "submitted", "reviewed_by": nil, "reviewed_at": nil}).Error; err != nil {
				return err
			}
			if err := appendReview(tx, id, nil, nil, "identifiers_changed", "", "Identity fields changed; document approvals require review"); err != nil {
				return err
			}
		}
		if err := tx.Save(p).Error; err != nil {
			return err
		}
		result = p
		return nil
	})
	if err != nil && (strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(err.Error(), "23505")) {
		// A concurrent submission may win after the friendly precheck.
		if retry {
			return s.updateDriverIdentifiers(id, input, false)
		}
		return nil, conflict("licence or plate number already exists")
	}
	return result, err
}

func (s *Store) ExpireDriverDocuments(now time.Time, driverID string) error {
	query := s.db.Model(&models.DriverDocument{}).Where("is_current = ? AND expires_at <= ? AND status <> ?", true, now, "expired")
	if driverID != "" {
		query = query.Where("driver_id = ?", driverID)
	}
	var ids []string
	if err := query.Distinct("driver_id").Pluck("driver_id", &ids).Error; err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.db.Transaction(func(tx *gorm.DB) error {
			p := &models.DriverProfile{}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(p, "user_id = ?", id).Error; err != nil {
				return err
			}
			var docs []models.DriverDocument
			if err := tx.Where("driver_id = ? AND is_current = ? AND expires_at <= ? AND status <> ?", id, true, now, "expired").Find(&docs).Error; err != nil {
				return err
			}
			for _, doc := range docs {
				if err := tx.Model(&doc).Update("status", "expired").Error; err != nil {
					return err
				}
				if err := appendReview(tx, id, &doc.ID, nil, "expired", "expired", doc.Type+" expired"); err != nil {
					return err
				}
				n := models.DriverNotification{ID: newID(), DriverID: id, DocumentID: doc.ID, Message: doc.Type + " has expired. Upload a replacement to resume driving.", CreatedAt: now}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&n).Error; err != nil {
					return err
				}
			}
			if len(docs) > 0 {
				if p.VerificationStatus != models.Suspended {
					p.VerificationStatus = models.Expired
				}
				p.IsOnline = false
				p.AssignedTo = nil
				p.AssignedAt = nil
				return tx.Save(p).Error
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SetDriverOnline(id string, online bool) error {
	if err := s.ExpireDriverDocuments(time.Now().UTC(), id); err != nil {
		return err
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		p, err := lockDriver(tx, id)
		if err != nil {
			return err
		}
		if online {
			var u models.User
			if err := tx.First(&u, "id = ?", id).Error; err != nil {
				return err
			}
			if !u.IsActive || u.Status != "active" || p.VerificationStatus != models.Approved {
				return invalid("only active approved drivers can go online or accept rides")
			}
			docs, err := currentDocuments(tx, id)
			if err != nil {
				return err
			}
			if err := completeDocuments(p, docs, time.Now(), true); err != nil {
				return err
			}
		}
		return tx.Model(p).Update("is_online", online).Error
	})
}

// RequireDriverApproval is shared by dispatch operations when rides are implemented.
func (s *Store) RequireDriverApproval(id string) error {
	detail, err := s.VerificationDetail(id, false)
	if err != nil {
		return err
	}
	u, ok := s.GetUserByID(id)
	if !ok || !u.IsActive || u.Status != "active" || u.Role != models.RoleDriver || detail.Profile.VerificationStatus != models.Approved {
		return invalid("driver approval is required")
	}
	return completeDocuments(detail.Profile, detail.Documents, time.Now(), true)
}
