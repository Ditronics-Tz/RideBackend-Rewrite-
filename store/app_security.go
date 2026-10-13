package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/models"
)

const AppMaxLoginFailures = 5
const AppLoginLockDuration = 15 * time.Minute

var securityType = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func recordSecurityEvent(tx *gorm.DB, userID, eventType, ip string, now time.Time) error {
	if !securityType.MatchString(eventType) {
		return FieldError("type", "invalid security event type")
	}
	device, err := sessionDevice(SessionDevice{IP: ip})
	if err != nil {
		return err
	}
	if err := tx.Create(&models.AppSecurityEvent{ID: newID(), UserID: userID, Type: eventType, IP: device.IP, CreatedAt: now}).Error; err != nil {
		return ErrAuthUnavailable
	}
	return nil
}

func (s *Store) RecordAppSecurityEvent(ctx context.Context, userID, eventType, ip string) error {
	if !securityType.MatchString(eventType) {
		return FieldError("type", "invalid security event type")
	}
	return authError(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockAuthUser(tx, userID); err != nil {
			return err
		}
		return recordSecurityEvent(tx, userID, eventType, ip, s.now())
	}))
}

func (s *Store) ListAppSecurityEvents(ctx context.Context, userID, eventType string, page Page) ([]models.AppSecurityEvent, int64, error) {
	page, err := authPage(page)
	if err != nil {
		return nil, 0, err
	}
	if eventType != "" && !securityType.MatchString(eventType) {
		return nil, 0, FieldError("type", "invalid security event type")
	}
	query := s.db.WithContext(ctx).Model(&models.AppSecurityEvent{}).Where("user_id = ?", userID)
	if eventType != "" {
		query = query.Where("type = ?", eventType)
	}
	var total int64
	events := []models.AppSecurityEvent{}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, ErrAuthUnavailable
	}
	if err := query.Order("created_at DESC, id DESC").Limit(page.Limit).Offset(page.Offset).Find(&events).Error; err != nil {
		return nil, 0, ErrAuthUnavailable
	}
	return events, total, nil
}

func (s *Store) IsAppLoginLocked(ctx context.Context, userID string) (bool, error) {
	var user models.User
	if err := s.db.WithContext(ctx).Select("locked_until").First(&user, "id = ?", userID).Error; err != nil {
		return false, authError(err)
	}
	return user.LockedUntil != nil && user.LockedUntil.After(s.now()), nil
}

// RecordAppLoginFailure serializes password counters on the user row. The
// controller records the failed/locked event with its request IP separately.
func (s *Store) RecordAppLoginFailure(ctx context.Context, userID string) (bool, error) {
	locked := false
	now := s.now()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := lockAuthUser(tx, userID)
		if err != nil {
			return err
		}
		if user.LockedUntil != nil && user.LockedUntil.After(now) {
			locked = true
			return nil
		}
		if user.LockedUntil != nil {
			user.FailedLoginCount = 0
			user.LockedUntil = nil
		}
		if user.FailedLoginCount < AppMaxLoginFailures {
			user.FailedLoginCount++
		}
		if user.FailedLoginCount >= AppMaxLoginFailures {
			expires := now.Add(AppLoginLockDuration)
			user.LockedUntil = &expires
			locked = true
		}
		if err := tx.Model(user).Updates(map[string]any{"failed_login_count": user.FailedLoginCount, "locked_until": user.LockedUntil}).Error; err != nil {
			return ErrAuthUnavailable
		}
		return nil
	})
	return locked, authError(err)
}

func (s *Store) ResetAppLoginFailures(ctx context.Context, userID string) error {
	return authError(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockAuthUser(tx, userID); err != nil {
			return err
		}
		if err := tx.Model(&models.User{}).Where("id = ?", userID).Updates(map[string]any{"failed_login_count": 0, "locked_until": nil}).Error; err != nil {
			return ErrAuthUnavailable
		}
		return nil
	}))
}

func (s *Store) UnlockAppLogin(ctx context.Context, userID, actorID, ip string) error {
	if actorID == "" {
		return FieldError("actor", "unlock audit actor is required")
	}
	return authError(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockAuthUser(tx, userID); err != nil {
			return err
		}
		if err := tx.Model(&models.User{}).Where("id = ?", userID).Updates(map[string]any{"failed_login_count": 0, "locked_until": nil}).Error; err != nil {
			return ErrAuthUnavailable
		}
		if err := recordSecurityEvent(tx, userID, "login_unlocked", ip, s.now()); err != nil {
			return err
		}
		txStore := *s
		txStore.db = tx
		if err := txStore.CreateAuditLog(actorID, "unlock_app_login", "user", userID, ip, nil); err != nil {
			return ErrAuthUnavailable
		}
		return nil
	}))
}

type AuthRate struct {
	Limit  int
	Window time.Duration
}
type AuthRateDecision struct {
	Allowed    bool
	RetryAfter int
}
type AuthLimiter interface {
	Allow(context.Context, string, string, AuthRate) (AuthRateDecision, error)
}
type SQLAuthLimiter struct{ store *Store }

func NewSQLAuthLimiter(s *Store) AuthLimiter { return &SQLAuthLimiter{store: s} }

func authRateHash(key string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(key))))
	return hex.EncodeToString(sum[:])
}

