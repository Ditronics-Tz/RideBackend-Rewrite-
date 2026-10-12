package store

import (
	"crypto/rand"
	"errors"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"math/big"
	backenddb "ride-backend/db"
	"ride-backend/models"
	"sort"
	"strings"
	"time"
)

func defaultReferralSettings() models.ReferralSettings {
	return models.ReferralSettings{ID: 1, RefereeRewardType: "fixed", ReferrerRewardType: "fixed", MaxRewardsPerReferrer: 10, RewardValidDays: 30}
}
func (s *Store) GetReferralSettings() (*models.ReferralSettings, error) {
	result := defaultReferralSettings()
	err := s.db.First(&result, "id = ?", 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &result, nil
	}
	return &result, err
}
func (s *Store) UpdateReferralSettings(input models.ReferralSettings) (*models.ReferralSettings, error) {
	input.ID = 1
	for _, reward := range []struct {
		kind  string
		value int64
		field string
	}{{input.RefereeRewardType, input.RefereeRewardValue, "referee_reward"}, {input.ReferrerRewardType, input.ReferrerRewardValue, "referrer_reward"}} {
		if reward.kind != "fixed" && reward.kind != "percent" {
			return nil, promoInvalid(reward.field+"_type", "reward type must be fixed or percent")
		}
		if reward.value < 0 || (input.Enabled && reward.value == 0) || (reward.kind == "percent" && (reward.value < 1 || reward.value > 100)) {
			return nil, promoInvalid(reward.field+"_value", "enabled rewards must be positive; percent must be 1-100")
		}
	}
	if input.QualifyingMinFare < 0 {
		return nil, promoInvalid("qualifying_min_fare", "minimum fare must not be negative")
	}
	if input.MaxRewardsPerReferrer < 1 {
		return nil, promoInvalid("max_rewards_per_referrer", "max_rewards_per_referrer must be at least 1")
	}
	if input.RewardValidDays < 1 || input.RewardValidDays > 3650 {
		return nil, promoInvalid("reward_valid_days", "reward_valid_days must be 1-3650")
	}
	err := s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&input).Error
	return &input, err
}
func randomRewardCode() (string, error) {
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	value := make([]byte, 8)
	for i := range value {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		value[i] = alphabet[n.Int64()]
	}
	return string(value), nil
}
func (s *Store) GetOrCreateReferralCode(userID string) (*models.UserReferralCode, error) {
	var result models.UserReferralCode
	err := s.Transaction(func(sc *Store) error {
		var user models.User
		if err := sc.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", userID).Error; err != nil {
			return err
		}
		if !user.IsActive || user.Status != "active" {
			return promoInvalid("user_id", "active account required")
		}
		err := sc.db.First(&result, "user_id = ?", userID).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		for i := 0; i < 10; i++ {
			code, err := randomRewardCode()
			if err != nil {
				return err
			}
			result = models.UserReferralCode{UserID: userID, Code: code}
			created := sc.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&result)
			if created.Error != nil {
				return created.Error
			}
			if created.RowsAffected == 1 {
				return nil
			}
		}
		return errors.New("unable to generate a unique referral code")
	})
	return &result, err
}

