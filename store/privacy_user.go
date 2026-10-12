package store

import (
	"time"

	"gorm.io/gorm"
	"ride-backend/models"
)

func init() {
	RegisterDataExporter(func(s *Store) DataExporter { return userExporter{s} })
	RegisterAnonymizer(userAnonymizer{})
}

type userExporter struct{ s *Store }

func (userExporter) Name() string     { return "account" }
func (userExporter) Tables() []string { return []string{"users"} }
func (e userExporter) Export(id string) (any, []string, error) {
	u, ok := e.s.GetUserByID(id)
	if !ok {
		return nil, nil, gorm.ErrRecordNotFound
	}
	return map[string]any{"id": u.ID, "name": u.Name, "email": u.Email, "phone": u.Phone, "role": u.Role, "status": u.Status, "created_at": u.CreatedAt, "phone_verified_at": u.PhoneVerifiedAt}, nil, nil
}

type userAnonymizer struct{}

func (userAnonymizer) Name() string { return "users" }
func (userAnonymizer) Tables() []string {
	return []string{"users", "user_status_events", "support_notes", "phone_challenges"}
}
func (userAnonymizer) Description() string {
	return "clears direct identifiers and credentials; retains account UUID, role and status history with generic reasons"
}
func (userAnonymizer) Anonymize(tx *gorm.DB, id string) error {
	var u models.User
	if err := tx.First(&u, "id = ?", id).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := tx.Model(&models.User{}).Where("id = ?", id).Updates(map[string]any{"name": "Deleted user", "email": "deleted+" + id + "@deleted.invalid", "password_hash": "", "phone": "", "phone_verified_at": nil, "status": "deleted", "is_active": false, "suspended_until": nil, "deleted_at": now, "updated_at": now}).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.UserStatusEvent{}).Where("user_id = ?", id).Update("reason", "Account data anonymised").Error; err != nil {
		return err
	}
	if err := tx.Model(&models.UserStatusEvent{}).Where("actor_id = ?", id).Update("actor_id", nil).Error; err != nil {
		return err
	}
	if err := tx.Where("user_id = ?", id).Delete(&models.SupportNote{}).Error; err != nil {
		return err
	}
	return tx.Where("user_id = ?", id).Delete(&models.PhoneChallenge{}).Error
}
