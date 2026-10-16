package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/models"
)

const AppRefreshTTL = 90 * 24 * time.Hour
const AppRefreshAbsoluteTTL = 365 * 24 * time.Hour

var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrAuthUnavailable = errors.New("authentication temporarily unavailable")

type SessionDevice struct{ DeviceID, Platform, IP string }
type IssuedAppSession struct {
	Session      *models.AppSession
	RefreshToken string `json:"-"`
}

func (s *Store) sessionLifetimes() (time.Duration, time.Duration) {
	if s.Policy == nil {
		return AppRefreshTTL, AppRefreshAbsoluteTTL
	}
	inactive, absolute := s.Policy.AppRefreshInactivityTTL, s.Policy.AppRefreshAbsoluteTTL
	if inactive == 0 {
		inactive = AppRefreshTTL
	}
	if absolute == 0 {
		absolute = AppRefreshAbsoluteTTL
	}
	return inactive, absolute
}

func refreshCredential() (string, string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", "", ErrAuthUnavailable
	}
	raw := base64.RawURLEncoding.EncodeToString(random[:])
	hash, _ := hashRefresh(raw)
	return raw, hash, nil
}

func hashRefresh(raw string) (string, error) {
	if len(raw) != 43 {
		return "", ErrInvalidCredentials
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != raw {
		return "", ErrInvalidCredentials
	}
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:]), nil
}

func sessionDevice(device SessionDevice) (SessionDevice, error) {
	device.DeviceID = strings.TrimSpace(device.DeviceID)
	device.Platform = strings.ToLower(strings.TrimSpace(device.Platform))
	for _, field := range []struct {
		name, value string
		max         int
	}{{"device_id", device.DeviceID, 128}, {"platform", device.Platform, 32}} {
		if len(field.value) > field.max || !utf8.ValidString(field.value) || strings.IndexFunc(field.value, unicode.IsControl) >= 0 {
			return device, FieldError(field.name, "invalid session device metadata")
		}
	}
	if device.IP != "" {
		ip := net.ParseIP(device.IP)
		if ip == nil {
			return device, FieldError("ip", "invalid session address")
		}
		device.IP = ip.String()
	}
	return device, nil
}

// Pending deletion keeps credential-based access to the recovery routes only.
// Middleware applies its existing allowlist after this session-state check.
func authActive(user *models.User) bool {
	return user.DeletedAt == nil && models.IsValidRole(user.Role) && (user.IsActive && (user.Status == "active" || user.Status == "") || user.Status == "pending_deletion")
}

func lockAuthUser(tx *gorm.DB, userID string) (*models.User, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return nil, ErrInvalidCredentials
	}
	var user models.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, ErrAuthUnavailable
	}
	return &user, nil
}

func (s *Store) IssueAppSession(ctx context.Context, userID string, device SessionDevice) (*IssuedAppSession, error) {
	device, err := sessionDevice(device)
	if err != nil {
		return nil, err
	}
	raw, hash, err := refreshCredential()
	if err != nil {
		return nil, err
	}
	now := s.now()
	inactive, absolute := s.sessionLifetimes()
	session := &models.AppSession{ID: newID(), UserID: userID, FamilyID: newID(), TokenHash: hash, DeviceID: device.DeviceID, Platform: device.Platform, IP: device.IP, CreatedAt: now, LastUsedAt: now, ExpiresAt: now.Add(inactive), AbsoluteExpiresAt: now.Add(absolute)}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := lockAuthUser(tx, userID)
		if err != nil {
			return err
		}
		if !authActive(user) || user.LockedUntil != nil && user.LockedUntil.After(now) {
			return ErrInvalidCredentials
		}
		if err := tx.Create(session).Error; err != nil {
			return ErrAuthUnavailable
		}
		return recordSecurityEvent(tx, userID, "session_created", device.IP, now)
	})
	if err != nil {
		return nil, authError(err)
	}
	return &IssuedAppSession{Session: session, RefreshToken: raw}, nil
}

