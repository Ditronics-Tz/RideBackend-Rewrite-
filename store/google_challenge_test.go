package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"ride-backend/models"
)

func TestGoogleChallengeNonceReplayAndExpiry(t *testing.T) {
	s, _, now := authTestStore(t)
	id, nonce, expiry, err := s.CreateGoogleChallenge(context.Background())
	if err != nil || len(nonce) != 64 || !expiry.Equal(now.Add(5*time.Minute)) {
		t.Fatal("challenge creation failed")
	}
	var row models.GoogleChallenge
	if err := s.db.First(&row, "id = ?", id).Error; err != nil || row.NonceHash == nonce {
		t.Fatal("plaintext nonce persisted")
	}
	wrong := nonce[:63] + "0"
	if nonce[63] == '0' {
		wrong = nonce[:63] + "1"
	}
	if err := s.ConsumeGoogleChallenge(context.Background(), id, wrong); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("wrong nonce accepted")
	}
	if err := s.ConsumeGoogleChallenge(context.Background(), id, nonce); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeGoogleChallenge(context.Background(), id, nonce); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("nonce replay accepted")
	}
	id, nonce, _, err = s.CreateGoogleChallenge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(5*time.Minute + time.Second)
	if err := s.ConsumeGoogleChallenge(context.Background(), id, nonce); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("expired nonce accepted")
	}
}
