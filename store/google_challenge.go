package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"ride-backend/models"
	"ride-backend/utils"
)

const googleChallengeTTL = 5 * time.Minute

func (s *Store) CreateGoogleChallenge(ctx context.Context) (string, string, time.Time, error) {
	nonce, err := randomOAuthValue()
	if err != nil {
		return "", "", time.Time{}, ErrAuthUnavailable
	}
	now := s.now()
	row := models.GoogleChallenge{ID: newID(), NonceHash: utils.HashSHA256(nonce), CreatedAt: now, ExpiresAt: now.Add(googleChallengeTTL)}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("expires_at <= ?", now).Delete(&models.GoogleChallenge{}).Error; err != nil {
			return err
		}
		return tx.Create(&row).Error
	}); err != nil {
		return "", "", time.Time{}, ErrAuthUnavailable
	}
	return row.ID, nonce, row.ExpiresAt, nil
}

// ConsumeGoogleChallenge is an atomic one-use compare-and-delete. The nonce is
// read only from a Google-signed ID token, never from an unauthenticated claim.
func (s *Store) ConsumeGoogleChallenge(ctx context.Context, id, signedNonce string) error {
	if _, err := uuid.Parse(id); err != nil || len(signedNonce) != 64 {
		return ErrInvalidCredentials
	}
	result := s.db.WithContext(ctx).Where("id = ? AND nonce_hash = ? AND expires_at > ?", id, utils.HashSHA256(signedNonce), s.now()).Delete(&models.GoogleChallenge{})
	if result.Error != nil {
		return ErrAuthUnavailable
	}
	if result.RowsAffected != 1 {
		return ErrInvalidCredentials
	}
	return nil
}

func (s *Store) FindUserForLogin(ctx context.Context, identifier string) (*models.User, error) {
	var user models.User
	query := s.db.WithContext(ctx).Where("email = ?", identifier)
	if err := query.First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, ErrAuthUnavailable
	}
	return &user, nil
}
