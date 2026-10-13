package store

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"ride-backend/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---------- Staff Users ----------

func (s *Store) CreateStaffUser(
	name, email, passwordHash string,
	role models.StaffRole,
	createdBy *string,
	mustChangePassword bool,
) (*models.StaffUser, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var count int64
	if err := s.db.Model(&models.StaffUser{}).Where("email = ?", email).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, errors.New("staff email already registered")
	}

	now := time.Now()
	u := &models.StaffUser{
		ID:                 newID(),
		Name:               strings.TrimSpace(name),
		Email:              email,
		PasswordHash:       passwordHash,
		Role:               role,
		IsActive:           true,
		MustChangePassword: mustChangePassword,
		FailedAttempts:     0,
		CreatedBy:          createdBy,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	if err := s.db.Create(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) GetStaffUserByID(id string) (*models.StaffUser, bool) {
	var u models.StaffUser
	if err := s.db.First(&u, "id = ?", id).Error; err != nil {
		return nil, false
	}
	return &u, true
}

func (s *Store) GetStaffUserByEmail(email string) (*models.StaffUser, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	var u models.StaffUser
	if err := s.db.First(&u, "email = ?", email).Error; err != nil {
		return nil, false
	}
	return &u, true
}

func (s *Store) ListStaffUsers() []*models.StaffUser {
	var list []*models.StaffUser
	_ = s.db.Order("created_at ASC").Find(&list).Error
	return list
}

func (s *Store) UpdateStaffUser(id string, fn func(u *models.StaffUser)) (*models.StaffUser, error) {
	var u models.StaffUser
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", id).Error; err != nil {
			return err
		}
		before := u
		fn(&u)
		u.UpdatedAt = s.now()
		secret := func(p *string) string {
			if p == nil {
				return ""
			}
			return *p
		}
		changed := before.Role != u.Role || before.IsActive != u.IsActive || before.PasswordHash != u.PasswordHash || before.MustChangePassword != u.MustChangePassword || before.TOTPEnabled != u.TOTPEnabled || secret(before.TOTPSecretEnc) != secret(u.TOTPSecretEnc)
		if changed {
			u.AuthVersion++
			u.PendingTOTPSecretEnc = nil
			u.PendingTOTPExpiresAt = nil
		}
		if err := tx.Save(&u).Error; err != nil {
			return err
		}
		if changed {
			return revokeStaffRows(tx, id, s.now())
		}
		return nil
	})
	return &u, err
}

func (s *Store) CountActiveAdmins() int64 {
	var count int64
	_ = s.db.Model(&models.StaffUser{}).
		Where("role = ? AND is_active = ?", models.StaffRoleAdmin, true).
		Count(&count).Error
	return count
}

func (s *Store) IncrementFailedLogin(id string) (int, *time.Time, error) {
	var u models.StaffUser
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", id).Error; err != nil {
			return err
		}
		now := s.now()
		if u.LockedUntil != nil && !u.LockedUntil.After(now) {
			u.LockedUntil = nil
			u.FailedAttempts = 0
		}
		u.FailedAttempts++
		if u.FailedAttempts >= 5 {
			until := now.Add(15 * time.Minute)
			u.LockedUntil = &until
		}
		u.UpdatedAt = now
		return tx.Model(&u).Updates(map[string]any{"failed_attempts": u.FailedAttempts, "locked_until": u.LockedUntil, "updated_at": now}).Error
	})
	return u.FailedAttempts, u.LockedUntil, err
}

func (s *Store) ResetFailedLogin(id string) error {
	now := time.Now()
	return s.db.Model(&models.StaffUser{}).Where("id = ?", id).Updates(map[string]interface{}{
		"failed_attempts": 0,
		"locked_until":    nil,
		"last_login_at":   now,
		"updated_at":      now,
	}).Error
}

// ---------- Staff Sessions ----------

