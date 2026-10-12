package store_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"math"
	"ride-backend/models"
	"ride-backend/store"
	"ride-backend/testutil"
	"strings"
	"testing"
	"time"
)

func promoUser(t *testing.T, s *store.Store, role models.Role) *models.User {
	t.Helper()
	u, err := s.CreateUser("Promo user", uuid.NewString()+"@example.test", "unused", role)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
func promoFixture() models.PromoCode {
	now := time.Now().UTC()
	return models.PromoCode{ID: uuid.NewString(), Code: strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:12]), Title: "Test promo", DiscountType: "percent", DiscountValue: 20, MinFare: 1000, Audience: "all", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), MaxPerUser: 1, Status: "active", Source: "admin"}
}
func int64ptr(value int64) *int64 { return &value }
func TestEvaluatePromo(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cases := []struct {
		name    string
		change  func(*models.PromoCode)
		setup   string
		fare    int64
		want    int64
		message string
	}{
		{name: "percent", fare: 10000, want: 2000},
		{name: "case normalized", fare: 10000, want: 2000},
		{name: "unknown", setup: "unknown", fare: 10000, message: "invalid or unavailable code"},
		{name: "draft", change: func(p *models.PromoCode) { p.Status = "draft" }, fare: 10000, message: "invalid or unavailable code"},
		{name: "paused", change: func(p *models.PromoCode) { p.Status = "paused" }, fare: 10000, message: "invalid or unavailable code"},
		{name: "archived", change: func(p *models.PromoCode) { p.Status = "archived" }, fare: 10000, message: "invalid or unavailable code"},
		{name: "scheduled", change: func(p *models.PromoCode) { p.StartsAt = now.Add(time.Minute) }, fare: 10000, message: "code is not active yet"},
		{name: "expired boundary", change: func(p *models.PromoCode) { p.ExpiresAt = now }, fare: 10000, message: "code has expired"},
		{name: "selected unauthorized", change: func(p *models.PromoCode) { p.Audience = "selected" }, fare: 10000, message: "invalid or unavailable code"},
		{name: "selected expired hidden", change: func(p *models.PromoCode) { p.Audience = "selected"; p.ExpiresAt = now }, fare: 10000, message: "invalid or unavailable code"},
		{name: "selected limited hidden", change: func(p *models.PromoCode) {
			p.Audience = "selected"
			p.MaxRedemptions = int64ptr(1)
			p.RedeemedCount = 1
		}, fare: 10000, message: "invalid or unavailable code"},
		{name: "selected granted", change: func(p *models.PromoCode) { p.Audience = "selected" }, setup: "grant", fare: 10000, want: 2000},
		{name: "per-user reserved", setup: "reserved", fare: 10000, message: "per-user limit reached"},
		{name: "per-user applied", setup: "applied", fare: 10000, message: "per-user limit reached"},
		{name: "released excluded", setup: "released", fare: 10000, want: 2000},
		{name: "total limit", change: func(p *models.PromoCode) { p.MaxRedemptions = int64ptr(5); p.RedeemedCount = 5 }, fare: 10000, message: "total limit reached"},
		{name: "minimum fare", fare: 999, message: "minimum fare not reached"},
		{name: "negative fare", fare: -1, message: "fare must not be negative"},
		{name: "first ride used", change: func(p *models.PromoCode) { p.FirstRideOnly = true }, setup: "completed", fare: 10000, message: "code is only valid for the first completed ride"},
		{name: "first ride unused", change: func(p *models.PromoCode) { p.FirstRideOnly = true }, fare: 10000, want: 2000},
		{name: "percent cap", change: func(p *models.PromoCode) { p.MaxDiscount = int64ptr(500) }, fare: 10000, want: 500},
		{name: "fixed above fare", change: func(p *models.PromoCode) { p.DiscountType = "fixed"; p.DiscountValue = 50000 }, fare: 10000, want: 10000},
		{name: "round down", change: func(p *models.PromoCode) { p.MinFare = 0; p.DiscountValue = 33 }, fare: 101, want: 33},
		{name: "int64 overflow safe", change: func(p *models.PromoCode) { p.DiscountValue = 100 }, fare: math.MaxInt64, want: math.MaxInt64},
		{name: "zero fare", change: func(p *models.PromoCode) { p.MinFare = 0 }, fare: 0, want: 0},
		{name: "driver forbidden", setup: "driver", fare: 10000, message: "invalid or unavailable code"},
		{name: "inactive forbidden", setup: "inactive", fare: 10000, message: "invalid or unavailable code"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			role := models.RolePassenger
			if tc.setup == "driver" {
				role = models.RoleDriver
			}
			u := promoUser(t, s, role)
			p := promoFixture()
			if tc.change != nil {
				tc.change(&p)
			}
			if err := s.DB().Create(&p).Error; err != nil {
				t.Fatal(err)
			}
			switch tc.setup {
			case "grant":
				if err := s.DB().Create(&models.PromoAudience{PromoID: p.ID, UserID: u.ID, GrantedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
			case "reserved", "applied", "released":
				if err := s.DB().Create(&models.PromoRedemption{ID: uuid.NewString(), PromoID: p.ID, UserID: u.ID, Status: tc.setup}).Error; err != nil {
					t.Fatal(err)
				}
			case "completed":
				if err := s.OnRideCompleted(uuid.NewString(), u.ID, 10000); err != nil {
					t.Fatal(err)
				}
			case "inactive":
				s.DB().Model(u).Update("is_active", false)
			}
			code := strings.ToLower(p.Code)
			if tc.setup == "unknown" {
				code = "NONEXISTENT"
			}
			amount, got, err := s.EvaluatePromo(u.ID, code, tc.fare, now)
			if tc.message != "" {
				if err == nil || err.Error() != tc.message {
					t.Fatalf("want %q, got %v", tc.message, err)
				}
				if got != nil {
					t.Fatal("Rejected code exposed promo")
				}
				return
			}
			if err != nil || amount != tc.want || got == nil {
				t.Fatalf("want %d got %d promo=%v err=%v", tc.want, amount, got, err)
			}
		})
	}
}

func TestPromoReservationLifecycleAndSequentialGuard(t *testing.T) {
	// SQLite's single writer cannot reliably exercise PostgreSQL row-lock races.
	// Test the exact SQL guard sequentially here; the opt-in PG test runs goroutines.
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	p := promoFixture()
	p.MaxRedemptions = int64ptr(5)
	if err := s.DB().Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	successes := 0
	var first *models.PromoRedemption
	for i := 0; i < 20; i++ {
		u := promoUser(t, s, models.RolePassenger)
		r, err := s.ReservePromo(u.ID, p.Code, 10000, uuid.NewString())
		if err == nil {
			successes++
			if first == nil {
				first = r
			}
		}
	}
	if successes != 5 {
		t.Fatalf("want five successful reservations, got %d", successes)
	}
	if err := s.ConfirmPromo(*first.RideID); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmPromo(*first.RideID); err != nil {
		t.Fatal("Confirm not idempotent", err)
	}
	if err := s.ReleasePromo(*first.RideID); err == nil {
		t.Fatal("Applied discount was released")
	}
	var reserved models.PromoRedemption
	s.DB().Where("promo_id = ? AND status = ?", p.ID, "reserved").First(&reserved)
	if err := s.ReleasePromo(*reserved.RideID); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleasePromo(*reserved.RideID); err != nil {
		t.Fatal(err)
	}
	u := promoUser(t, s, models.RolePassenger)
	id := uuid.NewString()
	r, err := s.ReservePromo(u.ID, p.Code, 10000, id)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.ReservePromo(u.ID, p.Code, 10000, id)
	if err != nil || again.ID != r.ID {
		t.Fatal("Reserve retry not idempotent", err)
	}
	if _, err := s.ReservePromo(u.ID, p.Code, 10000, uuid.NewString()); err == nil {
		t.Fatal("Per-user limit bypassed")
	}
	p2 := promoFixture()
	s.DB().Create(&p2)
	if _, err := s.ReservePromo(u.ID, p2.Code, 10000, id); err == nil {
		t.Fatal("Two promos reserved for one ride")
	}
	locked := map[string]json.RawMessage{"discount_value": json.RawMessage("25")}
	if _, err := s.UpdatePromo(p.ID, locked); err == nil {
		t.Fatal("Used discount terms changed")
	}
	if _, err := s.UpdatePromo(p.ID, map[string]json.RawMessage{"title": json.RawMessage(`"Renamed"`)}); err != nil {
		t.Fatal(err)
	}
}

func TestPromoValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*models.PromoCode)
	}{
		{"bad code", func(p *models.PromoCode) { p.Code = "bad code!" }}, {"short code", func(p *models.PromoCode) { p.Code = "ABC" }},
		{"percent high", func(p *models.PromoCode) { p.DiscountValue = 101 }}, {"zero value", func(p *models.PromoCode) { p.DiscountValue = 0 }},
		{"negative min", func(p *models.PromoCode) { p.MinFare = -1 }}, {"bad type", func(p *models.PromoCode) { p.DiscountType = "random" }},
		{"bad audience", func(p *models.PromoCode) { p.Audience = "nobody" }}, {"bad times", func(p *models.PromoCode) { p.ExpiresAt = p.StartsAt }},
		{"zero peruser", func(p *models.PromoCode) { p.MaxPerUser = 0 }}, {"zero cap", func(p *models.PromoCode) { p.MaxDiscount = int64ptr(0) }},
		{"zero total", func(p *models.PromoCode) { p.MaxRedemptions = int64ptr(0) }}, {"computed status stored", func(p *models.PromoCode) { p.Status = "expired" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := promoFixture()
			tc.change(&p)
			if err := store.ValidatePromo(&p); err == nil {
				t.Fatal("Invalid promo accepted")
			}
		})
	}
}
