package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"ride-backend/models"
)

func authTestStore(t *testing.T) (*Store, *models.User, *time.Time) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("test database unavailable")
	}
	if err := database.AutoMigrate(&models.User{}, &models.AppSession{}, &models.AppSecurityEvent{}, &models.AuthRateBucket{}, &models.StaffAuditLog{}); err != nil {
		t.Fatal("test schema unavailable")
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	s := New(database)
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	s.Clock = func() time.Time { return now }
	user := &models.User{ID: uuid.NewString(), Name: "Session fixture", Email: uuid.NewString() + "@example.test", Role: models.RolePassenger, Status: "active", IsActive: true}
	if err := database.Create(user).Error; err != nil {
		t.Fatal("test user unavailable")
	}
	return s, user, &now
}

func issueForTest(t *testing.T, s *Store, user *models.User) *IssuedAppSession {
	t.Helper()
	issued, err := s.IssueAppSession(context.Background(), user.ID, SessionDevice{DeviceID: "fixture-device", Platform: "web", IP: "192.0.2.1"})
	if err != nil {
		t.Fatal("session issuance failed")
	}
	return issued
}

func TestAppSessionHashesCredentialsAndHidesSecretMetadata(t *testing.T) {
	s, user, now := authTestStore(t)
	issued := issueForTest(t, s, user)
	var stored models.AppSession
	if err := s.db.First(&stored, "id = ?", issued.Session.ID).Error; err != nil {
		t.Fatal("session not persisted")
	}
	hash, err := hashRefresh(issued.RefreshToken)
	if err != nil || stored.TokenHash != hash || stored.TokenHash == issued.RefreshToken || len(stored.TokenHash) != 64 {
		t.Fatal("refresh credential was not hashed")
	}
	if !stored.ExpiresAt.Equal(now.Add(AppRefreshTTL)) {
		t.Fatal("refresh expiry differs from 30 days")
	}
	encoded, err := json.Marshal(issued)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{issued.RefreshToken, stored.TokenHash, stored.FamilyID, "token_hash", "FamilyID", "RefreshToken"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("session serialization exposed secret metadata")
		}
	}
	if err := s.ValidateAppSession(context.Background(), user.ID, stored.ID); err != nil {
		t.Fatal("issued session is invalid")
	}
	if err := s.ValidateAppSession(context.Background(), user.ID, ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("missing sid accepted")
	}
}

func TestAppSessionRotationInvalidatesOldSIDAndKeepsFamilyExpiry(t *testing.T) {
	s, user, now := authTestStore(t)
	original := issueForTest(t, s, user)
	*now = now.Add(time.Hour)
	rotated, err := s.RotateAppSession(context.Background(), original.RefreshToken, SessionDevice{IP: "192.0.2.2"})
	if err != nil {
		t.Fatal("rotation failed")
	}
	if rotated.Session.ID == original.Session.ID || rotated.RefreshToken == original.RefreshToken || rotated.Session.FamilyID != original.Session.FamilyID || !rotated.Session.ExpiresAt.Equal(original.Session.ExpiresAt) || rotated.Session.DeviceID != original.Session.DeviceID {
		t.Fatal("rotation contract violated")
	}
	if err := s.ValidateAppSession(context.Background(), user.ID, original.Session.ID); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("old access sid remains valid")
	}
	if err := s.ValidateAppSession(context.Background(), user.ID, rotated.Session.ID); err != nil {
		t.Fatal("rotated session invalid")
	}
	rows, total, err := s.ListAppSessions(context.Background(), user.ID, Page{Limit: 50})
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != rotated.Session.ID {
		t.Fatal("session list contains replaced sessions")
	}
}

func TestAppSessionRefreshReuseCommitsFamilyRevocation(t *testing.T) {
	s, user, _ := authTestStore(t)
	original := issueForTest(t, s, user)
	independent := issueForTest(t, s, user)
	rotated, err := s.RotateAppSession(context.Background(), original.RefreshToken, SessionDevice{})
	if err != nil {
		t.Fatal("rotation failed")
	}
	if _, err := s.RotateAppSession(context.Background(), original.RefreshToken, SessionDevice{IP: "192.0.2.3"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("replayed refresh accepted")
	}
	if err := s.ValidateAppSession(context.Background(), user.ID, rotated.Session.ID); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("family revocation rolled back when replay was rejected")
	}
	if err := s.ValidateAppSession(context.Background(), user.ID, independent.Session.ID); err != nil {
		t.Fatal("reuse revoked an unrelated family")
	}
	events, total, err := s.ListAppSecurityEvents(context.Background(), user.ID, "refresh_reuse_detected", Page{})
	if err != nil || total != 1 || len(events) != 1 {
		t.Fatal("reuse security event not recorded")
	}
}

