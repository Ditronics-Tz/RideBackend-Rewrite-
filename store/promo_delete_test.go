package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"ride-backend/models"
	"ride-backend/testutil"
)

func TestDeletePromoHidesOfferAndPreservesReservedDiscounts(t *testing.T) {
	for _, finish := range []string{"confirm", "release"} {
		t.Run(finish, func(t *testing.T) {
			s, _, err := testutil.SetupTestStore()
			if err != nil {
				t.Fatal(err)
			}
			passenger := promoUser(t, s, models.RolePassenger)
			promo := promoFixture()
			if err := s.CreatePromo(&promo, uuid.NewString(), nil); err != nil {
				t.Fatal(err)
			}
			rideID := uuid.NewString()
			reservation, err := s.ReservePromo(passenger.ID, promo.Code, 10000, rideID)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.DeletePromo(promo.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.DeletePromo(promo.ID); err != nil {
				t.Fatalf("retry must succeed: %v", err)
			}
			if _, err := s.GetPromo(promo.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("deleted promo remains readable: %v", err)
			}
			rows, total, err := s.ListPromos("", "", "", 20, 0, time.Now())
			if err != nil || total != 0 || len(rows) != 0 {
				t.Fatalf("deleted promo remains listed: %v", err)
			}
			userPromos, err := s.UserPromos(passenger.ID, time.Now())
			if err != nil || len(userPromos.Granted) != 0 || len(userPromos.Redemptions) != 1 {
				t.Fatalf("passenger visibility/history incorrect: %v", err)
			}
			if _, _, err := s.EvaluatePromo(passenger.ID, promo.Code, 10000, time.Now()); err == nil {
				t.Fatal("deleted promo remains eligible for new use")
			}
			if _, err := s.SetPromoStatus(promo.ID, "active"); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("deleted promotion can be resumed: %v", err)
			}
			if finish == "confirm" {
				err = s.ConfirmPromo(rideID)
			} else {
				err = s.ReleasePromo(rideID)
			}
			if err != nil {
				t.Fatalf("existing reservation cannot finish after deletion: %v", err)
			}
			var stored models.PromoRedemption
			if err := s.DB().First(&stored, "id = ?", reservation.ID).Error; err != nil || stored.DiscountAmount != reservation.DiscountAmount {
				t.Fatalf("redemption history changed: %v", err)
			}
			want := "applied"
			if finish == "release" {
				want = "released"
			}
			if stored.Status != want {
				t.Fatalf("want %s, got %s", want, stored.Status)
			}
		})
	}
}