// RotateAppSession replaces both refresh token and sid. Previously issued access
// tokens referring to the old sid are immediately invalid. The inactivity window
// slides on refresh, but cannot exceed the family's absolute deadline.
func (s *Store) RotateAppSession(ctx context.Context, rawRefresh string, device SessionDevice) (*IssuedAppSession, error) {
	hash, err := hashRefresh(rawRefresh)
	if err != nil {
		return nil, err
	}
	device, err = sessionDevice(device)
	if err != nil {
		return nil, err
	}
	var anchor models.AppSession
	if err := s.db.WithContext(ctx).Select("user_id").First(&anchor, "token_hash = ?", hash).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, ErrAuthUnavailable
	}
	raw, newHash, err := refreshCredential()
	if err != nil {
		return nil, err
	}
	var replacement models.AppSession
	rejected := false
	now := s.now()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := lockAuthUser(tx, anchor.UserID)
		if err != nil {
			return err
		}
		var previous models.AppSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&previous, "token_hash = ? AND user_id = ?", hash, anchor.UserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvalidCredentials
			}
			return ErrAuthUnavailable
		}
		if previous.ReplacedBy != nil {
			if err := tx.Model(&models.AppSession{}).Where("family_id = ? AND user_id = ? AND revoked_at IS NULL", previous.FamilyID, previous.UserID).Update("revoked_at", now).Error; err != nil {
				return ErrAuthUnavailable
			}
			if err := tx.Where("user_id = ? AND session_family_id = ?", previous.UserID, previous.FamilyID).Delete(&models.PushDevice{}).Error; err != nil {
				return ErrAuthUnavailable
			}
			rejected = true
			return recordSecurityEvent(tx, previous.UserID, "refresh_reuse_detected", device.IP, now)
		}
		if previous.RevokedAt != nil || !previous.ExpiresAt.After(now) || !previous.AbsoluteExpiresAt.After(now) || !authActive(user) || user.LockedUntil != nil && user.LockedUntil.After(now) {
			return ErrInvalidCredentials
		}
		if device.DeviceID == "" {
			device.DeviceID = previous.DeviceID
		}
		if device.Platform == "" {
			device.Platform = previous.Platform
		}
		inactivity, _ := s.sessionLifetimes()
		expires := now.Add(inactivity)
		if expires.After(previous.AbsoluteExpiresAt) {
			expires = previous.AbsoluteExpiresAt
		}
		replacement = models.AppSession{ID: newID(), UserID: previous.UserID, FamilyID: previous.FamilyID, TokenHash: newHash, DeviceID: device.DeviceID, Platform: device.Platform, IP: device.IP, CreatedAt: now, LastUsedAt: now, ExpiresAt: expires, AbsoluteExpiresAt: previous.AbsoluteExpiresAt}
		if err := tx.Create(&replacement).Error; err != nil {
			return ErrAuthUnavailable
		}
		result := tx.Model(&models.AppSession{}).Where("id = ? AND revoked_at IS NULL AND replaced_by IS NULL", previous.ID).Updates(map[string]any{"revoked_at": now, "replaced_by": replacement.ID, "last_used_at": now})
		if result.Error != nil {
			return ErrAuthUnavailable
		}
		if result.RowsAffected != 1 {
			return ErrInvalidCredentials
		}
		return recordSecurityEvent(tx, previous.UserID, "session_rotated", device.IP, now)
	})
	if err != nil {
		return nil, authError(err)
	}
	if rejected {
		return nil, ErrInvalidCredentials
	}
	return &IssuedAppSession{Session: &replacement, RefreshToken: raw}, nil
}

func (s *Store) ValidateAppSession(ctx context.Context, userID, sid string) error {
	if _, err := uuid.Parse(sid); err != nil {
		return ErrInvalidCredentials
	}
	now := s.now()
	return authError(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := lockAuthUser(tx, userID)
		if err != nil {
			return err
		}
		if !authActive(user) {
			return ErrInvalidCredentials
		}
		var session models.AppSession
		if err := tx.First(&session, "id = ? AND user_id = ? AND revoked_at IS NULL AND expires_at > ? AND absolute_expires_at > ?", sid, userID, now, now).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvalidCredentials
			}
			return ErrAuthUnavailable
		}
		if session.LastUsedAt.Before(now.Add(-time.Minute)) {
			if err := tx.Model(&session).Update("last_used_at", now).Error; err != nil {
				return ErrAuthUnavailable
			}
		}
		return nil
	}))
}

