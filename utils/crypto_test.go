package utils_test

import (
	"strings"
	"testing"
	"time"

	"ride-backend/utils"
)

func TestCryptoUtilities(t *testing.T) {
	key := []byte("12345678901234567890123456789012") // 32 bytes

	t.Run("AES-256-GCM encryption and decryption roundtrip", func(t *testing.T) {
		plaintext := "MY_SECRET_TOTP_SEED_12345"
		encrypted, err := utils.EncryptAESGCM(plaintext, key)
		if err != nil {
			t.Fatalf("EncryptAESGCM failed: %v", err)
		}
		if encrypted == plaintext {
			t.Fatal("Encrypted text equals plaintext")
		}

		decrypted, err := utils.DecryptAESGCM(encrypted, key)
		if err != nil {
			t.Fatalf("DecryptAESGCM failed: %v", err)
		}
		if decrypted != plaintext {
			t.Fatalf("Expected %q, got %q", plaintext, decrypted)
		}
	})

	t.Run("AES-256-GCM wrong key fails decryption", func(t *testing.T) {
		plaintext := "MY_SECRET_TOTP_SEED_12345"
		encrypted, err := utils.EncryptAESGCM(plaintext, key)
		if err != nil {
			t.Fatalf("EncryptAESGCM failed: %v", err)
		}

		wrongKey := []byte("99999999999999999999999999999999")
		_, err = utils.DecryptAESGCM(encrypted, wrongKey)
		if err == nil {
			t.Fatal("Expected error with wrong decryption key")
		}
	})

	t.Run("TOTP generation and validation", func(t *testing.T) {
		secret, err := utils.GenerateTOTPSecret()
		if err != nil {
			t.Fatalf("GenerateTOTPSecret failed: %v", err)
		}
		if len(secret) < 16 {
			t.Fatalf("Generated secret too short: %s", secret)
		}

		code, err := utils.GenerateTOTPCode(secret, time.Now())
		if err != nil {
			t.Fatalf("GenerateTOTPCode failed: %v", err)
		}
		if len(code) != 6 {
			t.Fatalf("Expected 6-digit code, got %s", code)
		}

		if !utils.ValidateTOTPCode(secret, code) {
			t.Fatalf("Generated code failed validation against secret")
		}

		if utils.ValidateTOTPCode(secret, "000000") && code != "000000" {
			t.Fatalf("Bogus code should not validate")
		}
	})

	t.Run("TOTP URI generation", func(t *testing.T) {
		uri := utils.TOTPAuthURL("admin@ride.tz", "JBSWY3DPEHPK3PXP", "RideBackend")
		if !strings.HasPrefix(uri, "otpauth://totp/") {
			t.Fatalf("Invalid otpauth URI: %s", uri)
		}
		if !strings.Contains(uri, "secret=JBSWY3DPEHPK3PXP") {
			t.Fatalf("URI missing secret: %s", uri)
		}
	})

	t.Run("HashSHA256 returns consistent 64-char hex", func(t *testing.T) {
		h1 := utils.HashSHA256("test-token-123")
		h2 := utils.HashSHA256("test-token-123")
		if h1 != h2 {
			t.Fatalf("HashSHA256 not deterministic")
		}
		if len(h1) != 64 {
			t.Fatalf("Expected 64-character hex hash, got len %d", len(h1))
		}
	})
}
