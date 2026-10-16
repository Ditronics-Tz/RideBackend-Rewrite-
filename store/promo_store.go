package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"regexp"
	backenddb "ride-backend/db"
	"ride-backend/models"
	"ride-backend/notifier"
	"strings"
	"time"
)

type PromoError struct {
	Message   string
	Fields    map[string]string
	Duplicate bool
}

func (e *PromoError) Error() string { return e.Message }
func promoInvalid(field, message string) error {
	return &PromoError{Message: message, Fields: map[string]string{field: message}}
}
func unavailablePromo() error { return promoInvalid("code", "invalid or unavailable code") }
func duplicatePromo() error {
	return &PromoError{Message: "code already exists", Fields: map[string]string{"code": "code already exists"}, Duplicate: true}
}

// Transaction lets controllers persist writes and their named audit entry together.
func (s *Store) Transaction(fn func(*Store) error) error {
	return s.db.Transaction(func(tx *gorm.DB) error { sc := *s; sc.db = tx; return fn(&sc) })
}

var promoCodePattern = regexp.MustCompile(`^[A-Z0-9-]{4,20}$`)

func ValidatePromo(p *models.PromoCode) error {
	p.StartsAt = p.StartsAt.UTC()
	p.ExpiresAt = p.ExpiresAt.UTC()
	p.Code = strings.ToUpper(strings.TrimSpace(p.Code))
	p.Title = strings.TrimSpace(p.Title)
	if !promoCodePattern.MatchString(p.Code) {
		return promoInvalid("code", "code must contain 4-20 letters, digits or hyphens")
	}
	if p.Title == "" || len(p.Title) > 200 {
		return promoInvalid("title", "title must contain 1-200 characters")
	}
	if len(p.Description) > 4000 {
		return promoInvalid("description", "description must not exceed 4000 characters")
	}
	if p.DiscountType != "percent" && p.DiscountType != "fixed" {
		return promoInvalid("discount_type", "discount_type must be percent or fixed")
	}
	if p.DiscountValue <= 0 || (p.DiscountType == "percent" && p.DiscountValue > 100) {
		return promoInvalid("discount_value", "discount_value must be positive; percent must be 1-100")
	}
	if p.MaxDiscount != nil && (*p.MaxDiscount <= 0 || p.DiscountType != "percent") {
		return promoInvalid("max_discount", "max_discount must be a positive percent cap")
	}
	if p.MinFare < 0 {
		return promoInvalid("min_fare", "min_fare must not be negative")
	}
	if p.Audience != "all" && p.Audience != "selected" {
		return promoInvalid("audience", "audience must be all or selected")
	}
	if p.StartsAt.IsZero() || !p.ExpiresAt.After(p.StartsAt) {
		return promoInvalid("expires_at", "expires_at must be after starts_at")
	}
	if p.MaxRedemptions != nil && (*p.MaxRedemptions <= 0 || *p.MaxRedemptions < p.RedeemedCount) {
		return promoInvalid("max_redemptions", "max_redemptions must be positive and at least redeemed_count")
	}
	if p.MaxPerUser < 1 {
		return promoInvalid("max_per_user", "max_per_user must be at least 1")
	}
	switch p.Status {
	case "draft", "active", "paused", "archived":
	default:
		return promoInvalid("status", "status must be draft, active, paused or archived")
	}
	return nil
}

