package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// EncryptAESGCM encrypts plaintext using AES-256-GCM and a 32-byte key.
// Returns a hex-encoded string containing nonce + ciphertext.
func EncryptAESGCM(plaintext string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", errors.New("encryption key must be exactly 32 bytes")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

// DecryptAESGCM decrypts a hex-encoded ciphertext using AES-256-GCM.
func DecryptAESGCM(ciphertextHex string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", errors.New("encryption key must be exactly 32 bytes")
	}

	data, err := hex.DecodeString(ciphertextHex)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// GenerateTOTPSecret generates a random base32 encoded secret (RFC 6238).
func GenerateTOTPSecret() (string, error) {
	bytes := make([]byte, 20) // 160 bits
	if _, err := io.ReadFull(rand.Reader, bytes); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(bytes), nil
}

// GenerateTOTPCode generates a 6-digit TOTP code for a secret at time t.
func GenerateTOTPCode(secretBase32 string, t time.Time) (string, error) {
	return generateTOTPAtStep(secretBase32, t.Unix()/30)
}

func generateTOTPAtStep(secretBase32 string, counter int64) (string, error) {
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secretBase32)))
	if err != nil {
		secret, err = base32.StdEncoding.DecodeString(strings.ToUpper(strings.TrimSpace(secretBase32)))
		if err != nil {
			return "", fmt.Errorf("invalid base32 secret: %w", err)
		}
	}
	if len(secret) < 20 {
		return "", errors.New("totp secret must be at least 160 bits")
	}

	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(counter))

	mac := hmac.New(sha1.New, secret)
	mac.Write(buf)
	h := mac.Sum(nil)

	offset := h[len(h)-1] & 0x0f
	codeInt := ((int(h[offset]) & 0x7f) << 24) |
		((int(h[offset+1]) & 0xff) << 16) |
		((int(h[offset+2]) & 0xff) << 8) |
		(int(h[offset+3]) & 0xff)

	otp := codeInt % 1000000
	return fmt.Sprintf("%06d", otp), nil
}

// MatchTOTPStep accepts the current 30-second step and one step on either side.
// The matched step is recorded so the same step cannot be reused.
func MatchTOTPStep(secretBase32, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	base := now.Unix() / 30
	var matched int64
	ok := 0
	for _, delta := range []int64{0, -1, 1} {
		step := base + delta
		expected, err := generateTOTPAtStep(secretBase32, step)
		match := 0
		if err == nil && len(expected) == len(code) {
			match = subtle.ConstantTimeCompare([]byte(expected), []byte(code))
		}
		if match == 1 && ok == 0 {
			matched = step
		}
		ok |= match
	}
	return matched, ok == 1
}

// ValidateTOTPCode checks if a 6-digit code matches the secret with a +/-1 time window (30s).
func ValidateTOTPCode(secretBase32, code string) bool {
	_, ok := MatchTOTPStep(secretBase32, code, time.Now())
	return ok
}

// TOTPAuthURL returns an otpauth:// URI for QR code generation.
func TOTPAuthURL(account, secret, issuer string) string {
	return fmt.Sprintf(
		"otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30",
		url.PathEscape(issuer),
		url.PathEscape(account),
		secret,
		url.QueryEscape(issuer),
	)
}

// HashSHA256 returns hex-encoded SHA-256 hash of a string.
func HashSHA256(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}
