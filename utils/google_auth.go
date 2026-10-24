package utils

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"ride-backend/config"
)

type GoogleIdentity struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

type GoogleAuthenticator interface {
	Authenticate(context.Context, string) (GoogleIdentity, error)
}
type GoogleOAuth struct {
	Config *config.Config
	Client *http.Client
}

func (g GoogleOAuth) Authenticate(ctx context.Context, code string) (GoogleIdentity, error) {
	var identity GoogleIdentity
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, client)
	token, err := GetGoogleOAuthConfig(g.Config).Exchange(ctx, code)
	if err != nil {
		return identity, errors.New("Google token exchange failed")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://openidconnect.googleapis.com/v1/userinfo", nil)
	if err != nil {
		return identity, errors.New("Google request failed")
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	resp, err := client.Do(req)
	if err != nil {
		return identity, errors.New("Google identity request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return identity, errors.New("Google identity request rejected")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return identity, errors.New("Google identity response invalid")
	}
	if err := json.Unmarshal(raw, &identity); err != nil {
		return identity, errors.New("Google identity response invalid")
	}
	return identity, nil
}
