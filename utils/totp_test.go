package utils

import (
	"strings"
	"testing"
	"time"
)

func TestTOTPSecretLengthSkewReplayAndProvisioningURI(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" {
		t.Fatal("secret must be generated")
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	current, err := GenerateTOTPCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := GenerateTOTPCode(secret, now.Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ahead, err := GenerateTOTPCode(secret, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	far, err := GenerateTOTPCode(secret, now.Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	step, ok := MatchTOTPStep(secret, current, now)
	if !ok || step != now.Unix()/30 {
		t.Fatalf("current step: %d %v", step, ok)
	}
	if _, ok := MatchTOTPStep(secret, previous, now); !ok {
		t.Fatal("one step behind must be accepted")
	}
	if _, ok := MatchTOTPStep(secret, ahead, now); !ok {
		t.Fatal("one step ahead must be accepted")
	}
	if _, ok := MatchTOTPStep(secret, far, now); ok {
		t.Fatal("three steps ahead must be rejected")
	}
	if _, ok := MatchTOTPStep(secret, "000000", now); ok {
		t.Fatal("wrong code accepted")
	}
	uri := TOTPAuthURL("admin@rida.tz", secret, "Rida")
	for _, part := range []string{"otpauth://totp/Rida:admin@rida.tz", "algorithm=SHA1", "digits=6", "period=30", "issuer=Rida", "secret=" + secret} {
		if !strings.Contains(uri, part) {
			t.Fatalf("uri missing %s: %s", part, uri)
		}
	}
}