// Allow uses a fixed window anchored at its first request and an atomic SQL
// upsert. Counters saturate at Limit+1; rejecting a request never extends expiry.
// Window bounds prevent indefinite bucket retention. Scope separates IP and
// account policies; only the normalized irreversible key digest is persisted.
func (l *SQLAuthLimiter) Allow(ctx context.Context, scope, key string, rate AuthRate) (AuthRateDecision, error) {
	if l == nil || l.store == nil {
		return AuthRateDecision{}, ErrAuthUnavailable
	}
	if !securityType.MatchString(scope) || strings.TrimSpace(key) == "" || len(key) > 1024 || rate.Limit < 1 || rate.Limit > 10000 || rate.Window < time.Second || rate.Window > time.Hour {
		return AuthRateDecision{}, FieldError("rate", "invalid authentication rate policy")
	}
	s := l.store
	now := s.now()
	if _, err := s.PruneAuthRateBuckets(ctx, 100); err != nil {
		return AuthRateDecision{}, err
	}
	bucket := models.AuthRateBucket{Scope: scope, KeyHash: authRateHash(key), Count: 1, WindowStart: now, ExpiresAt: now.Add(rate.Window)}
	updates := map[string]any{
		"count":        gorm.Expr("CASE WHEN auth_rate_buckets.expires_at <= ? THEN 1 WHEN auth_rate_buckets.count < ? THEN auth_rate_buckets.count + 1 ELSE ? END", now, rate.Limit+1, rate.Limit+1),
		"window_start": gorm.Expr("CASE WHEN auth_rate_buckets.expires_at <= ? THEN ? ELSE auth_rate_buckets.window_start END", now, now),
		"expires_at":   gorm.Expr("CASE WHEN auth_rate_buckets.expires_at <= ? THEN ? ELSE auth_rate_buckets.expires_at END", now, now.Add(rate.Window)),
	}
	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "scope"}, {Name: "key_hash"}}, DoUpdates: clause.Assignments(updates)}, clause.Returning{}).Create(&bucket)
	if result.Error != nil {
		return AuthRateDecision{}, ErrAuthUnavailable
	}
	decision := AuthRateDecision{Allowed: bucket.Count <= rate.Limit}
	if !decision.Allowed {
		decision.RetryAfter = max(1, int(math.Ceil(bucket.ExpiresAt.Sub(now).Seconds())))
	}
	return decision, nil
}

func (s *Store) PruneAuthRateBuckets(ctx context.Context, limit int) (int64, error) {
	if limit < 1 || limit > 1000 {
		return 0, FieldError("limit", "cleanup limit must be 1-1000")
	}
	now := s.now()
	db := s.db.WithContext(ctx)
	expired := db.Model(&models.AuthRateBucket{}).Select("scope, key_hash").Where("expires_at <= ?", now).Order("expires_at, scope, key_hash").Limit(limit)
	result := db.Where("expires_at <= ? AND (scope, key_hash) IN (?)", now, expired).Delete(&models.AuthRateBucket{})
	if result.Error != nil {
		return 0, ErrAuthUnavailable
	}
	return result.RowsAffected, nil
}

func init() {
	RegisterDataExporter(func(s *Store) DataExporter { return appAuthExporter{s} })
	RegisterAnonymizer(appAuthAnonymizer{})
}

type appAuthExporter struct{ s *Store }

func (appAuthExporter) Name() string { return "app_auth" }
func (appAuthExporter) Tables() []string {
	return []string{"app_sessions", "app_security_events", "auth_rate_buckets"}
}
func (e appAuthExporter) Export(userID string) (any, []string, error) {
	var user models.User
	if err := e.s.db.Select("id, email").First(&user, "id = ?", userID).Error; err != nil {
		return nil, nil, ErrAuthUnavailable
	}
	sessions := []models.AppSession{}
	events := []models.AppSecurityEvent{}
	buckets := []models.AuthRateBucket{}
	if err := e.s.db.Where("user_id = ?", userID).Order("created_at, id").Find(&sessions).Error; err != nil {
		return nil, nil, ErrAuthUnavailable
	}
	if err := e.s.db.Where("user_id = ?", userID).Order("created_at, id").Find(&events).Error; err != nil {
		return nil, nil, ErrAuthUnavailable
	}
	if err := e.s.db.Where("key_hash IN ? AND (scope LIKE ? OR scope LIKE ?)", []string{authRateHash(user.Email), authRateHash(userID)}, "%_account", "%_user").Find(&buckets).Error; err != nil {
		return nil, nil, ErrAuthUnavailable
	}
	return map[string]any{"sessions": sessions, "security_events": events, "rate_windows": buckets}, nil, nil
}

type appAuthAnonymizer struct{}

func (appAuthAnonymizer) Name() string { return "app_auth" }
func (appAuthAnonymizer) Tables() []string {
	return []string{"app_sessions", "app_security_events", "auth_rate_buckets"}
}
func (appAuthAnonymizer) Description() string {
	return "deletes app refresh sessions/security events and owned hashed account limiter buckets; anonymous IP limiter buckets expire within one hour"
}
func (appAuthAnonymizer) Anonymize(tx *gorm.DB, userID string) error {
	var user models.User
	if err := tx.Select("id, email").First(&user, "id = ?", userID).Error; err != nil {
		return ErrAuthUnavailable
	}
	for _, model := range []any{&models.AppSession{}, &models.AppSecurityEvent{}} {
		if err := tx.Where("user_id = ?", userID).Delete(model).Error; err != nil {
			return ErrAuthUnavailable
		}
	}
	if err := tx.Where("key_hash IN ? AND (scope LIKE ? OR scope LIKE ?)", []string{authRateHash(user.Email), authRateHash(userID)}, "%_account", "%_user").Delete(&models.AuthRateBucket{}).Error; err != nil {
		return ErrAuthUnavailable
	}
	return nil
}