// AppUserForSession distinguishes a missing/inactive account from a database
// outage after refresh rotation, so clients do not discard valid credentials
// on a transient failure.
func (s *Store) AppUserForSession(ctx context.Context, userID string) (*models.User, error) {
	var user models.User
	if err := s.db.WithContext(ctx).First(&user, "id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, ErrAuthUnavailable
	}
	if !authActive(&user) {
		return nil, ErrInvalidCredentials
	}
	return &user, nil
}

// RevokeAppSessionsInTransaction is an invalidation hook for account status,
// role and deletion transitions. The caller holds the user's row lock.
func RevokeAppSessionsInTransaction(tx *gorm.DB, userID string, now time.Time) error {
	if err := tx.Model(&models.AppSession{}).Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", now).Error; err != nil {
		return ErrAuthUnavailable
	}
	if err := tx.Where("user_id = ?", userID).Delete(&models.PushDevice{}).Error; err != nil {
		return ErrAuthUnavailable
	}
	return nil
}

func (s *Store) RevokeAppSession(ctx context.Context, userID, sid string) error {
	if _, err := uuid.Parse(sid); err != nil {
		return FieldError("id", "valid session UUID required")
	}
	now := s.now()
	return authError(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockAuthUser(tx, userID); err != nil {
			return err
		}
		var session models.AppSession
		if err := tx.First(&session, "id = ? AND user_id = ?", sid, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return gorm.ErrRecordNotFound
			}
			return ErrAuthUnavailable
		}
		if session.RevokedAt != nil {
			return nil
		}
		if err := tx.Model(&session).Update("revoked_at", now).Error; err != nil {
			return ErrAuthUnavailable
		}
		if err := tx.Where("user_id = ? AND session_family_id = ?", userID, session.FamilyID).Delete(&models.PushDevice{}).Error; err != nil {
			return ErrAuthUnavailable
		}
		return recordSecurityEvent(tx, userID, "session_revoked", "", now)
	}))
}

func (s *Store) RevokeAllAppSessions(ctx context.Context, userID string) error {
	now := s.now()
	return authError(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockAuthUser(tx, userID); err != nil {
			return err
		}
		if err := RevokeAppSessionsInTransaction(tx, userID, now); err != nil {
			return err
		}
		return recordSecurityEvent(tx, userID, "sessions_revoked", "", now)
	}))
}

func authPage(page Page) (Page, error) {
	if page.Limit == 0 {
		page.Limit = 50
	}
	if page.Limit < 1 || page.Limit > 200 || page.Offset < 0 {
		return page, FieldError("pagination", "limit must be 1-200 and offset non-negative")
	}
	return page, nil
}

func (s *Store) ListAppSessions(ctx context.Context, userID string, page Page) ([]models.AppSession, int64, error) {
	page, err := authPage(page)
	if err != nil {
		return nil, 0, err
	}
	sessions := []models.AppSession{}
	var total int64
	query := s.db.WithContext(ctx).Model(&models.AppSession{}).Where("user_id = ? AND revoked_at IS NULL AND expires_at > ? AND absolute_expires_at > ?", userID, s.now(), s.now())
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, ErrAuthUnavailable
	}
	if err := query.Order("last_used_at DESC, created_at DESC, id DESC").Limit(page.Limit).Offset(page.Offset).Find(&sessions).Error; err != nil {
		return nil, 0, ErrAuthUnavailable
	}
	return sessions, total, nil
}

func authError(err error) error {
	if err == nil || errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrAuthUnavailable) || errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var field *OperationError
	if errors.As(err, &field) {
		return err
	}
	return ErrAuthUnavailable
}
