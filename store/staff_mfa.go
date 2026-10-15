package store

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/models"
)

const (
	MFAPurposeVerify = "verify"
	MFAPurposeEnroll = "enroll"
	mfaMaxFailures   = 5
)

var (
	ErrMFAExpired = errors.New("mfa challenge expired")
	ErrMFAInvalid = errors.New("mfa challenge invalid")
	ErrMFAReplay  = errors.New("mfa code replayed")
	ErrMFALocked  = errors.New("mfa challenge locked")
)

func (s *Store) CreateStaffMFAChallenge(ctx context.Context, staffID, purpose string, expires time.Time) (*models.StaffMFAChallenge, error) {
	row := &models.StaffMFAChallenge{
		ID:        newID(),
		StaffID:   staffID,
		Purpose:   purpose,
		ExpiresAt: expires,
		CreatedAt: s.now(),
	}
	if err := s.db.WithContext(ctx).Create(row).Error; err != nil {
		return nil, err
	}
	return row, nil
}

// PendingOrSetStaffTOTP keeps one unexpired pending secret. A concurrent
// enrollment sees the secret already stored instead of replacing it.
func (s *Store) PendingOrSetStaffTOTP(ctx context.Context, id string, version int, candidate string) (string, *time.Time, error) {
	var stored string
	var expires *time.Time
	err := s.WithContext(ctx).Transaction(func(tx *Store) error {
		var current models.StaffUser
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", id).Error; err != nil {
			return err
		}
		if !current.IsActive || current.AuthVersion != version {
			return ErrStaffSessionInvalid
		}
		if current.PendingTOTPSecretEnc != nil && current.PendingTOTPExpiresAt != nil && current.PendingTOTPExpiresAt.After(s.now()) {
			stored = *current.PendingTOTPSecretEnc
			expires = current.PendingTOTPExpiresAt
			return nil
		}
		exp := s.now().Add(10 * time.Minute)
		current.PendingTOTPSecretEnc = &candidate
		current.PendingTOTPExpiresAt = &exp
		current.UpdatedAt = s.now()
		if err := tx.db.Model(&current).Updates(map[string]any{
			"pending_totp_secret_enc": candidate,
			"pending_totp_expires_at": exp,
			"updated_at":              current.UpdatedAt,
		}).Error; err != nil {
			return err
		}
		stored = candidate
		expires = &exp
		return nil
	})
	return stored, expires, err
}

func (s *Store) RecordAcceptedTOTPStep(ctx context.Context, id string, step int64) error {
	res := s.db.WithContext(ctx).Model(&models.StaffUser{}).
		Where("id = ? AND (last_accepted_totp_step IS NULL OR last_accepted_totp_step < ?)", id, step).
		Update("last_accepted_totp_step", step)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrMFAReplay
	}
	return nil
}

func (s *Store) loadMFAChallenge(tx *gorm.DB, id, staffID, purpose string) (*models.StaffMFAChallenge, error) {
	var row models.StaffMFAChallenge
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMFAInvalid
		}
		return nil, err
	}
	if row.StaffID != staffID || row.Purpose != purpose || row.ConsumedAt != nil {
		return nil, ErrMFAInvalid
	}
	if !row.ExpiresAt.After(s.now()) {
		return nil, ErrMFAExpired
	}
	if row.FailedAttempts >= mfaMaxFailures {
		return nil, ErrMFALocked
	}
	return &row, nil
}

func (s *Store) FailStaffMFAChallenge(ctx context.Context, id, staffID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row models.StaffMFAChallenge
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ? AND staff_id = ?", id, staffID).Error; err != nil {
			return ErrMFAInvalid
		}
		if row.ConsumedAt != nil {
			return ErrMFAInvalid
		}
		row.FailedAttempts++
		now := s.now()
		updates := map[string]any{"failed_attempts": row.FailedAttempts}
		if row.FailedAttempts >= mfaMaxFailures || !row.ExpiresAt.After(now) {
			updates["consumed_at"] = now
		}
		return tx.Model(&row).Updates(updates).Error
	})
}

