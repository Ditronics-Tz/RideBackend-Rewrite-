package utils

import (
	"crypto/rand"
	"io"
	"strings"
)

const recoveryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GenerateRecoveryCodes returns display codes and their SHA-256 hashes.
// Only the hashes may be stored. Each display code is shown once.
func GenerateRecoveryCodes(count int) (display []string, hashes []string, err error) {
	if count < 1 || count > 20 {
		count = 8
	}
	display = make([]string, count)
	hashes = make([]string, count)
	buf := make([]byte, 16)
	for i := 0; i < count; i++ {
		if _, err = io.ReadFull(rand.Reader, buf); err != nil {
			return nil, nil, err
		}
		chars := make([]byte, 16)
		for n := range chars {
			chars[n] = recoveryAlphabet[int(buf[n])%len(recoveryAlphabet)]
		}
		plain := string(chars)
		display[i] = plain[0:4] + "-" + plain[4:8] + "-" + plain[8:12] + "-" + plain[12:16]
		hashes[i] = HashSHA256(NormalizeRecoveryCode(display[i]))
	}
	return display, hashes, nil
}

// NormalizeRecoveryCode strips separators so users can type codes with or without hyphens.
func NormalizeRecoveryCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	return code
}
