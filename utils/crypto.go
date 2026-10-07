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
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secretBase32)))
	if err != nil {
		secret, err = base32.StdEncoding.DecodeString(strings.ToUpper(strings.TrimSpace(secretBase32)))
		if err != nil {
			return "", fmt.Errorf("invalid base32 secret: %w", err)
		}
	}

	counter := uint64(t.Unix() / 30)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

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

// ValidateTOTPCode checks if a 6-digit code matches the secret with a +/-1 time window (30s).
func ValidateTOTPCode(secretBase32, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}

	now := time.Now()
	intervals := []time.Time{
		now,
		now.Add(-30 * time.Second),
		now.Add(30 * time.Second),
	}

	for _, t := range intervals {
		expected, err := GenerateTOTPCode(secretBase32, t)
		if err == nil && subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return true
		}
	}
	return false
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