// Lock user IDs in one global order across referrals to prevent mutual-invite deadlocks.
func lockReferralUsers(tx *gorm.DB, ids ...string) (map[string]models.User, error) {
	sort.Strings(ids)
	result := map[string]models.User{}
	for _, id := range ids {
		if _, ok := result[id]; ok {
			continue
		}
		var u models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", id).Error; err != nil {
			return nil, err
		}
		result[id] = u
	}
	return result, nil
}
func completedRideCount(tx *gorm.DB, userID string) (int64, error) {
	var count int64
	err := tx.Model(&models.RideCompletionReceipt{}).Where("passenger_id = ?", userID).Count(&count).Error
	return count, err
}
func (s *Store) ApplyReferralCode(refereeID, code string) (*models.Referral, error) {
	var result *models.Referral
	err := s.Transaction(func(sc *Store) error {
		settings, err := sc.GetReferralSettings()
		if err != nil {
			return err
		}
		if !settings.Enabled {
			return promoInvalid("code", "referrals are disabled")
		}
		var invite models.UserReferralCode
		err = sc.db.Where("code = ?", strings.ToUpper(strings.TrimSpace(code))).First(&invite).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return promoInvalid("code", "invalid or unavailable referral code")
		}
		if err != nil {
			return err
		}
		if invite.UserID == refereeID {
			return promoInvalid("code", "self-referral is not allowed")
		}
		users, err := lockReferralUsers(sc.db, refereeID, invite.UserID)
		if err != nil {
			return err
		}
		referee, referrer := users[refereeID], users[invite.UserID]
		now := sc.now()
		if referee.Role != models.RolePassenger || !referee.IsActive || referee.Status != "active" {
			return promoInvalid("code", "active passenger account required")
		}
		if referee.CreatedAt.After(now) || !now.Before(referee.CreatedAt.Add(7*24*time.Hour)) {
			return promoInvalid("code", "referral must be applied within 7 days of signup")
		}
		if referee.Phone == "" || referee.PhoneVerifiedAt == nil {
			return promoInvalid("phone", "verify your phone before applying a referral")
		}
		if !referrer.IsActive || referrer.Status != "active" || (referrer.Role != models.RolePassenger && referrer.Role != models.RoleDriver) {
			return promoInvalid("code", "invalid or unavailable referral code")
		}
		if referrer.Phone != "" && referrer.Phone == referee.Phone {
			return promoInvalid("code", "self-referral is not allowed")
		}
		completed, err := completedRideCount(sc.db, refereeID)
		if err != nil {
			return err
		}
		if completed > 0 {
			return promoInvalid("code", "referral must be applied before the first completed ride")
		}
		var count int64
		if err := sc.db.Model(&models.Referral{}).Where("referee_id = ?", refereeID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return promoInvalid("code", "a referral has already been applied")
		}
		if err := sc.db.Model(&models.Referral{}).Where("referrer_id = ? AND status = ?", referrer.ID, "rewarded").Count(&count).Error; err != nil {
			return err
		}
		if count >= settings.MaxRewardsPerReferrer {
			return promoInvalid("code", "referrer reward limit reached")
		}
		phone, err := backenddb.CanonicalPhone(referee.Phone)
		if err != nil {
			return promoInvalid("phone", "valid verified phone required")
		}
		if err := sc.db.Model(&models.ReferralRewardPhone{}).Where("phone = ?", phone).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return promoInvalid("phone", "this phone has already received a referral reward")
		}
		result = &models.Referral{ID: newID(), ReferrerID: referrer.ID, RefereeID: refereeID, Status: "pending", ReferrerRewardStatus: "none"}
		if err := sc.db.Create(result).Error; err != nil {
			return err
		}
		return sc.QueueFraudEvent(referrer.ID, "referral_applied", nil, "referral:"+result.ID)
	})
	return result, err
}

func issueReferralPromo(tx *gorm.DB, userID, kind string, value int64, days int, now time.Time) error {
	for i := 0; i < 10; i++ {
		code, err := randomRewardCode()
		if err != nil {
			return err
		}
		one := int64(1)
		p := models.PromoCode{ID: newID(), Code: "RW-" + code, Title: "Referral reward", Description: "Reward for a qualifying first ride", DiscountType: kind, DiscountValue: value, Audience: "selected", StartsAt: now, ExpiresAt: now.AddDate(0, 0, days), MaxRedemptions: &one, MaxPerUser: 1, Status: "active", Source: "referral_reward"}
		if err := ValidatePromo(&p); err != nil {
			return err
		}
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&p)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			continue
		}
		return tx.Create(&models.PromoAudience{PromoID: p.ID, UserID: userID, GrantedAt: now}).Error
	}
	return errors.New("unable to generate a unique referral reward")
}

