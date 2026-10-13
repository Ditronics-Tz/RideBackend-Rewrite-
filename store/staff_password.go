package store

import (
	"context"
	"gorm.io/gorm/clause"
	"ride-backend/models"
)

func (s *Store) ChangeStaffPassword(ctx context.Context, expected *models.StaffUser, hash, ip string) error {
	return s.WithContext(ctx).Transaction(func(tx *Store) error {
		var current models.StaffUser
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", expected.ID).Error; err != nil {
			return err
		}
		if !current.IsActive || current.AuthVersion != expected.AuthVersion {
			return ErrStaffSessionInvalid
		}
		current.PasswordHash = hash
		current.MustChangePassword = false
		current.AuthVersion++
		current.PendingTOTPSecretEnc = nil
		current.PendingTOTPExpiresAt = nil
		current.UpdatedAt = s.now()
		if err := tx.db.Save(&current).Error; err != nil {
			return err
		}
		if err := revokeStaffRows(tx.db, current.ID, s.now()); err != nil {
			return err
		}
		return tx.CreateAuditLog(current.ID, "password_change", "staff_user", current.ID, ip, nil)
	})
}
