package utils

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/api/idtoken"
)

var ErrGoogleIDToken = errors.New("invalid Google ID token")

// GoogleIDTokenVerifier uses Google's maintained certificate and signature
// verifier. The additional checks are explicit so a change in library policy
// cannot silently weaken the mobile authentication contract.
type GoogleIDTokenVerifier struct {
	Validate func(context.Context, string, string) (*idtoken.Payload, error)
}

func (v GoogleIDTokenVerifier) Verify(ctx context.Context, raw, audience string) (GoogleIdentity, string, error) {
	var empty GoogleIdentity
	if audience == "" || raw == "" || len(raw) > 16384 {
		return empty, "", ErrGoogleIDToken
	}
	validate := v.Validate
	if validate == nil {
		validate = idtoken.Validate
	}
	payload, err := validate(ctx, raw, audience)
	if err != nil || payload == nil || payload.Audience != audience ||
		(payload.Issuer != "https://accounts.google.com" && payload.Issuer != "accounts.google.com") ||
		payload.Expires <= time.Now().Unix() || payload.Subject == "" || len(payload.Subject) > 255 {
		return empty, "", ErrGoogleIDToken
	}
	nonce, ok := payload.Claims["nonce"].(string)
	if !ok || len(nonce) != 64 {
		return empty, "", ErrGoogleIDToken
	}
	email, ok := payload.Claims["email"].(string)
	if !ok || !strings.Contains(email, "@") {
		return empty, "", ErrGoogleIDToken
	}
	verified, ok := payload.Claims["email_verified"].(bool)
	if !ok || !verified {
		return empty, "", ErrGoogleIDToken
	}
	name, _ := payload.Claims["name"].(string)
	return GoogleIdentity{Subject: payload.Subject, Email: email, VerifiedEmail: true, Name: name}, nonce, nil
}