func (s *Store) CreatePromo(p *models.PromoCode, actor string, audience []string) error {
	if p.MaxPerUser == 0 {
		p.MaxPerUser = 1
	}
	if p.Status == "" {
		p.Status = "draft"
	}
	if err := ValidatePromo(p); err != nil {
		return err
	}
	return s.Transaction(func(sc *Store) error {
		p.ID = newID()
		p.Source = "admin"
		p.CreatedBy = &actor
		p.RedeemedCount = 0
		result := sc.db.Clauses(clause.OnConflict{DoNothing: true}).Create(p)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return duplicatePromo()
		}
		if len(audience) > 0 && p.Audience != "selected" {
			return promoInvalid("audience_user_ids", "initial audience requires audience=selected")
		}
		resultAudience, err := sc.AddPromoAudience(p.ID, actor, AudienceInput{UserIDs: audience})
		if err != nil {
			return err
		}
		if len(resultAudience.NotFound) > 0 || len(resultAudience.NotPassengers) > 0 {
			return promoInvalid("audience_user_ids", "all initial audience IDs must identify passengers")
		}
		return nil
	})
}
func (s *Store) GetPromo(id string) (*models.PromoCode, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, gorm.ErrRecordNotFound
	}
	var p models.PromoCode
	err := s.db.First(&p, "id = ?", id).Error
	return &p, err
}
func (s *Store) UpdatePromo(id string, changes map[string]json.RawMessage) (*models.PromoCode, error) {
	var result *models.PromoCode
	err := s.Transaction(func(sc *Store) error {
		p, err := sc.lockPromo(id)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(p)
		var merged map[string]json.RawMessage
		_ = json.Unmarshal(raw, &merged)
		allowed := map[string]bool{"code": true, "title": true, "description": true, "discount_type": true, "discount_value": true, "max_discount": true, "min_fare": true, "audience": true, "first_ride_only": true, "starts_at": true, "expires_at": true, "max_redemptions": true, "max_per_user": true}
		for field, value := range changes {
			if !allowed[field] {
				return nilField(field)
			}
			merged[field] = value
		}
		raw, _ = json.Marshal(merged)
		var updated models.PromoCode
		if err := json.Unmarshal(raw, &updated); err != nil {
			return promoInvalid("body", "invalid promo field type or timestamp")
		}
		if err := ValidatePromo(&updated); err != nil {
			return err
		}
		if p.RedeemedCount > 0 && (updated.Code != p.Code || updated.DiscountType != p.DiscountType || updated.DiscountValue != p.DiscountValue || updated.Audience != p.Audience) {
			return promoInvalid("body", "code, discount type/value and audience are locked after redemption")
		}
		// Updating a code uses the unique constraint without aborting a surrounding audit transaction.
		var count int64
		if err := sc.db.Model(&models.PromoCode{}).Where("code = ? AND id <> ?", updated.Code, id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return duplicatePromo()
		}
		if err := sc.db.Save(&updated).Error; err != nil {
			if isUniqueViolation(err) {
				return duplicatePromo()
			}
			return err
		}
		result = &updated
		return nil
	})
	return result, err
}
func nilField(field string) error { return promoInvalid(field, "field cannot be changed") }
func isUniqueViolation(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "23505") || strings.Contains(strings.ToLower(err.Error()), "unique constraint"))
}
func (s *Store) lockPromo(id string) (*models.PromoCode, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, gorm.ErrRecordNotFound
	}
	var p models.PromoCode
	err := s.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, "id = ?", id).Error
	return &p, err
}
func (s *Store) SetPromoStatus(id, status string) (*models.PromoCode, error) {
	var p *models.PromoCode
	err := s.Transaction(func(sc *Store) error {
		var err error
		p, err = sc.lockPromo(id)
		if err != nil {
			return err
		}
		if p.Status == "archived" {
			return promoInvalid("status", "archived promos cannot be changed")
		}
		p.Status = status
		if err := ValidatePromo(p); err != nil {
			return err
		}
		return sc.db.Save(p).Error
	})
	return p, err
}

