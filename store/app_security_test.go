package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ride-backend/models"
)

func TestAppLoginLockoutThresholdExpiryResetAndExistingAccess(t *testing.T) {
	s, user, now := authTestStore(t)
	issued := issueForTest(t, s, user)
	for attempt := 1; attempt <= AppMaxLoginFailures; attempt++ {
		locked, err := s.RecordAppLoginFailure(context.Background(), user.ID)
		if err != nil || locked != (attempt == AppMaxLoginFailures) {
			t.Fatal("lockout threshold mismatch")
		}
	}
	locked, err := s.IsAppLoginLocked(context.Background(), user.ID)
	if err != nil || !locked {
		t.Fatal("lockout not persisted")
	}
	if _, err := s.IssueAppSession(context.Background(), user.ID, SessionDevice{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("locked login issued a session")
	}
	if _, err := s.RotateAppSession(context.Background(), issued.RefreshToken, SessionDevice{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("locked account refreshed")
	}
	if err := s.ValidateAppSession(context.Background(), user.ID, issued.Session.ID); err != nil {
		t.Fatal("password guessing denied existing authenticated access")
	}
	*now = now.Add(AppLoginLockDuration + time.Second)
	locked, err = s.IsAppLoginLocked(context.Background(), user.ID)
	if err != nil || locked {
		t.Fatal("lockout did not expire")
	}
	locked, err = s.RecordAppLoginFailure(context.Background(), user.ID)
	if err != nil || locked {
		t.Fatal("expired lockout retained stale failures")
	}
	var stored models.User
	if err := s.db.First(&stored, "id = ?", user.ID).Error; err != nil || stored.FailedLoginCount != 1 || stored.LockedUntil != nil {
		t.Fatal("expired counter not restarted")
	}
	if err := s.ResetAppLoginFailures(context.Background(), user.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.First(&stored, "id = ?", user.ID).Error; err != nil || stored.FailedLoginCount != 0 {
		t.Fatal("successful login reset failed")
	}
}

func TestAppLoginManualUnlockIsAuditedAndAtomic(t *testing.T) {
	s, user, _ := authTestStore(t)
	for i := 0; i < AppMaxLoginFailures; i++ {
		if _, err := s.RecordAppLoginFailure(context.Background(), user.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UnlockAppLogin(context.Background(), user.ID, uuid.NewString(), "192.0.2.9"); err != nil {
		t.Fatal(err)
	}
	locked, err := s.IsAppLoginLocked(context.Background(), user.ID)
	if err != nil || locked {
		t.Fatal("unlock left account locked")
	}
	var audit models.StaffAuditLog
	if err := s.db.Where("action = ? AND target_id = ?", "unlock_app_login", user.ID).First(&audit).Error; err != nil || audit.Meta != "{}" {
		t.Fatal("unlock audit missing or contains metadata")
	}
	for i := 0; i < AppMaxLoginFailures; i++ {
		s.RecordAppLoginFailure(context.Background(), user.ID)
	}
	if err := s.db.Migrator().DropTable(&models.StaffAuditLog{}); err != nil {
		t.Fatal(err)
	}
	if err := s.UnlockAppLogin(context.Background(), user.ID, uuid.NewString(), ""); !errors.Is(err, ErrAuthUnavailable) {
		t.Fatal("unaudited unlock succeeded")
	}
	locked, err = s.IsAppLoginLocked(context.Background(), user.ID)
	if err != nil || !locked {
		t.Fatal("failed audit did not roll back unlock")
	}
}

func TestAppSecurityEventsFilterAndBoundMetadata(t *testing.T) {
	s, user, _ := authTestStore(t)
	for _, kind := range []string{"login_failed", "login_succeeded", "login_failed"} {
		if err := s.RecordAppSecurityEvent(context.Background(), user.ID, kind, "192.0.2.8"); err != nil {
			t.Fatal(err)
		}
	}
	events, total, err := s.ListAppSecurityEvents(context.Background(), user.ID, "login_failed", Page{Limit: 1, Offset: 1})
	if err != nil || total != 2 || len(events) != 1 || events[0].Type != "login_failed" {
		t.Fatal("security event filtering/pagination failed")
	}
	if err := s.RecordAppSecurityEvent(context.Background(), user.ID, "plaintext@example.test", ""); err == nil {
		t.Fatal("personal string accepted as event type")
	}
	if _, err := s.IssueAppSession(context.Background(), user.ID, SessionDevice{DeviceID: strings.Repeat("x", 129)}); err == nil {
		t.Fatal("unbounded device metadata accepted")
	}
}

func TestAppLimiterPersistsNormalizedDigestAndHonorsWindow(t *testing.T) {
	s, user, now := authTestStore(t)
	first := NewSQLAuthLimiter(s)
	second := NewSQLAuthLimiter(s)
	rate := AuthRate{Limit: 2, Window: time.Minute}
	for _, limiter := range []AuthLimiter{first, second} {
		decision, err := limiter.Allow(context.Background(), "app_login_account", " "+strings.ToUpper(user.Email)+" ", rate)
		if err != nil || !decision.Allowed || decision.RetryAfter != 0 {
			t.Fatal("allowed rate request rejected")
		}
	}
	decision, err := second.Allow(context.Background(), "app_login_account", user.Email, rate)
	if err != nil || decision.Allowed || decision.RetryAfter != 60 {
		t.Fatal("persistent limit was not enforced")
	}
	var bucket models.AuthRateBucket
	if err := s.db.First(&bucket).Error; err != nil || bucket.KeyHash != authRateHash(user.Email) || strings.Contains(bucket.KeyHash, user.Email) || bucket.Count != 3 {
		t.Fatal("limiter did not store normalized digest/capped counter")
	}
	*now = now.Add(30 * time.Second)
	decision, err = first.Allow(context.Background(), "app_login_account", user.Email, rate)
	if err != nil || decision.Allowed || decision.RetryAfter != 30 {
		t.Fatal("rejection extended rate window")
	}
	*now = now.Add(31 * time.Second)
	decision, err = second.Allow(context.Background(), "app_login_account", user.Email, rate)
	if err != nil || !decision.Allowed {
		t.Fatal("rate window did not reset")
	}
	decision, err = first.Allow(context.Background(), "app_register_account", user.Email, rate)
	if err != nil || !decision.Allowed {
		t.Fatal("scope policies share counters")
	}
}

func TestAppLimiterCleanupIsBounded(t *testing.T) {
	s, _, now := authTestStore(t)
	rows := []models.AuthRateBucket{}
	for i := 0; i < 150; i++ {
		rows = append(rows, models.AuthRateBucket{Scope: "fixture_account", KeyHash: authRateHash(uuid.NewString()), Count: 1, WindowStart: now.Add(-2 * time.Minute), ExpiresAt: now.Add(-time.Minute)})
	}
	if err := s.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	deleted, err := s.PruneAuthRateBuckets(context.Background(), 100)
	if err != nil || deleted != 100 {
		t.Fatal("expired cleanup is not bounded")
	}
	var remaining int64
	if err := s.db.Model(&models.AuthRateBucket{}).Count(&remaining).Error; err != nil || remaining != 50 {
		t.Fatal("cleanup removed wrong rows")
	}
	if _, err := NewSQLAuthLimiter(s).Allow(context.Background(), "app_login_ip", "192.0.2.1", AuthRate{Limit: 1, Window: 2 * time.Hour}); err == nil {
		t.Fatal("unbounded rate window accepted")
	}
}

func TestAppAuthPrivacyHookClearsOwnedRowsAndKeepsAnonymousIPWindow(t *testing.T) {
	s, user, _ := authTestStore(t)
	issueForTest(t, s, user)
	limiter := NewSQLAuthLimiter(s)
	for _, policy := range []struct{ scope, key string }{{"app_login_account", user.Email}, {"app_login_ip", "192.0.2.1"}} {
		if _, err := limiter.Allow(context.Background(), policy.scope, policy.key, AuthRate{Limit: 20, Window: time.Minute}); err != nil {
			t.Fatal(err)
		}
	}
	if err := (appAuthAnonymizer{}).Anonymize(s.db, user.ID); err != nil {
		t.Fatal("auth privacy cleanup failed")
	}
	for _, model := range []any{&models.AppSession{}, &models.AppSecurityEvent{}} {
		var count int64
		if err := s.db.Model(model).Where("user_id = ?", user.ID).Count(&count).Error; err != nil || count != 0 {
			t.Fatal("owned auth rows retained after cleanup")
		}
	}
	var owned, shared int64
	s.db.Model(&models.AuthRateBucket{}).Where("scope = ?", "app_login_account").Count(&owned)
	s.db.Model(&models.AuthRateBucket{}).Where("scope = ?", "app_login_ip").Count(&shared)
	if owned != 0 || shared != 1 {
		t.Fatal("auth cleanup removed anonymous IP policy or retained owned account bucket")
	}
}
