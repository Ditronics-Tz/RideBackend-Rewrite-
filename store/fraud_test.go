package store_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"ride-backend/models"
	"ride-backend/store"
	"ride-backend/testutil"
	"testing"
	"time"
)

func TestFraudRulesPositiveAndNegative(t *testing.T) {
	for _, key := range []string{"shared_device", "duplicate_identifiers", "cancellation_abuse", "location_mismatch", "collusion", "rating_manipulation", "promo_referral_abuse", "payout_anomaly"} {
		for _, positive := range []bool{false, true} {
			name := "negative"
			if positive {
				name = "positive"
			}
			t.Run(key+"/"+name, func(t *testing.T) {
				s, cfg, err := testutil.SetupTestStore()
				if err != nil {
					t.Fatal(err)
				}
				cfg.DeviceHashPepper = "offline-test-pepper"
				s.ConfigureOperations(cfg)
				now := time.Now().UTC().Add(time.Minute)
				s.Clock = func() time.Time { return now }
				user, err := s.CreateUser("Test driver", uuid.NewString()+"@test.invalid", "", models.RoleDriver)
				if err != nil {
					t.Fatal(err)
				}
				other, err := s.CreateUser("Test passenger", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
				if err != nil {
					t.Fatal(err)
				}
				save := func(v any) {
					t.Helper()
					if err := s.DB().Create(v).Error; err != nil {
						t.Fatal(err)
					}
				}
				n := 2
				if positive {
					n = 12
				}
				switch key {
				case "shared_device":
					if err := s.ObserveDevice(user.ID, "family-device", "android", "127.0.0.1"); err != nil {
						t.Fatal(err)
					}
					count := 2
					if positive {
						count = 4
					}
					for i := 1; i < count; i++ {
						u, err := s.CreateUser("Family", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
						if err != nil {
							t.Fatal(err)
						}
						if err := s.ObserveDevice(u.ID, "family-device", "ios", "127.0.0.1"); err != nil {
							t.Fatal(err)
						}
					}
				case "duplicate_identifiers":
					phone := "+255712345678"
					otherPhone := "+255713000000"
					if positive {
						otherPhone = "+255 712 345 678"
					}
					if err := s.DB().Model(&models.User{}).Where("id = ?", user.ID).Update("phone", phone).Error; err != nil {
						t.Fatal(err)
					}
					if err := s.DB().Model(&models.User{}).Where("id = ?", other.ID).Update("phone", otherPhone).Error; err != nil {
						t.Fatal(err)
					}
				case "cancellation_abuse", "collusion":
					for i := 0; i < n; i++ {
						r := models.Ride{ID: uuid.NewString(), DriverID: user.ID, PassengerID: other.ID, Status: "completed", Fare: 25000, ExpectedDistance: 200, CreatedAt: now, AcceptedAt: &now, CompletedAt: &now}
						if key == "cancellation_abuse" {
							r.Status = "cancelled"
							r.CancelledBy = &user.ID
						}
						save(&r)
					}
				case "location_mismatch":
					r := models.Ride{ID: uuid.NewString(), DriverID: user.ID, PassengerID: other.ID, Status: "completed", ExpectedDistance: 2000, CreatedAt: now}
					save(&r)
					for i := 0; i < 3; i++ {
						lng := 39.2 + float64(i)*0.01
						if positive {
							lng = 39.2
						}
						save(&models.RideLocationPoint{ID: uuid.NewString(), RideID: r.ID, UserID: user.ID, Lat: -6.8, Lng: lng, Accuracy: 5, At: now.Add(time.Duration(i) * time.Minute)})
					}
				case "rating_manipulation":
					for i := 0; i < n; i++ {
						from, to := user.ID, other.ID
						if i%2 == 0 {
							from, to = to, from
						}
						save(&models.Rating{ID: uuid.NewString(), FromUserID: from, ToUserID: to, Score: 5, CreatedAt: now})
					}
				case "promo_referral_abuse":
					if err := s.ObserveDevice(user.ID, "referral-device", "web", "127.0.0.1"); err != nil {
						t.Fatal(err)
					}
					for i := 0; i < n; i++ {
						save(&models.Referral{ID: uuid.NewString(), ReferrerID: user.ID, RefereeID: uuid.NewString(), Status: "pending", CreatedAt: now})
					}
				case "payout_anomaly":
					save(&models.DriverPayout{ID: uuid.NewString(), DriverID: user.ID, Amount: 1000, Status: "requested", IdempotencyKey: uuid.NewString(), CreatedAt: now})
					for i := 0; i < n; i++ {
						save(&models.PaymentTransaction{ID: uuid.NewString(), DriverID: &user.ID, PayerID: other.ID, Method: "cash", Status: "succeeded", Amount: 5000, IdempotencyKey: uuid.NewString(), CreatedAt: now.Add(-time.Minute)})
					}
				}
				if err := s.EvaluateFraud(context.Background(), models.FraudEvent{UserID: user.ID, Type: "nightly"}); err != nil {
					t.Fatal(err)
				}
				var count int64
				if err := s.DB().Model(&models.FraudFlag{}).Where("user_id = ? AND rule_key = ?", user.ID, key).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				expected := int64(0)
				if positive {
					expected = 1
				}
				if count != expected {
					t.Fatalf("flags=%d, want %d", count, expected)
				}
			})
		}
	}
}

func TestSharedDeviceThresholdAppliesToEachDevice(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DeviceHashPepper = "test"
	s.ConfigureOperations(cfg)
	user, err := s.CreateUser("Owner", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	for _, device := range []string{"family-phone", "family-tablet"} {
		if err := s.ObserveDevice(user.ID, device, "android", ""); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			other, err := s.CreateUser("Family", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.ObserveDevice(other.ID, device, "android", ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.EvaluateFraud(context.Background(), models.FraudEvent{UserID: user.ID, Type: "nightly"}); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := s.DB().Model(&models.FraudFlag{}).Where("user_id = ? AND rule_key = ?", user.ID, "shared_device").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("three accounts per device should be allowed; got %d flags", count)
	}
	other, err := s.CreateUser("Fourth account", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ObserveDevice(other.ID, "family-phone", "android", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.EvaluateFraud(context.Background(), models.FraudEvent{UserID: user.ID, Type: "nightly"}); err != nil {
		t.Fatal(err)
	}
	var flag models.FraudFlag
	if err := s.DB().Where("user_id = ? AND rule_key = ?", user.ID, "shared_device").First(&flag).Error; err != nil {
		t.Fatal(err)
	}
	var linked []string
	if err := json.Unmarshal([]byte(flag.RelatedUserIDs), &linked); err != nil {
		t.Fatal(err)
	}
	if len(linked) != 3 {
		t.Fatalf("only accounts from the over-threshold device should be linked, got %v", linked)
	}
}

func TestFraudDedupeSuppressionDryRunPermissionsAndMitigations(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DeviceHashPepper = "test"
	s.ConfigureOperations(cfg)
	now := time.Now().UTC().Add(time.Minute)
	s.Clock = func() time.Time { return now }
	users := []*models.User{}
	for i := 0; i < 4; i++ {
		u, err := s.CreateUser("Family", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, u)
		if err := s.ObserveDevice(u.ID, "same", "android", "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
	}
	rules, err := s.FraudRules()
	if err != nil {
		t.Fatal(err)
	}
	var rule models.FraudRule
	for _, r := range rules {
		if r.Key == "shared_device" {
			rule = r
		}
	}
	rule.Severity = "high"
	rule.AutoMitigation = "payout_hold,promo_block"
	admin := &models.StaffUser{ID: uuid.NewString(), Role: models.StaffRoleAdmin}
	support := &models.StaffUser{ID: uuid.NewString(), Role: models.StaffRoleSupport}
	if err := s.UpdateFraudRules(admin.ID, []models.FraudRule{rule}); err != nil {
		t.Fatal(err)
	}
	before := map[string]int64{}
	for _, table := range []string{"fraud_flags", "fraud_actions", "fraud_mitigations", "fraud_notes", "fraud_rules", "staff_audit_logs"} {
		var n int64
		if err := s.DB().Table(table).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		before[table] = n
	}
	count, err := s.DryRunFraud("shared_device")
	if err != nil || count != 4 {
		t.Fatal(count, err)
	}
	for table, n := range before {
		var after int64
		if err := s.DB().Table(table).Count(&after).Error; err != nil {
			t.Fatal(err)
		}
		if n != after {
			t.Fatal("dry run wrote", table)
		}
	}
	for i := 0; i < 2; i++ {
		if err := s.EvaluateFraud(context.Background(), models.FraudEvent{UserID: users[0].ID}); err != nil {
			t.Fatal(err)
		}
	}
	var flags []models.FraudFlag
	if err := s.DB().Where("user_id = ?", users[0].ID).Find(&flags).Error; err != nil {
		t.Fatal(err)
	}
	if len(flags) != 1 {
		t.Fatalf("dedupe flags=%d", len(flags))
	}
	held, err := s.HasFraudMitigation(users[0].ID, "payout_hold")
	if err != nil || !held {
		t.Fatal("missing payout hold", err)
	}
	promo := promoFixture()
	if err := s.DB().Create(&promo).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EvaluatePromo(users[0].ID, promo.Code, 5000, now); err == nil {
		t.Fatal("promo mitigation not enforced")
	}
	if err := s.WorkFraud(flags[0].ID, "assign", support, store.FraudWork{}); err != nil {
		t.Fatal("support must triage a high flag", err)
	}
	if err := s.WorkFraud(flags[0].ID, "note", support, store.FraudWork{Note: "Reviewed linked activity"}); err != nil {
		t.Fatal("support must note a high flag", err)
	}
	if err := s.WorkFraud(flags[0].ID, "resolve", support, store.FraudWork{Status: "dismissed", Note: "Investigated"}); err == nil {
		t.Fatal("support resolved high flag")
	}
	if err := s.WorkFraud(flags[0].ID, "resolve", admin, store.FraudWork{Status: "dismissed", Note: "Family device"}); err != nil {
		t.Fatal(err)
	}
	held, err = s.HasFraudMitigation(users[0].ID, "payout_hold")
	if err != nil || held {
		t.Fatal("hold not released", err)
	}
	if err := s.EvaluateFraud(context.Background(), models.FraudEvent{UserID: users[0].ID}); err != nil {
		t.Fatal(err)
	}
	var total int64
	if err := s.DB().Model(&models.FraudFlag{}).Count(&total).Error; err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatal("dismissal suppression ignored")
	}
	now = now.AddDate(0, 0, 31)
	for _, u := range users {
		if err := s.ObserveDevice(u.ID, "same", "android", "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.EvaluateFraud(context.Background(), models.FraudEvent{UserID: users[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().Model(&models.FraudFlag{}).Count(&total).Error; err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatal("suppression did not expire")
	}
}

func TestFraudMissingDataAndHaversine(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"shared_device", "location_mismatch", "rating_manipulation", "payout_anomaly"} {
		available, reason, err := s.RuleAvailability(key)
		if err != nil || available || reason == "" {
			t.Fatalf("%s unavailable status: %v %q %v", key, available, reason, err)
		}
	}
	if store.Haversine(-6.8, 39.2, -6.8, 39.2) != 0 {
		t.Fatal("zero distance")
	}
	d := store.Haversine(0, 0, 0, 1)
	if d < 111000 || d > 112000 {
		t.Fatal(d)
	}
}
