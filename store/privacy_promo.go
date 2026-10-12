package store

import (
	"gorm.io/gorm"
	"ride-backend/models"
)

func init() {
	RegisterDataExporter(func(s *Store) DataExporter { return promoExporter{s} })
	RegisterAnonymizer(promoAnonymizer{})
}

type promoExporter struct{ s *Store }

func (promoExporter) Name() string { return "promos_and_referrals" }
func (promoExporter) Tables() []string {
	return []string{"promo_codes", "promo_audiences", "promo_redemptions", "referrals", "ride_completion_receipts"}
}
func (e promoExporter) Export(id string) (any, []string, error) {
	s := e.s
	var granted []models.PromoAudience
	var redeemed []models.PromoRedemption
	var referred []models.Referral
	var code models.UserReferralCode
	if err := s.db.Where("user_id = ?", id).Find(&granted).Error; err != nil {
		return nil, nil, err
	}
	if err := s.db.Where("user_id = ?", id).Find(&redeemed).Error; err != nil {
		return nil, nil, err
	}
	if err := s.db.Where("referrer_id = ? OR referee_id = ?", id, id).Find(&referred).Error; err != nil {
		return nil, nil, err
	}
	_ = s.db.First(&code, "user_id = ?", id).Error
	grantOut := make([]map[string]any, 0, len(granted))
	for _, g := range granted {
		grantOut = append(grantOut, map[string]any{"promo_id": g.PromoID, "granted_at": g.GrantedAt, "notified_at": g.NotifiedAt})
	}
	redemptionOut := make([]map[string]any, 0, len(redeemed))
	for _, r := range redeemed {
		redemptionOut = append(redemptionOut, map[string]any{"promo_id": r.PromoID, "ride_id": r.RideID, "discount_amount": r.DiscountAmount, "status": r.Status, "created_at": r.CreatedAt})
	}
	referralOut := make([]map[string]any, 0, len(referred))
	for _, r := range referred {
		direction := "inviter"
		if r.RefereeID == id {
			direction = "invited"
		}
		referralOut = append(referralOut, map[string]any{"direction": direction, "status": r.Status, "reject_reason": r.RejectReason, "reward_status": r.ReferrerRewardStatus, "created_at": r.CreatedAt})
	}
	return map[string]any{"granted_promos": grantOut, "redemptions": redemptionOut, "referrals": referralOut, "referral_code": code.Code}, nil, nil
}

type promoAnonymizer struct{}

func (promoAnonymizer) Name() string { return "promos_and_referrals" }
func (promoAnonymizer) Tables() []string {
	return []string{"promo_audiences", "promo_redemptions", "referrals", "user_referral_codes", "referral_settings", "ride_completion_receipts", "referral_reward_phones"}
}
func (promoAnonymizer) Description() string {
	return "removes audience membership, referral code and raw-phone anti-abuse ledger; retains promo redemption and referral UUID relationships"
}
func (promoAnonymizer) Anonymize(tx *gorm.DB, id string) error {
	if err := tx.Where("user_id = ?", id).Delete(&models.PromoAudience{}).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.PromoAudience{}).Where("granted_by = ?", id).Update("granted_by", nil).Error; err != nil {
		return err
	}
	if err := tx.Where("user_id = ?", id).Delete(&models.UserReferralCode{}).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.Referral{}).Where("referrer_id = ? OR referee_id = ?", id, id).Update("reject_reason", "").Error; err != nil {
		return err
	}
	var u models.User
	if err := tx.First(&u, "id = ?", id).Error; err != nil {
		return err
	}
	if u.Phone != "" {
		if err := tx.Where("phone = ?", u.Phone).Delete(&models.ReferralRewardPhone{}).Error; err != nil {
			return err
		}
	}
	return nil
}
