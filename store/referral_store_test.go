package store_test

import (
	"fmt"
	"github.com/google/uuid"
	"ride-backend/models"
	"ride-backend/store"
	"ride-backend/testutil"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var testPhoneSequence atomic.Int64

func verifiedPromoUser(t *testing.T, s *store.Store, role models.Role) *models.User {
	t.Helper()
	u := promoUser(t, s, role)
	now := time.Now().UTC()
	u.Phone = fmt.Sprintf("+2557%08d", testPhoneSequence.Add(1))
	u.PhoneVerifiedAt = &now
	if err := s.DB().Model(u).Updates(map[string]interface{}{"phone": u.Phone, "phone_verified_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	return u
}
func enableTestReferrals(t *testing.T, s *store.Store) {
	t.Helper()
	_, err := s.UpdateReferralSettings(models.ReferralSettings{Enabled: true, RefereeRewardType: "fixed", RefereeRewardValue: 1000, ReferrerRewardType: "fixed", ReferrerRewardValue: 2000, QualifyingMinFare: 3000, MaxRewardsPerReferrer: 2, RewardValidDays: 30})
	if err != nil {
		t.Fatal(err)
	}
}
func TestReferralAbuse(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"self", "inactive referrer", "old account", "completed ride", "already applied", "unverified phone", "disabled", "driver referee", "reward limit", "recycled phone"} {
		t.Run(name, func(t *testing.T) {
			enableTestReferrals(t, s)
			inviter := verifiedPromoUser(t, s, models.RolePassenger)
			invited := verifiedPromoUser(t, s, models.RolePassenger)
			code, err := s.GetOrCreateReferralCode(inviter.ID)
			if err != nil {
				t.Fatal(err)
			}
			again, err := s.GetOrCreateReferralCode(inviter.ID)
			if err != nil || again.Code != code.Code || len(code.Code) != 8 || strings.ContainsAny(code.Code, "01OI") {
				t.Fatal("invalid or unstable referral code", err)
			}
			switch name {
			case "self":
				invited = inviter
			case "inactive referrer":
				s.DB().Model(inviter).Update("status", "suspended")
			case "old account":
				s.DB().Model(invited).Update("created_at", time.Now().Add(-8*24*time.Hour))
			case "completed ride":
				if err := s.OnRideCompleted(uuid.NewString(), invited.ID, 5000); err != nil {
					t.Fatal(err)
				}
			case "already applied":
				if _, err := s.ApplyReferralCode(invited.ID, code.Code); err != nil {
					t.Fatal(err)
				}
			case "unverified phone":
				s.DB().Model(invited).Update("phone_verified_at", nil)
			case "disabled":
				s.DB().Model(&models.ReferralSettings{}).Where("id = 1").Update("enabled", false)
			case "driver referee":
				s.DB().Model(invited).Update("role", models.RoleDriver)
			case "reward limit":
				for i := 0; i < 2; i++ {
					if err := s.DB().Create(&models.Referral{ID: uuid.NewString(), ReferrerID: inviter.ID, RefereeID: uuid.NewString(), Status: "rewarded", ReferrerRewardStatus: "issued"}).Error; err != nil {
						t.Fatal(err)
					}
				}
			case "recycled phone":
				if err := s.DB().Create(&models.ReferralRewardPhone{Phone: invited.Phone, ReferralID: uuid.NewString()}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.ApplyReferralCode(invited.ID, code.Code); err == nil {
				t.Fatal("abuse case accepted")
			}
		})
	}
}
func TestReferralRewardsAndCompletionIdempotency(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []models.Role{models.RolePassenger, models.RoleDriver} {
		t.Run(string(role), func(t *testing.T) {
			enableTestReferrals(t, s)
			inviter := verifiedPromoUser(t, s, role)
			invited := verifiedPromoUser(t, s, models.RolePassenger)
			code, err := s.GetOrCreateReferralCode(inviter.ID)
			if err != nil {
				t.Fatal(err)
			}
			ref, err := s.ApplyReferralCode(invited.ID, strings.ToLower(code.Code))
			if err != nil {
				t.Fatal(err)
			}
			ride := uuid.NewString()
			for i := 0; i < 2; i++ {
				if err := s.OnRideCompleted(ride, invited.ID, 5000); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.DB().First(ref, "id = ?", ref.ID).Error; err != nil {
				t.Fatal(err)
			}
			want := "issued"
			if role == models.RoleDriver {
				want = "pending_wallet"
			}
			if ref.Status != "rewarded" || ref.ReferrerRewardStatus != want || ref.QualifyingRideID == nil || *ref.QualifyingRideID != ride {
				t.Fatalf("unexpected referral: %+v", ref)
			}
			var grants []models.PromoAudience
			if err := s.DB().Where("user_id IN ?", []string{inviter.ID, invited.ID}).Find(&grants).Error; err != nil {
				t.Fatal(err)
			}
			count := 2
			if role == models.RoleDriver {
				count = 1
			}
			if len(grants) != count {
				t.Fatalf("want %d rewards got %d", count, len(grants))
			}
			outsider := promoUser(t, s, models.RolePassenger)
			for _, grant := range grants {
				p, err := s.GetPromo(grant.PromoID)
				if err != nil {
					t.Fatal(err)
				}
				if p.Source != "referral_reward" || p.Audience != "selected" || p.MaxRedemptions == nil || *p.MaxRedemptions != 1 || p.MaxPerUser != 1 || p.ExpiresAt.Sub(p.StartsAt) != 30*24*time.Hour {
					t.Fatalf("invalid reward %+v", p)
				}
				if _, _, err := s.EvaluatePromo(outsider.ID, p.Code, 5000, time.Now()); err == nil || err.Error() != "invalid or unavailable code" {
					t.Fatal("reward leaked")
				}
				if _, err := s.ReservePromo(grant.UserID, p.Code, 5000, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
				if _, err := s.ReservePromo(grant.UserID, p.Code, 5000, uuid.NewString()); err == nil {
					t.Fatal("reward reused")
				}
			}
			if err := s.OnRideCompleted(ride, outsider.ID, 5000); err == nil {
				t.Fatal("completion ride reused by another passenger")
			}
		})
	}
}
func TestReferralFirstRideMustQualify(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	enableTestReferrals(t, s)
	a := verifiedPromoUser(t, s, models.RolePassenger)
	b := verifiedPromoUser(t, s, models.RolePassenger)
	c, err := s.GetOrCreateReferralCode(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.ApplyReferralCode(b.ID, c.Code)
	if err != nil {
		t.Fatal(err)
	}
	for _, fare := range []int64{2999, 5000} {
		if err := s.OnRideCompleted(uuid.NewString(), b.ID, fare); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DB().First(r, "id = ?", r.ID).Error; err != nil {
		t.Fatal(err)
	}
	if r.Status != "rejected" || r.RejectReason == "" {
		t.Fatalf("nonqualifying first ride rewarded: %+v", r)
	}
}