// ConsumeStaffMFAChallenge burns a challenge after the caller has checked the code.
func (s *Store) ConsumeStaffMFAChallenge(ctx context.Context, id, staffID, purpose string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := s.loadMFAChallenge(tx, id, staffID, purpose); err != nil {
			return err
		}
		now := s.now()
		res := tx.Model(&models.StaffMFAChallenge{}).Where("id = ? AND consumed_at IS NULL", id).Update("consumed_at", now)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrMFAInvalid
		}
		return nil
	})
}

func (s *Store) CountUnusedRecoveryCodes(ctx context.Context, staffID string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&models.StaffMFARecoveryCode{}).Where("staff_id = ? AND used_at IS NULL", staffID).Count(&n).Error
	return n, err
}

func (s *Store) ReplaceStaffRecoveryCodes(ctx context.Context, staffID string, hashes []string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("staff_id = ?", staffID).Delete(&models.StaffMFARecoveryCode{}).Error; err != nil {
			return err
		}
		if len(hashes) == 0 {
			return nil
		}
		now := s.now()
		rows := make([]models.StaffMFARecoveryCode, len(hashes))
		for i, hash := range hashes {
			rows[i] = models.StaffMFARecoveryCode{ID: newID(), StaffID: staffID, CodeHash: hash, CreatedAt: now}
		}
		return tx.Create(&rows).Error
	})
}

// RedeemStaffRecoveryCode marks one unused code used. Comparison walks every
// unused hash so the response does not depend on which code matched.
func (s *Store) RedeemStaffRecoveryCode(ctx context.Context, staffID, codeHash string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []models.StaffMFARecoveryCode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("staff_id = ? AND used_at IS NULL", staffID).Find(&rows).Error; err != nil {
			return err
		}
		given := []byte(codeHash)
		var matched *models.StaffMFARecoveryCode
		for i := range rows {
			if subtle.ConstantTimeCompare([]byte(rows[i].CodeHash), given) == 1 {
				copy := rows[i]
				matched = &copy
			}
		}
		if matched == nil {
			return ErrMFAInvalid
		}
		now := s.now()
		res := tx.Model(&models.StaffMFARecoveryCode{}).Where("id = ? AND used_at IS NULL", matched.ID).Update("used_at", now)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrMFAInvalid
		}
		return nil
	})
}

func (s *Store) ClearStaffMFA(ctx context.Context, id string) (*models.StaffUser, error) {
	var updated models.StaffUser
	err := s.WithContext(ctx).Transaction(func(tx *Store) error {
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&updated, "id = ?", id).Error; err != nil {
			return err
		}
		updated.TOTPEnabled = false
		updated.TOTPSecretEnc = nil
		updated.PendingTOTPSecretEnc = nil
		updated.PendingTOTPExpiresAt = nil
		updated.LastAcceptedTOTPStep = nil
		updated.TOTPVerifiedAt = nil
		updated.AuthVersion++
		updated.UpdatedAt = s.now()
		if err := tx.db.Save(&updated).Error; err != nil {
			return err
		}
		if err := tx.db.Where("staff_id = ?", id).Delete(&models.StaffMFARecoveryCode{}).Error; err != nil {
			return err
		}
		now := s.now()
		if err := tx.db.Model(&models.StaffMFAChallenge{}).Where("staff_id = ? AND consumed_at IS NULL", id).Update("consumed_at", now).Error; err != nil {
			return err
		}
		return revokeStaffRows(tx.db, id, now)
	})
	return &updated, err
}

func (s *Store) ListStaffMFAAudit(ctx context.Context, staffID string, limit int) ([]models.StaffAuditLog, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	actions := []string{
		"login_success", "login_failure", "2fa_enabled", "2fa_setup_pending",
		"mfa_challenge_issued", "mfa_verify_failure", "mfa_recovery_used",
		"mfa_recovery_regenerated", "mfa_reset", "mfa_refresh_blocked",
	}
	var rows []models.StaffAuditLog
	err := s.db.WithContext(ctx).Where("actor_id = ? AND action IN ?", staffID, actions).Order("created_at DESC").Limit(limit).Find(&rows).Error
	return rows, err
}
