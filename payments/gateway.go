package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GatewayProvider follows the assumed contract documented in README. No secret,
// provider response body or phone is logged or returned in an error.
type GatewayProvider struct {
	URL, Key, Secret, PreviousSecret string
	Client                           *http.Client
}

func (g *GatewayProvider) call(ctx context.Context, method, path string, body any, out any) error {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(g.URL, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.Key)
	req.Header.Set("Content-Type", "application/json")
	if r, ok := body.(Request); ok {
		req.Header.Set("Idempotency-Key", r.IdempotencyKey)
	}
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return errors.New("payment gateway unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("payment gateway rejected request")
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}
func (g *GatewayProvider) InitiateCollection(ctx context.Context, r Request) (Result, error) {
	var out Result
	err := g.call(ctx, "POST", "/collections", r, &out)
	return out, err
}
func (g *GatewayProvider) Disburse(ctx context.Context, r Request) (Result, error) {
	var out Result
	err := g.call(ctx, "POST", "/disbursements", r, &out)
	return out, err
}
func (g *GatewayProvider) Status(ctx context.Context, ref string) (Result, error) {
	var out Result
	err := g.call(ctx, "GET", "/transactions/"+url.PathEscape(ref), nil, &out)
	return out, err
}
func (g *GatewayProvider) Refund(ctx context.Context, ref string, amount int64) (Result, error) {
	var out Result
	err := g.call(ctx, "POST", "/refunds", Request{Reference: ref, Amount: amount, IdempotencyKey: "refund:" + ref}, &out)
	return out, err
}
func (g *GatewayProvider) VerifyWebhook(h http.Header, b []byte) error {
	return Verify(g.Secret, g.PreviousSecret, h, b, time.Now())
}
func (g *GatewayProvider) Records(ctx context.Context, from, to time.Time) ([]Result, error) {
	var out []Result
	err := g.call(ctx, "GET", "/transactions?from="+url.QueryEscape(from.Format(time.RFC3339))+"&to="+url.QueryEscape(to.Format(time.RFC3339)), nil, &out)
	return out, err
}

func (g *GatewayProvider) RefundWithKey(ctx context.Context, ref string, amount int64, key string) (Result, error) {
	var out Result
	err := g.call(ctx, "POST", "/refunds", Request{Reference: ref, Amount: amount, IdempotencyKey: key}, &out)
	return out, err
}