func evaluatePromo(tx *gorm.DB, userID, code string, fare int64, now time.Time) (int64, *models.PromoCode, error) {
	var p models.PromoCode
	err := tx.Where("code = ?", strings.ToUpper(strings.TrimSpace(code))).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil, unavailablePromo()
	}
	if err != nil {
		return 0, nil, err
	}
	// Resolve visibility before returning any specific status/time/limit error.
	if p.Audience == "selected" {
		var count int64
		if err := tx.Model(&models.PromoAudience{}).Where("promo_id = ? AND user_id = ?", p.ID, userID).Count(&count).Error; err != nil {
			return 0, nil, err
		}
		if count == 0 {
			return 0, nil, unavailablePromo()
		}
	}
	if p.Status != "active" {
		return 0, nil, unavailablePromo()
	}
	if now.Before(p.StartsAt) {
		return 0, nil, promoInvalid("code", "code is not active yet")
	}
	if !now.Before(p.ExpiresAt) {
		return 0, nil, promoInvalid("code", "code has expired")
	}
	var count int64
	if err := tx.Model(&models.PromoRedemption{}).Where("promo_id = ? AND user_id = ? AND status IN ?", p.ID, userID, []string{"reserved", "applied"}).Count(&count).Error; err != nil {
		return 0, nil, err
	}
	if count >= p.MaxPerUser {
		return 0, nil, promoInvalid("code", "per-user limit reached")
	}
	if p.MaxRedemptions != nil && p.RedeemedCount >= *p.MaxRedemptions {
		return 0, nil, promoInvalid("code", "total limit reached")
	}
	if fare < 0 {
		return 0, nil, promoInvalid("fare", "fare must not be negative")
	}
	if fare < p.MinFare {
		return 0, nil, promoInvalid("fare", "minimum fare not reached")
	}
	if p.FirstRideOnly {
		if err := tx.Model(&models.RideCompletionReceipt{}).Where("passenger_id = ?", userID).Count(&count).Error; err != nil {
			return 0, nil, err
		}
		if count > 0 {
			return 0, nil, promoInvalid("code", "code is only valid for the first completed ride")
		}
	}
	var user models.User
	if err := tx.First(&user, "id = ?", userID).Error; err != nil {
		return 0, nil, unavailablePromo()
	}
	if user.Role != models.RolePassenger || !user.IsActive || user.Status != "active" {
		return 0, nil, unavailablePromo()
	}
	var held int64
	if err := tx.Model(&models.FraudMitigation{}).Where("user_id = ? AND type = ? AND active = ?", userID, "promo_block", true).Count(&held).Error; err != nil {
		return 0, nil, err
	}
	if held > 0 {
		return 0, nil, unavailablePromo()
	}
	discount := p.DiscountValue
	if p.DiscountType == "percent" {
		discount = (fare/100)*p.DiscountValue + ((fare%100)*p.DiscountValue)/100
		if p.MaxDiscount != nil && discount > *p.MaxDiscount {
			discount = *p.MaxDiscount
		}
	}
	if discount > fare {
		discount = fare
	}
	return discount, &p, nil
}
func (s *Store) EvaluatePromo(userID, code string, fare int64, now time.Time) (int64, *models.PromoCode, error) {
	return evaluatePromo(s.db, userID, code, fare, now)
}

