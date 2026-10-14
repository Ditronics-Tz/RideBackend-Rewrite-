package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"ride-backend/models"
	"ride-backend/utils"
)

func googleIdentityConflict(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) || err != nil && err.Error() == "email already registered" {
		return true
	}
	var sqlState interface{ SQLState() string }
	return errors.As(err, &sqlState) && sqlState.SQLState() == "23505"
}

func randomOAuthValue() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (s *Store) CreateOAuthState(ctx context.Context, redirect string) (state, browser string, err error) {
	state, err = randomOAuthValue()
	if err != nil {
		return
	}
	browser, err = randomOAuthValue()
	if err != nil {
		return
	}
	row := models.OAuthState{StateHash: utils.HashSHA256(state), BrowserHash: utils.HashSHA256(browser), RedirectURI: redirect, ExpiresAt: s.now().Add(10 * time.Minute), CreatedAt: s.now()}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("expires_at <= ?", s.now()).Delete(&models.OAuthState{}).Error; err != nil {
			return err
		}
		return tx.Create(&row).Error
	})
	return
}

func (s *Store) ConsumeOAuthState(ctx context.Context, state, browser, redirect string) error {
	if len(state) != 64 || len(browser) != 64 {
		return OpError(400, "invalid OAuth state")
	}
	r := s.db.WithContext(ctx).Where("state_hash = ? AND browser_hash = ? AND redirect_uri = ? AND expires_at > ?", utils.HashSHA256(state), utils.HashSHA256(browser), redirect, s.now()).Delete(&models.OAuthState{})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return OpError(400, "invalid OAuth state")
	}
	return nil
}

// Existing emails are never auto-linked: an unverified local registration must
// not gain control of an unrelated Google identity with the same email.
func (s *Store) ResolveGoogleIdentity(ctx context.Context, subject, name, email string) (*models.User, error) {
	if subject == "" || len(subject) > 255 {
		return nil, OpError(401, "invalid Google identity")
	}
	var result *models.User
	err := s.WithContext(ctx).Transaction(func(tx *Store) error {
		var identity models.OAuthIdentity
		err := tx.db.Where("provider = ? AND subject = ?", "google", subject).First(&identity).Error
		if err == nil {
			var u models.User
			if err := tx.db.First(&u, "id = ?", identity.UserID).Error; err != nil {
				return err
			}
			if u.DeletedAt != nil || (u.Status != "pending_deletion" && (!u.IsActive || u.Status != "active")) {
				return OpError(401, "invalid credentials")
			}
			result = &u
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var count int64
		if err := tx.db.Model(&models.User{}).Where("email = ?", email).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return OpError(409, "Sign in to the existing account; automatic Google linking is unavailable")
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = "Passenger"
		}
		if len([]rune(name)) > 100 {
			name = string([]rune(name)[:100])
		}
		u, err := tx.CreateUser(name, email, "", models.RolePassenger)
		if err != nil {
			return err
		}
		identity = models.OAuthIdentity{ID: newID(), Provider: "google", Subject: subject, UserID: u.ID, CreatedAt: s.now()}
		if err := tx.db.Create(&identity).Error; err != nil {
			return err
		}
		result = u
		return nil
	})
	if googleIdentityConflict(err) {
		return nil, OpError(409, "Sign in to the existing account; automatic Google linking is unavailable")
	}
	return result, err
}

func init() {
	RegisterDataExporter(func(s *Store) DataExporter { return oauthExporter{s} })
	RegisterAnonymizer(oauthAnonymizer{})
}

type oauthExporter struct{ s *Store }

func (oauthExporter) Name() string { return "oauth" }
func (oauthExporter) Tables() []string {
	return []string{"oauth_states", "oauth_identities", "google_challenges"}
}
func (e oauthExporter) Export(id string) (any, []string, error) {
	var rows []struct {
		Provider  string
		CreatedAt time.Time
	}
	err := e.s.db.Model(&models.OAuthIdentity{}).Select("provider,created_at").Where("user_id = ?", id).Find(&rows).Error
	return map[string]any{"identities": rows}, nil, err
}

type oauthAnonymizer struct{}

func (oauthAnonymizer) Name() string { return "oauth" }
func (oauthAnonymizer) Tables() []string {
	return []string{"oauth_states", "oauth_identities", "google_challenges"}
}
func (oauthAnonymizer) Description() string {
	return "deletes linked identities; anonymous browser state and mobile Google challenges contain no account association and expire automatically"
}
func (oauthAnonymizer) Anonymize(tx *gorm.DB, id string) error {
	return tx.Where("user_id = ?", id).Delete(&models.OAuthIdentity{}).Error
}
