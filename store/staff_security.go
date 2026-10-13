package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/models"
)

var ErrStaffSessionInvalid = errors.New("invalid staff session")

func (s *Store) StaffSessionActive(ctx context.Context, userID, sid string) error {
	var n int64
	err := s.db.WithContext(ctx).Model(&models.StaffSession{}).Where("staff_id = ? AND id = ? AND revoked_at IS NULL AND expires_at > ?", userID, sid, s.now()).Count(&n).Error
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaffSessionInvalid
	}
	return nil
}

func (s *Store) IssueStaffSession(ctx context.Context, expected *models.StaffUser, tokenHash, familyID, userAgent, ip string, expires time.Time) (*models.StaffSession, error) {
	var session *models.StaffSession
	err := s.WithContext(ctx).Transaction(func(tx *Store) error {
		var current models.StaffUser
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", expected.ID).Error; err != nil {
			return err
		}
		if !current.IsActive || current.AuthVersion != expected.AuthVersion || current.LockedUntil != nil && current.LockedUntil.After(s.now()) {
			return ErrStaffSessionInvalid
		}
		var err error
		session, err = tx.CreateStaffSession(expected.ID, tokenHash, familyID, userAgent, ip, expires)
		return err
	})
	return session, err
}

func (s *Store) SetPendingStaffTOTP(ctx context.Context, id string, version int, encrypted string) (*time.Time, error) {
	expires := s.now().Add(10 * time.Minute)
	r := s.db.WithContext(ctx).Model(&models.StaffUser{}).Where("id = ? AND auth_version = ? AND is_active = ?", id, version, true).Updates(map[string]any{"pending_totp_secret_enc": encrypted, "pending_totp_expires_at": expires})
	if r.Error != nil {
		return nil, r.Error
	}
	if r.RowsAffected != 1 {
		return nil, ErrStaffSessionInvalid
	}
	return &expires, nil
}

// ChangeStaffTOTP activates only the exact, unexpired pending secret verified
// by the caller, or disables current TOTP after credential proof. Other refresh
// sessions are revoked and the version invalidates all old bearer tokens.
func (s *Store) ChangeStaffTOTP(ctx context.Context, expected *models.StaffUser, currentSID, ip string, enable bool) (*models.StaffUser, error) {
	var updated models.StaffUser
	err := s.WithContext(ctx).Transaction(func(tx *Store) error {
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&updated, "id = ?", expected.ID).Error; err != nil {
			return err
		}
		if !updated.IsActive || updated.AuthVersion != expected.AuthVersion {
			return ErrStaffSessionInvalid
		}
		if enable {
			if updated.PendingTOTPSecretEnc == nil || expected.PendingTOTPSecretEnc == nil || *updated.PendingTOTPSecretEnc != *expected.PendingTOTPSecretEnc || updated.PendingTOTPExpiresAt == nil || !updated.PendingTOTPExpiresAt.After(s.now()) {
				return ErrStaffSessionInvalid
			}
			updated.TOTPSecretEnc = updated.PendingTOTPSecretEnc
		} else {
			updated.TOTPSecretEnc = nil
		}
		updated.TOTPEnabled = enable
		updated.PendingTOTPSecretEnc = nil
		updated.PendingTOTPExpiresAt = nil
		updated.AuthVersion++
		updated.UpdatedAt = s.now()
		if err := tx.db.Save(&updated).Error; err != nil {
			return err
		}
		q := tx.db.Model(&models.StaffSession{}).Where("staff_id = ? AND revoked_at IS NULL", updated.ID)
		if currentSID != "" {
			q = q.Where("id <> ?", currentSID)
		}
		if err := q.Update("revoked_at", s.now()).Error; err != nil {
			return err
		}
		action := "2fa_disabled"
		if enable {
			action = "2fa_enabled"
		}
		return tx.CreateAuditLog(updated.ID, action, "staff_user", updated.ID, ip, nil)
	})
	return &updated, err
}

func revokeStaffRows(tx *gorm.DB, id string, now time.Time) error {
	return tx.Model(&models.StaffSession{}).Where("staff_id = ? AND revoked_at IS NULL", id).Update("revoked_at", now).Error
}