// Reserve counts against both limits. Caller must provide a stable UUID ride ID.
func (s *Store) ReservePromo(userID, code string, fare int64, rideID string) (*models.PromoRedemption, error) {
	if _, err := uuid.Parse(rideID); err != nil {
		return nil, promoInvalid("ride_id", "ride_id must be a UUID")
	}
	var redemption *models.PromoRedemption
	err := s.Transaction(func(sc *Store) error {
		// User lock serializes per-user limits against reservations and first completions.
		var user models.User
		if err := sc.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", userID).Error; err != nil {
			return err
		}
		var existing models.PromoRedemption
		err := sc.db.Where("ride_id = ?", rideID).First(&existing).Error
		if err == nil {
			if existing.UserID == userID && existing.Status != "released" {
				var p models.PromoCode
				if err := sc.db.First(&p, "id = ?", existing.PromoID).Error; err != nil {
					return err
				}
				if p.Code == strings.ToUpper(strings.TrimSpace(code)) {
					redemption = &existing
					return nil
				}
			}
			return promoInvalid("ride_id", "ride already has a promo redemption")
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var p models.PromoCode
		if err := sc.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("code = ?", strings.ToUpper(strings.TrimSpace(code))).First(&p).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return unavailablePromo()
			}
			return err
		}
		discount, _, err := evaluatePromo(sc.db, userID, code, fare, sc.now())
		if err != nil {
			return err
		}
		updated := sc.db.Model(&models.PromoCode{}).Where("id = ? AND (max_redemptions IS NULL OR redeemed_count < max_redemptions)", p.ID).UpdateColumn("redeemed_count", gorm.Expr("redeemed_count + 1"))
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return promoInvalid("code", "total limit reached")
		}
		redemption = &models.PromoRedemption{ID: newID(), PromoID: p.ID, UserID: userID, RideID: &rideID, DiscountAmount: discount, Status: "reserved"}
		inserted := sc.db.Clauses(clause.OnConflict{DoNothing: true}).Create(redemption)
		if inserted.Error != nil {
			return inserted.Error
		}
		if inserted.RowsAffected != 1 {
			return promoInvalid("ride_id", "ride already has a promo redemption")
		}
		return nil
	})
	return redemption, err
}
func (s *Store) ConfirmPromo(rideID string) error { return s.finishPromo(rideID, false) }
func (s *Store) ReleasePromo(rideID string) error { return s.finishPromo(rideID, true) }
func (s *Store) finishPromo(rideID string, release bool) error {
	if _, err := uuid.Parse(rideID); err != nil {
		return promoInvalid("ride_id", "ride_id must be a UUID")
	}
	return s.Transaction(func(sc *Store) error {
		var r models.PromoRedemption
		err := sc.db.Where("ride_id = ?", rideID).First(&r).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		// Consistent promo -> redemption ordering prevents confirm/release deadlocks.
		if _, err := sc.lockPromo(r.PromoID); err != nil {
			return err
		}
		if err := sc.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&r, "id = ?", r.ID).Error; err != nil {
			return err
		}
		if r.Status == "released" {
			if release {
				return nil
			}
			return promoInvalid("ride_id", "released reservation cannot be confirmed")
		}
		if r.Status == "applied" {
			if !release {
				return nil
			}
			return promoInvalid("ride_id", "applied redemption cannot be released")
		}
		if release {
			r.Status = "released"
			updated := sc.db.Model(&models.PromoCode{}).Where("id = ? AND redeemed_count > 0", r.PromoID).UpdateColumn("redeemed_count", gorm.Expr("redeemed_count - 1"))
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return errors.New("promo reservation counter is inconsistent")
			}
		} else {
			r.Status = "applied"
		}
		return sc.db.Save(&r).Error
	})
}

type AudienceInput struct {
	UserIDs []string `json:"user_ids"`
	Phones  []string `json:"phones"`
	Emails  []string `json:"emails"`
}
type AudienceResult struct {
	Added          int      `json:"added"`
	AlreadyPresent int      `json:"already_present"`
	NotFound       []string `json:"not_found"`
	NotPassengers  []string `json:"not_passengers"`
}

