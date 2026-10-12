package payments

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestWebhookSignatures(t *testing.T) {
	now := time.Now()
	body := []byte(`{"status":"succeeded"}`)
	stamp := strconv.FormatInt(now.Unix(), 10)
	sign := func(secret string) http.Header {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(stamp + "."))
		mac.Write(body)
		h := http.Header{}
		h.Set("X-Payment-Timestamp", stamp)
		h.Set("X-Payment-Signature", hex.EncodeToString(mac.Sum(nil)))
		return h
	}
	for _, tc := range []struct {
		name, key string
		allow     bool
	}{{"current", "current", true}, {"rotated", "old", true}, {"wrong", "wrong", false}} {
		t.Run(tc.name, func(t *testing.T) {
			err := Verify("current", "old", sign(tc.key), body, now)
			if (err == nil) != tc.allow {
				t.Fatal(err)
			}
		})
	}
	if err := Verify("current", "old", sign("current"), body, now.Add(6*time.Minute)); err == nil {
		t.Fatal("stale signature accepted")
	}
}