func TestAppSessionScopeExpiryAndRevocation(t *testing.T) {
	s, user, now := authTestStore(t)
	issued := issueForTest(t, s, user)
	other := &models.User{ID: uuid.NewString(), Name: "Other fixture", Email: uuid.NewString() + "@example.test", Role: models.RoleDriver, IsActive: true, Status: "active"}
	if err := s.db.Create(other).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeAppSession(context.Background(), other.ID, issued.Session.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("another user can revoke session")
	}
	if err := s.ValidateAppSession(context.Background(), other.ID, issued.Session.ID); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("sid can be used for another user")
	}
	if err := s.RevokeAppSession(context.Background(), user.ID, issued.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RotateAppSession(context.Background(), issued.RefreshToken, SessionDevice{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("revoked refresh accepted")
	}
	second := issueForTest(t, s, user)
	*now = now.Add(AppRefreshTTL + time.Second)
	if err := s.ValidateAppSession(context.Background(), user.ID, second.Session.ID); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("expired session accepted")
	}
	if _, err := s.RotateAppSession(context.Background(), second.RefreshToken, SessionDevice{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("expired refresh accepted")
	}
	_, total, err := s.ListAppSessions(context.Background(), user.ID, Page{})
	if err != nil || total != 0 {
		t.Fatal("expired/revoked sessions listed as active")
	}
}

func TestAppSessionRejectsInactiveStatesAndPreservesPendingRecovery(t *testing.T) {
	for _, status := range []string{"suspended", "banned", "deleted", "disabled", "pending_deletion"} {
		t.Run(status, func(t *testing.T) {
			s, user, _ := authTestStore(t)
			issued := issueForTest(t, s, user)
			changes := map[string]any{"status": status, "is_active": false}
			if status == "disabled" {
				changes["status"] = "active"
			}
			if status == "deleted" {
				changes["deleted_at"] = s.now()
			}
			if err := s.db.Model(user).Updates(changes).Error; err != nil {
				t.Fatal(err)
			}
			if status == "pending_deletion" {
				if err := s.ValidateAppSession(context.Background(), user.ID, issued.Session.ID); err != nil {
					t.Fatal("pending deletion recovery access rejected")
				}
				if _, err := s.RotateAppSession(context.Background(), issued.RefreshToken, SessionDevice{}); err != nil {
					t.Fatal("pending deletion recovery refresh rejected")
				}
				if _, err := s.IssueAppSession(context.Background(), user.ID, SessionDevice{}); err != nil {
					t.Fatal("pending deletion recovery login rejected")
				}
				return
			}
			if err := s.ValidateAppSession(context.Background(), user.ID, issued.Session.ID); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatal("inactive access accepted")
			}
			if _, err := s.RotateAppSession(context.Background(), issued.RefreshToken, SessionDevice{}); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatal("inactive refresh accepted")
			}
			if _, err := s.IssueAppSession(context.Background(), user.ID, SessionDevice{}); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatal("inactive login issuance accepted")
			}
		})
	}
}

func TestAppSessionConcurrentRotationAllowsOneReplacementThenDetectsReplay(t *testing.T) {
	s, user, _ := authTestStore(t)
	original := issueForTest(t, s, user)
	var workers sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := s.RotateAppSession(context.Background(), original.RefreshToken, SessionDevice{})
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	success, replay := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrInvalidCredentials) {
			replay++
		} else {
			t.Fatal("unexpected concurrent rotation error")
		}
	}
	if success != 1 || replay != 1 {
		t.Fatal("concurrent rotation created multiple replacements")
	}
	_, active, err := s.ListAppSessions(context.Background(), user.ID, Page{})
	if err != nil || active != 0 {
		t.Fatal("concurrent replay did not revoke replacement family")
	}
}

func TestAppSessionAccountTransitionHookPreventsNewSessionAfterSuspension(t *testing.T) {
	s, user, _ := authTestStore(t)
	issued := issueForTest(t, s, user)
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := lockAuthUser(tx, user.ID); err != nil {
			return err
		}
		if err := tx.Model(user).Updates(map[string]any{"status": "suspended", "is_active": false}).Error; err != nil {
			return err
		}
		return RevokeAppSessionsInTransaction(tx, user.ID, s.now())
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateAppSession(context.Background(), user.ID, issued.Session.ID); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("transition left access valid")
	}
	if _, err := s.IssueAppSession(context.Background(), user.ID, SessionDevice{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("issuance after suspension accepted")
	}
}