func (s *Store) CreateStaffSession(
	staffID, tokenHash, familyID, userAgent, ip string,
	expiresAt time.Time,
) (*models.StaffSession, error) {
	now := time.Now()
	sess := &models.StaffSession{
		ID:        newID(),
		StaffID:   staffID,
		TokenHash: tokenHash,
		FamilyID:  familyID,
		UserAgent: userAgent,
		IP:        ip,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}
	if err := s.db.Create(sess).Error; err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Store) GetStaffSessionByHash(tokenHash string) (*models.StaffSession, bool) {
	var sess models.StaffSession
	if err := s.db.First(&sess, "token_hash = ?", tokenHash).Error; err != nil {
		return nil, false
	}
	return &sess, true
}

func (s *Store) RotateStaffSession(
	oldSession *models.StaffSession,
	newTokenHash string,
	newExpiresAt time.Time,
	userAgent, ip string,
) (*models.StaffSession, error) {
	var newSess *models.StaffSession
	err := s.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		var user models.StaffUser
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", oldSession.StaffID).Error; err != nil {
			return err
		}
		var current models.StaffSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", oldSession.ID).Error; err != nil {
			return err
		}
		if current.RevokedAt != nil || current.ReplacedBy != nil || !current.ExpiresAt.After(now) || !user.IsActive {
			return ErrStaffSessionInvalid
		}
		newS := &models.StaffSession{
			ID:        newID(),
			StaffID:   oldSession.StaffID,
			TokenHash: newTokenHash,
			FamilyID:  oldSession.FamilyID,
			UserAgent: userAgent,
			IP:        ip,
			ExpiresAt: newExpiresAt,
			CreatedAt: now,
		}
		if err := tx.Create(newS).Error; err != nil {
			return err
		}

		result := tx.Model(&models.StaffSession{}).Where("id = ? AND revoked_at IS NULL AND replaced_by IS NULL", current.ID).Updates(map[string]any{"revoked_at": now, "replaced_by": newS.ID})
		if err := result.Error; err != nil {
			return err
		}
		if result.RowsAffected != 1 {
			return ErrStaffSessionInvalid
		}

		newSess = newS
		return nil
	})
	return newSess, err
}

func (s *Store) RevokeStaffSession(sessionID string) error {
	now := time.Now()
	return s.db.Model(&models.StaffSession{}).
		Where("id = ? AND revoked_at IS NULL", sessionID).
		Update("revoked_at", now).Error
}

func (s *Store) RevokeStaffSessionFamily(familyID string) error {
	now := time.Now()
	return s.db.Model(&models.StaffSession{}).
		Where("family_id = ? AND revoked_at IS NULL", familyID).
		Update("revoked_at", now).Error
}

func (s *Store) RevokeAllStaffSessions(staffID string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var staff models.StaffUser
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&staff, "id = ?", staffID).Error; err != nil {
			return err
		}
		if err := tx.Model(&staff).Update("auth_version", gorm.Expr("auth_version + 1")).Error; err != nil {
			return err
		}
		return revokeStaffRows(tx, staffID, s.now())
	})
}

// ---------- Staff Audit Logs ----------

func (s *Store) CreateAuditLog(
	actorID, action, targetType, targetID, ip string,
	meta interface{},
) error {
	metaJSON := "{}"
	if meta != nil {
		if b, err := json.Marshal(meta); err == nil {
			metaJSON = string(b)
		}
	}

	log := &models.StaffAuditLog{
		ID:         newID(),
		ActorID:    actorID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		IP:         ip,
		Meta:       metaJSON,
		CreatedAt:  time.Now(),
	}
	return s.db.Create(log).Error
}

func (s *Store) ListAuditLogs(limit, offset int) ([]*models.StaffAuditLog, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var logs []*models.StaffAuditLog
	var total int64
	if err := s.db.Model(&models.StaffAuditLog{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := s.db.Order("created_at DESC").Limit(limit).Offset(offset).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

// ---------- App Realm User helpers ----------

func (s *Store) DisableAppUser(id string) error {
	return s.db.Model(&models.User{}).Where("id = ?", id).Update("is_active", false).Error
}

func (s *Store) DB() *gorm.DB {
	return s.db
}