func (s *Store) AddPromoAudience(id, actor string, input AudienceInput) (*AudienceResult, error) {
	result := &AudienceResult{NotFound: []string{}, NotPassengers: []string{}}
	if len(input.UserIDs)+len(input.Phones)+len(input.Emails) > 500 {
		return nil, promoInvalid("audience", "add at most 500 identifiers per request")
	}
	err := s.Transaction(func(sc *Store) error {
		p, err := sc.lockPromo(id)
		if err != nil {
			return err
		}
		if p.Audience != "selected" && len(input.UserIDs)+len(input.Phones)+len(input.Emails) > 0 {
			return promoInvalid("audience", "manual grants require a selected audience")
		}
		seen := map[string]bool{}
		add := func(field, value, original string) error {
			var u models.User
			err := sc.db.Where(field+" = ?", value).First(&u).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				result.NotFound = append(result.NotFound, original)
				return nil
			}
			if err != nil {
				return err
			}
			if u.Role != models.RolePassenger {
				result.NotPassengers = append(result.NotPassengers, original)
				return nil
			}
			if seen[u.ID] {
				return nil
			}
			seen[u.ID] = true
			grant := models.PromoAudience{PromoID: id, UserID: u.ID, GrantedBy: &actor, GrantedAt: time.Now().UTC()}
			inserted := sc.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&grant)
			if inserted.Error != nil {
				return inserted.Error
			}
			if inserted.RowsAffected == 0 {
				result.AlreadyPresent++
			} else {
				result.Added++
			}
			return nil
		}
		for _, value := range input.UserIDs {
			if _, err := uuid.Parse(value); err != nil {
				result.NotFound = append(result.NotFound, value)
				continue
			}
			if err := add("id", value, value); err != nil {
				return err
			}
		}
		for _, value := range input.Phones {
			phone, err := backenddb.CanonicalPhone(value)
			if err != nil || phone == "" {
				result.NotFound = append(result.NotFound, value)
				continue
			}
			if err := add("phone", phone, value); err != nil {
				return err
			}
		}
		for _, value := range input.Emails {
			if err := add("email", strings.ToLower(strings.TrimSpace(value)), value); err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}
func (s *Store) RemovePromoAudience(id, userID string) error {
	return s.Transaction(func(sc *Store) error {
		if _, err := sc.lockPromo(id); err != nil {
			return err
		}
		if _, err := uuid.Parse(userID); err != nil {
			return promoInvalid("user_id", "user_id must be a UUID")
		}
		return sc.db.Where("promo_id = ? AND user_id = ?", id, userID).Delete(&models.PromoAudience{}).Error
	})
}

func (s *Store) SharePromo(ctx context.Context, id, actor string) (int, error) {
	count := 0
	err := s.Transaction(func(sc *Store) error {
		p, err := sc.lockPromo(id)
		if err != nil {
			return err
		}
		if p.EffectiveStatus(s.now()) != "active" {
			return promoInvalid("status", "only currently active promos can be shared")
		}
		var users []models.User
		q := sc.db.Where("role = ?", models.RolePassenger)
		if p.Audience == "selected" {
			q = q.Where("id IN (?)", sc.db.Model(&models.PromoAudience{}).Select("user_id").Where("promo_id = ?", id))
		}
		if err := q.Order("id ASC").Find(&users).Error; err != nil {
			return err
		}
		sender := sc.Notifier
		if sender == nil {
			sender = notifier.Logging{}
		}
		now := time.Now().UTC()
		for _, u := range users {
			grant := models.PromoAudience{PromoID: id, UserID: u.ID, GrantedBy: &actor, GrantedAt: now}
			if err := sc.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&grant).Error; err != nil {
				return err
			}
			if err := sender.NotifyPromo(ctx, u.ID, notifier.PromoMessage{PromoID: id, Code: p.Code, Title: p.Title, Description: p.Description}); err != nil {
				return err
			}
			body := "Use code " + p.Code + " for your next eligible ride."
			if p.Description != "" {
				body = p.Description
			}
			if len([]rune(body)) > 500 {
				body = string([]rune(body)[:500])
			}
			title := p.Title
			if len([]rune(title)) > 160 {
				title = string([]rune(title)[:160])
			}
			if err := sc.queueAppNotification(ctx, u.ID, "promo", title, body, map[string]string{"promo_id": p.ID, "code": p.Code}); err != nil {
				return err
			}
			if err := sc.db.Model(&models.PromoAudience{}).Where("promo_id = ? AND user_id = ?", id, u.ID).Update("notified_at", now).Error; err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}
