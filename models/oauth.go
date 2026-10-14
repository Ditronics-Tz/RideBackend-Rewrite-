package models

import "time"

type OAuthState struct {
	StateHash   string    `json:"-" gorm:"primaryKey;size:64"`
	BrowserHash string    `json:"-" gorm:"not null;size:64"`
	RedirectURI string    `json:"-" gorm:"not null"`
	ExpiresAt   time.Time `json:"-" gorm:"not null;index"`
	CreatedAt   time.Time `json:"-"`
}

func (OAuthState) TableName() string { return "oauth_states" }

// GoogleChallenge stores only a digest of the nonce sent to the Android client.
type GoogleChallenge struct {
	ID        string    `json:"-" gorm:"primaryKey;type:uuid"`
	NonceHash string    `json:"-" gorm:"type:varchar(64);not null"`
	ExpiresAt time.Time `json:"-" gorm:"not null;index"`
	CreatedAt time.Time `json:"-" gorm:"not null"`
}

type OAuthIdentity struct {
	ID        string    `json:"-" gorm:"primaryKey;type:uuid"`
	Provider  string    `json:"-" gorm:"not null;uniqueIndex:idx_oauth_provider_subject"`
	Subject   string    `json:"-" gorm:"not null;uniqueIndex:idx_oauth_provider_subject"`
	UserID    string    `json:"-" gorm:"not null;type:uuid;index"`
	CreatedAt time.Time `json:"-"`
}

func (OAuthIdentity) TableName() string { return "oauth_identities" }
