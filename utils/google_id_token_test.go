package utils

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/api/idtoken"
)

func TestGoogleIDTokenClaimsAndVerificationFailures(t *testing.T) {
	base := idtoken.Payload{Issuer: "https://accounts.google.com", Audience: "web-client", Expires: time.Now().Add(time.Hour).Unix(), Subject: "google-sub", Claims: map[string]interface{}{"nonce": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "email": "user@example.test", "email_verified": true}}
	for _, tc := range []struct {
		name   string
		mutate func(*idtoken.Payload)
		reject bool
	}{
		{"valid", func(*idtoken.Payload) {}, false},
		{"audience", func(p *idtoken.Payload) { p.Audience = "other" }, true},
		{"issuer", func(p *idtoken.Payload) { p.Issuer = "https://attacker.test" }, true},
		{"expired", func(p *idtoken.Payload) { p.Expires = time.Now().Add(-time.Minute).Unix() }, true},
		{"missing subject", func(p *idtoken.Payload) { p.Subject = "" }, true},
		{"missing nonce", func(p *idtoken.Payload) { delete(p.Claims, "nonce") }, true},
		{"unverified email", func(p *idtoken.Payload) { p.Claims["email_verified"] = false }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			p.Claims = map[string]interface{}{}
			for k, v := range base.Claims {
				p.Claims[k] = v
			}
			tc.mutate(&p)
			v := GoogleIDTokenVerifier{Validate: func(context.Context, string, string) (*idtoken.Payload, error) { return &p, nil }}
			_, _, err := v.Verify(context.Background(), "signed-fixture", "web-client")
			if (err != nil) != tc.reject {
				t.Fatalf("reject=%v error=%v", tc.reject, err)
			}
		})
	}
	v := GoogleIDTokenVerifier{Validate: func(context.Context, string, string) (*idtoken.Payload, error) {
		return nil, errors.New("bad signature")
	}}
	if _, _, err := v.Verify(context.Background(), "signed-fixture", "web-client"); !errors.Is(err, ErrGoogleIDToken) {
		t.Fatal("signature failure accepted")
	}
	if _, _, err := (GoogleIDTokenVerifier{}).Verify(context.Background(), "not-a-jwt", "web-client"); !errors.Is(err, ErrGoogleIDToken) {
		t.Fatal("malformed token accepted by Google verifier")
	}
}
