package store

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"ride-backend/models"

	"gorm.io/gorm"
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
	u, ok := s.GetStaffUserByID(id)
	if !ok {
		return nil, errors.New("staff user not found")
	}
	fn(u)
	u.UpdatedAt = time.Now()
	if err := s.db.Save(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) CountActiveAdmins() int64 {
	var count int64
	_ = s.db.Model(&models.StaffUser{}).
		Where("role = ? AND is_active = ?", models.StaffRoleAdmin, true).
		Count(&count).Error
	return count
}

func (s *Store) IncrementFailedLogin(id string) (int, *time.Time, error) {
	u, ok := s.GetStaffUserByID(id)
	if !ok {
		return 0, nil, errors.New("staff user not found")
	}

	u.FailedAttempts++
	var lockedUntil *time.Time
	if u.FailedAttempts >= 5 {
		t := time.Now().Add(15 * time.Minute)
		lockedUntil = &t
		u.LockedUntil = lockedUntil
	}
	u.UpdatedAt = time.Now()

	if err := s.db.Save(u).Error; err != nil {
		return 0, nil, err
	}
	return u.FailedAttempts, lockedUntil, nil
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

		oldSession.RevokedAt = &now
		oldSession.ReplacedBy = &newS.ID
		if err := tx.Save(oldSession).Error; err != nil {
			return err
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
	now := time.Now()
	return s.db.Model(&models.StaffSession{}).
		Where("staff_id = ? AND revoked_at IS NULL", staffID).
		Update("revoked_at", now).Error
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
