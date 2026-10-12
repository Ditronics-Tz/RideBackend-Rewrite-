package store

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
	"ride-backend/models"
	"strings"
	"time"
)

type PromoView struct {
	models.PromoCode
	EffectiveStatus string `json:"effective_status"`
}

func PromoResponse(p models.PromoCode, now time.Time) PromoView {
	return PromoView{PromoCode: p, EffectiveStatus: p.EffectiveStatus(now)}
}

type PromoDetail struct {
	PromoView
	TotalDiscountGiven int64 `json:"total_discount_given"`
	AudienceCount      int64 `json:"audience_count"`
}

func (s *Store) PromoDetail(id string, now time.Time) (*PromoDetail, error) {
	p, err := s.GetPromo(id)
	if err != nil {
		return nil, err
	}
	result := &PromoDetail{PromoView: PromoResponse(*p, now)}
	if err := s.db.Model(&models.PromoRedemption{}).Select("COALESCE(SUM(discount_amount),0)").Where("promo_id = ? AND status = ?", id, "applied").Scan(&result.TotalDiscountGiven).Error; err != nil {
		return nil, err
	}
	if p.Audience == "all" {
		err = s.db.Model(&models.User{}).Where("role = ?", models.RolePassenger).Count(&result.AudienceCount).Error
	} else {
		err = s.db.Model(&models.PromoAudience{}).Where("promo_id = ?", id).Count(&result.AudienceCount).Error
	}
	return result, err
}

func (s *Store) ListPromos(status, audience, search string, limit, offset int, now time.Time) ([]PromoView, int64, error) {
	q := s.db.Model(&models.PromoCode{})
	switch status {
	case "":
	case "active":
		q = q.Where("status = ? AND starts_at <= ? AND expires_at > ?", "active", now, now)
	case "scheduled":
		q = q.Where("status = ? AND starts_at > ? AND expires_at > ?", "active", now, now)
	case "expired":
		q = q.Where("status = ? AND expires_at <= ?", "active", now)
	case "draft", "paused", "archived":
		q = q.Where("status = ?", status)
	default:
		return nil, 0, promoInvalid("status", "invalid promo status")
	}
	if audience != "" {
		if audience != "all" && audience != "selected" {
			return nil, 0, promoInvalid("audience", "invalid audience")
		}
		q = q.Where("audience = ?", audience)
	}
	if search = strings.ToLower(strings.TrimSpace(search)); search != "" {
		like := "%" + search + "%"
		q = q.Where("LOWER(code) LIKE ? OR LOWER(title) LIKE ?", like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []models.PromoCode{}
	if err := q.Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	result := make([]PromoView, 0, len(rows))
	for _, p := range rows {
		result = append(result, PromoResponse(p, now))
	}
	return result, total, nil
}

type PromoAudienceView struct {
	models.PromoAudience
	User models.UserResponse `json:"user"`
}

func (s *Store) ListPromoAudience(id, search string, limit, offset int) ([]PromoAudienceView, int64, error) {
	if _, err := s.GetPromo(id); err != nil {
		return nil, 0, err
	}
	q := s.db.Model(&models.PromoAudience{}).Joins("JOIN users ON users.id = promo_audiences.user_id").Where("promo_id = ?", id)
	if search = strings.ToLower(strings.TrimSpace(search)); search != "" {
		like := "%" + search + "%"
		q = q.Where("LOWER(users.name) LIKE ? OR LOWER(users.email) LIKE ? OR users.phone LIKE ?", like, like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	grants := []models.PromoAudience{}
	if err := q.Select("promo_audiences.*").Order("granted_at DESC, user_id ASC").Limit(limit).Offset(offset).Find(&grants).Error; err != nil {
		return nil, 0, err
	}
	result := make([]PromoAudienceView, 0, len(grants))
	for _, g := range grants {
		var u models.User
		if err := s.db.First(&u, "id = ?", g.UserID).Error; err != nil {
			return nil, 0, err
		}
		result = append(result, PromoAudienceView{PromoAudience: g, User: models.ToUserResponse(&u)})
	}
	return result, total, nil
}
func (s *Store) ListPromoRedemptions(id string, limit, offset int) ([]models.PromoRedemption, int64, error) {
	if _, err := s.GetPromo(id); err != nil {
		return nil, 0, err
	}
	q := s.db.Model(&models.PromoRedemption{}).Where("promo_id = ?", id)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []models.PromoRedemption{}
	err := q.Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}

type UserPromos struct {
	Granted     []PromoView              `json:"granted"`
	Redemptions []models.PromoRedemption `json:"redemptions"`
}

func (s *Store) UserPromos(id string, now time.Time) (*UserPromos, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, gorm.ErrRecordNotFound
	}
	var u models.User
	if err := s.db.First(&u, "id = ?", id).Error; err != nil {
		return nil, err
	}
	if u.Role != models.RolePassenger {
		return nil, promoInvalid("user_id", "promos are only available to passengers")
	}
	rows := []models.PromoCode{}
	if err := eligiblePromoQuery(s.db, id).Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := &UserPromos{Granted: make([]PromoView, 0, len(rows)), Redemptions: []models.PromoRedemption{}}
	for _, p := range rows {
		result.Granted = append(result.Granted, PromoResponse(p, now))
	}
	err := s.db.Where("user_id = ?", id).Order("created_at DESC").Find(&result.Redemptions).Error
	return result, err
}
func eligiblePromoQuery(tx *gorm.DB, id string) *gorm.DB {
	return tx.Model(&models.PromoCode{}).Where("audience = ? OR id IN (?)", "all", tx.Model(&models.PromoAudience{}).Select("promo_id").Where("user_id = ?", id))
}

type AvailablePromo struct {
	PromoView
	DiscountAmount int64 `json:"discount_amount"`
	FinalFare      int64 `json:"final_fare"`
}

func (s *Store) AvailablePromos(userID string, fare int64, now time.Time) ([]AvailablePromo, error) {
	if fare < 0 {
		return nil, promoInvalid("fare", "fare must not be negative")
	}
	rows := []models.PromoCode{}
	if err := eligiblePromoQuery(s.db, userID).Where("status = ? AND starts_at <= ? AND expires_at > ?", "active", now, now).Order("expires_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := []AvailablePromo{}
	for _, p := range rows {
		discount, _, err := s.EvaluatePromo(userID, p.Code, fare, now)
		if err != nil {
			if _, ok := err.(*PromoError); ok {
				continue
			}
			return nil, err
		}
		result = append(result, AvailablePromo{PromoView: PromoResponse(p, now), DiscountAmount: discount, FinalFare: fare - discount})
	}
	return result, nil
}
func (s *Store) SearchUsers(role, search string, limit, offset int) ([]*models.User, int64, error) {
	q := s.db.Model(&models.User{})
	if role != "" {
		q = q.Where("role = ?", role)
	}
	if search = strings.ToLower(strings.TrimSpace(search)); search != "" {
		like := "%" + search + "%"
		plate := "%" + CanonicalIdentifier(search) + "%"
		q = q.Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ? OR phone LIKE ? OR id IN (?)", like, like, like, s.db.Model(&models.DriverProfile{}).Select("user_id").Where("plate_number LIKE ?", plate))
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []*models.User{}
	err := q.Order("created_at ASC, id ASC").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}
