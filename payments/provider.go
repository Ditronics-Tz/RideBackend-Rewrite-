package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type Request struct {
	Amount         int64  `json:"amount"`
	Phone          string `json:"phone"`
	Method         string `json:"method"`
	Reference      string `json:"reference"`
	IdempotencyKey string `json:"idempotency_key"`
}
type Result struct {
	Reference   string `json:"reference"`
	Status      string `json:"status"`
	Amount      int64  `json:"amount"`
	FailureCode string `json:"failure_code"`
}
type Provider interface {
	InitiateCollection(context.Context, Request) (Result, error)
	Status(context.Context, string) (Result, error)
	Refund(context.Context, string, int64) (Result, error)
	Disburse(context.Context, Request) (Result, error)
	VerifyWebhook(http.Header, []byte) error
}

// Refunds require a distinct key per partial refund, including across retries.
type IdempotentRefundProvider interface {
	RefundWithKey(context.Context, string, int64, string) (Result, error)
}
type RecordsProvider interface {
	Records(context.Context, time.Time, time.Time) ([]Result, error)
}

func Verify(secret, previous string, headers http.Header, body []byte, now time.Time) error {
	stamp := headers.Get("X-Payment-Timestamp")
	ts, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || now.Sub(time.Unix(ts, 0)) > 5*time.Minute || time.Unix(ts, 0).Sub(now) > 5*time.Minute {
		return errors.New("invalid webhook signature")
	}
	supplied, err := hex.DecodeString(headers.Get("X-Payment-Signature"))
	if err != nil {
		return errors.New("invalid webhook signature")
	}
	for _, key := range []string{secret, previous} {
		if key == "" {
			continue
		}
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(stamp + "."))
		mac.Write(body)
		if hmac.Equal(supplied, mac.Sum(nil)) {
			return nil
		}
	}
	return errors.New("invalid webhook signature")
}

type MockProvider struct {
	Mode           string
	Secret         string
	PreviousSecret string
	mu             sync.Mutex
	rows           map[string]Result
}

func (m *MockProvider) start(r Request) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]Result{}
	}
	if saved, ok := m.rows[r.Reference]; ok {
		return saved, nil
	}
	result := Result{Reference: r.Reference, Status: "pending", Amount: r.Amount}
	m.rows[r.Reference] = result
	if m.Mode == "timeout" {
		return result, context.DeadlineExceeded
	}
	return result, nil
}
func (m *MockProvider) InitiateCollection(_ context.Context, r Request) (Result, error) {
	return m.start(r)
}
func (m *MockProvider) Disburse(_ context.Context, r Request) (Result, error) { return m.start(r) }
func (m *MockProvider) Status(_ context.Context, ref string) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[ref]
	if !ok {
		return Result{}, errors.New("reference not found")
	}
	switch m.Mode {
	case "timeout":
		return r, context.DeadlineExceeded
	case "pending":
		r.Status = "pending"
	case "failure":
		r.Status = "failed"
		r.FailureCode = "mock_declined"
	default:
		r.Status = "succeeded"
	}
	return r, nil
}
func (m *MockProvider) Refund(_ context.Context, ref string, amount int64) (Result, error) {
	return Result{Reference: ref, Status: "succeeded", Amount: amount}, nil
}
func (m *MockProvider) VerifyWebhook(h http.Header, b []byte) error {
	return Verify(m.Secret, m.PreviousSecret, h, b, time.Now())
}
func (m *MockProvider) Records(_ context.Context, _, _ time.Time) ([]Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Result{}
	for _, r := range m.rows {
		out = append(out, r)
	}
	return out, nil
}

func (m *MockProvider) RefundWithKey(ctx context.Context, ref string, amount int64, key string) (Result, error) {
	return m.start(Request{Reference: key, Amount: amount, IdempotencyKey: key})
}
