package store

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/models"
	"strings"
	"time"
)

// ActiveAccount is used for every authenticated app request, not just token issuance.
func (s *Store) ActiveAccount(id string) (*models.User, error) {
	var u models.User
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", id).Error; err != nil {
			return err
		}
		if u.Status == "suspended" && u.SuspendedUntil != nil && !u.SuspendedUntil.After(time.Now()) {
			from := u.Status
			u.Status = "active"
			u.IsActive = true
			u.SuspendedUntil = nil
			if err := tx.Save(&u).Error; err != nil {
				return err
			}
			if err := tx.Create(&models.UserStatusEvent{ID: newID(), UserID: id, FromStatus: from, ToStatus: "active", Reason: "Temporary suspension elapsed", CreatedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if u.DeletedAt != nil || u.Status == "deleted" || !u.IsActive || (u.Status != "active" && u.Status != "") {
		return nil, conflict("account is suspended or banned")
	}
	return &u, nil
}

// AccountForRequest performs an uncached account-state read so deletion and
// suspension transitions take effect immediately for already-issued JWTs.
func (s *Store) AccountForRequest(id string) (*models.User, error) {
	var u models.User
	if err := s.db.First(&u, "id = ?", id).Error; err != nil {
		return nil, err
	}
	if u.Status == "pending_deletion" || u.Status == "deleted" || u.DeletedAt != nil {
		return &u, nil
	}
	return s.ActiveAccount(id)
}

func (s *Store) ChangeAccountStatus(id string, actor *models.StaffUser, status, reason string, until *time.Time) error {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 2000 {
		return invalid("a reason of at most 2000 characters is required")
	}
	if status != "active" && status != "suspended" && status != "banned" {
		return invalid("invalid account status")
	}
	if status == "banned" && actor.Role != models.StaffRoleAdmin {
		return conflict("only an admin can permanently ban an account")
	}
	if status == "suspended" && actor.Role == models.StaffRoleSupport {
		if until == nil {
			end := s.now().Add(7 * 24 * time.Hour)
			until = &end
		} else if until.After(s.now().Add(7 * 24 * time.Hour)) {
			return invalid("support suspensions cannot exceed 7 days")
		}
	}
	if until != nil && (status != "suspended" || !until.After(time.Now())) {
		return invalid("until must be a future timestamp for a suspension")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var u models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", id).Error; err != nil {
			return err
		}
		if u.Status == "banned" && actor.Role != models.StaffRoleAdmin {
			return conflict("only an admin can change a banned account")
		}
		if u.Status == status {
			return conflict("account already has this status")
		}
		event := models.UserStatusEvent{ID: newID(), UserID: id, ActorID: &actor.ID, FromStatus: u.Status, ToStatus: status, Reason: reason, Until: until, CreatedAt: time.Now().UTC()}
		if err := tx.Create(&event).Error; err != nil {
			return err
		}
		if err := tx.Model(&u).Updates(map[string]interface{}{"status": status, "is_active": status == "active", "suspended_until": until}).Error; err != nil {
			return err
		}
		// Suspension is an account restriction. Retain document approvals for reactivation.
		if status != "active" {
			return tx.Model(&models.DriverProfile{}).Where("user_id = ?", id).Update("is_online", false).Error
		}
		return nil
	})
}

func (s *Store) AddSupportNote(id string, actor *models.StaffUser, body string) (*models.SupportNote, error) {
	body = strings.TrimSpace(body)
	if body == "" || len(body) > 4000 {
		return nil, invalid("note must contain 1 to 4000 characters")
	}
	if _, ok := s.GetUserByID(id); !ok {
		return nil, gorm.ErrRecordNotFound
	}
	n := &models.SupportNote{ID: newID(), UserID: id, AuthorID: actor.ID, AuthorName: actor.Name, Body: body, CreatedAt: time.Now().UTC()}
	return n, s.db.Create(n).Error
}