// OnRideCompleted must receive every authoritative completion, even without a promo.
// Receipt + referral + phone claim + rewards commit together; retries are idempotent.
func (s *Store) OnRideCompleted(rideID, passengerID string, fare int64) error {
	if _, err := uuid.Parse(rideID); err != nil {
		return promoInvalid("ride_id", "ride_id must be a UUID")
	}
	if fare < 0 {
		return promoInvalid("fare", "fare must not be negative")
	}
	return s.Transaction(func(sc *Store) error {
		var referral models.Referral
		err := sc.db.Where("referee_id = ?", passengerID).First(&referral).Error
		hasReferral := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		ids := []string{passengerID}
		if hasReferral {
			ids = append(ids, referral.ReferrerID)
		}
		users, err := lockReferralUsers(sc.db, ids...)
		if err != nil {
			return err
		}
		passenger := users[passengerID]
		if passenger.Role != models.RolePassenger {
			return promoInvalid("passenger_id", "completion must belong to a passenger")
		}
		var receipt models.RideCompletionReceipt
		err = sc.db.First(&receipt, "ride_id = ?", rideID).Error
		if err == nil {
			if receipt.PassengerID != passengerID {
				return promoInvalid("ride_id", "completion already belongs to another passenger")
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		previous, err := completedRideCount(sc.db, passengerID)
		if err != nil {
			return err
		}
		now := sc.now()
		created := sc.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.RideCompletionReceipt{RideID: rideID, PassengerID: passengerID, CreatedAt: now})
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected != 1 {
			return promoInvalid("ride_id", "completion already recorded")
		}
		if err := sc.QueueFraudEvent(passengerID, "ride_completed", &rideID, "completed:"+rideID); err != nil {
			return err
		}
		// Re-read after the account lock: a concurrent apply may have committed meanwhile.
		if err := sc.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("referee_id = ?", passengerID).First(&referral).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if referral.Status != "pending" {
			return nil
		}
		// If a referral appeared after the initial read, retry so both user locks are held.
		referrer, locked := users[referral.ReferrerID]
		if !locked {
			return errors.New("referral changed concurrently; retry completion")
		}
		reject := func(reason string) error {
			referral.Status = "rejected"
			referral.RejectReason = reason
			return sc.db.Save(&referral).Error
		}
		if previous > 0 {
			return reject("not_first_completed_ride")
		}
		settings, err := sc.GetReferralSettings()
		if err != nil {
			return err
		}
		if !settings.Enabled {
			return reject("referrals_disabled")
		}
		if fare < settings.QualifyingMinFare {
			return reject("minimum_fare_not_reached")
		}
		if !passenger.IsActive || passenger.Status != "active" || !referrer.IsActive || referrer.Status != "active" {
			return reject("inactive_account")
		}
		if passenger.PhoneVerifiedAt == nil || passenger.Phone == "" {
			return reject("unverified_phone")
		}
		phone, err := backenddb.CanonicalPhone(passenger.Phone)
		if err != nil {
			return reject("invalid_phone")
		}
		if referrer.ID == passengerID || (referrer.Phone != "" && referrer.Phone == phone) {
			return reject("self_referral")
		}
		var count int64
		if err := sc.db.Model(&models.Referral{}).Where("referrer_id = ? AND status = ?", referrer.ID, "rewarded").Count(&count).Error; err != nil {
			return err
		}
		if count >= settings.MaxRewardsPerReferrer {
			return reject("referrer_reward_limit")
		}
		claimed := sc.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.ReferralRewardPhone{Phone: phone, ReferralID: referral.ID, CreatedAt: now})
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected != 1 {
			return reject("phone_already_rewarded")
		}
		referral.Status = "qualified"
		referral.QualifyingRideID = &rideID
		if err := issueReferralPromo(sc.db, passengerID, settings.RefereeRewardType, settings.RefereeRewardValue, settings.RewardValidDays, now); err != nil {
			return err
		}
		if referrer.Role == models.RoleDriver {
			referral.ReferrerRewardStatus = "pending_wallet"
		} else if referrer.Role == models.RolePassenger {
			if err := issueReferralPromo(sc.db, referrer.ID, settings.ReferrerRewardType, settings.ReferrerRewardValue, settings.RewardValidDays, now); err != nil {
				return err
			}
			referral.ReferrerRewardStatus = "issued"
		} else {
			return errors.New("unsupported referrer role")
		}
		referral.Status = "rewarded"
		return sc.db.Save(&referral).Error
	})
}

type ReferralStats struct {
	Pending       int64 `json:"pending"`
	Qualified     int64 `json:"qualified"`
	Rewarded      int64 `json:"rewarded"`
	Rejected      int64 `json:"rejected"`
	PendingWallet int64 `json:"pending_wallet"`
}

func (s *Store) ReferralStats(userID string) (*ReferralStats, error) {
	result := &ReferralStats{}
	for status, target := range map[string]*int64{"pending": &result.Pending, "qualified": &result.Qualified, "rewarded": &result.Rewarded, "rejected": &result.Rejected} {
		if err := s.db.Model(&models.Referral{}).Where("referrer_id = ? AND status = ?", userID, status).Count(target).Error; err != nil {
			return nil, err
		}
	}
	err := s.db.Model(&models.Referral{}).Where("referrer_id = ? AND referrer_reward_status = ?", userID, "pending_wallet").Count(&result.PendingWallet).Error
	return result, err
}
func (s *Store) ListReferrals(status string, limit, offset int) ([]models.Referral, int64, error) {
	q := s.db.Model(&models.Referral{})
	if status != "" {
		switch status {
		case "pending", "qualified", "rewarded", "rejected":
		default:
			return nil, 0, promoInvalid("status", "invalid referral status")
		}
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []models.Referral{}
	err := q.Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}
