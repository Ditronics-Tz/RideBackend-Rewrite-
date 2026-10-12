package controllers

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
	"io"
	"ride-backend/config"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
	"strconv"
	"strings"
	"time"
)

type PromoController struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewPromoController(cfg *config.Config, s *store.Store) *PromoController {
	return &PromoController{Cfg: cfg, Store: s}
}
func promoError(c *fiber.Ctx, err error) error {
	var invalid *store.PromoError
	if errors.As(err, &invalid) {
		status := 400
		if invalid.Duplicate {
			status = 409
		}
		return c.Status(status).JSON(fiber.Map{"error": invalid.Message, "fields": invalid.Fields})
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.Status(404).JSON(fiber.Map{"error": "Record not found"})
	}
	return c.Status(500).JSON(fiber.Map{"error": "Unable to process promotion or referral"})
}
func invalidPromoBody(c *fiber.Ctx, field, message string) error {
	return promoError(c, &store.PromoError{Message: message, Fields: map[string]string{field: message}})
}
func decodePromoBody(c *fiber.Ctx, out interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(c.Body()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
func promoPage(c *fiber.Ctx) (int, int, error) {
	limit, err := strconv.Atoi(c.Query("limit", "20"))
	if err != nil || limit < 1 || limit > 100 {
		return 0, 0, &store.PromoError{Message: "limit must be 1-100", Fields: map[string]string{"limit": "limit must be 1-100"}}
	}
	offset, err := strconv.Atoi(c.Query("offset", "0"))
	if err != nil || offset < 0 {
		return 0, 0, &store.PromoError{Message: "offset must not be negative", Fields: map[string]string{"offset": "offset must not be negative"}}
	}
	return limit, offset, nil
}
func (pc *PromoController) write(c *fiber.Ctx, action string, fn func(*store.Store) (interface{}, error)) error {
	var result interface{}
	err := pc.Store.Transaction(func(sc *store.Store) error {
		var err error
		result, err = fn(sc)
		if err != nil {
			return err
		}
		target := c.Params("id")
		if target == "" {
			target = "settings"
		}
		if p, ok := result.(*models.PromoCode); ok {
			target = p.ID
		}
		return sc.CreateAuditLog(middleware.CurrentStaff(c).ID, action, "promo", target, c.IP(), nil)
	})
	if err != nil {
		return promoError(c, err)
	}
	if p, ok := result.(*models.PromoCode); ok {
		result = store.PromoResponse(*p, time.Now().UTC())
	}
	status := 200
	if action == "create_promo" {
		status = 201
	}
	return c.Status(status).JSON(result)
}

type promoCreateInput struct {
	Code            string    `json:"code"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	DiscountType    string    `json:"discount_type"`
	DiscountValue   int64     `json:"discount_value"`
	MaxDiscount     *int64    `json:"max_discount"`
	MinFare         int64     `json:"min_fare"`
	Audience        string    `json:"audience"`
	FirstRideOnly   bool      `json:"first_ride_only"`
	StartsAt        time.Time `json:"starts_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	MaxRedemptions  *int64    `json:"max_redemptions"`
	MaxPerUser      int64     `json:"max_per_user"`
	Status          string    `json:"status"`
	AudienceUserIDs []string  `json:"audience_user_ids"`
}

func (pc *PromoController) Create(c *fiber.Ctx) error {
	var input promoCreateInput
	if err := decodePromoBody(c, &input); err != nil {
		return invalidPromoBody(c, "body", "invalid promo body, field type or timestamp")
	}
	p := &models.PromoCode{Code: input.Code, Title: input.Title, Description: input.Description, DiscountType: input.DiscountType, DiscountValue: input.DiscountValue, MaxDiscount: input.MaxDiscount, MinFare: input.MinFare, Audience: input.Audience, FirstRideOnly: input.FirstRideOnly, StartsAt: input.StartsAt, ExpiresAt: input.ExpiresAt, MaxRedemptions: input.MaxRedemptions, MaxPerUser: input.MaxPerUser, Status: input.Status}
	return pc.write(c, "create_promo", func(sc *store.Store) (interface{}, error) {
		return p, sc.CreatePromo(p, middleware.CurrentStaff(c).ID, input.AudienceUserIDs)
	})
}
func (pc *PromoController) List(c *fiber.Ctx) error {
	limit, offset, err := promoPage(c)
	if err != nil {
		return promoError(c, err)
	}
	rows, total, err := pc.Store.ListPromos(c.Query("status"), c.Query("audience"), c.Query("q"), limit, offset, time.Now().UTC())
	if err != nil {
		return promoError(c, err)
	}
	return c.JSON(fiber.Map{"total": total, "limit": limit, "offset": offset, "promos": rows})
}
func (pc *PromoController) Get(c *fiber.Ctx) error {
	result, err := pc.Store.PromoDetail(c.Params("id"), time.Now().UTC())
	if err != nil {
		return promoError(c, err)
	}
	return c.JSON(result)
}
func (pc *PromoController) Update(c *fiber.Ctx) error {
	var input map[string]json.RawMessage
	if err := decodePromoBody(c, &input); err != nil || input == nil {
		return invalidPromoBody(c, "body", "invalid promo patch")
	}
	return pc.write(c, "update_promo", func(sc *store.Store) (interface{}, error) { return sc.UpdatePromo(c.Params("id"), input) })
}
func (pc *PromoController) SetStatus(c *fiber.Ctx) error {
	action := c.Params("action")
	status := ""
	switch action {
	case "pause":
		status = "paused"
	case "resume":
		status = "active"
	case "archive":
		status = "archived"
	default:
		return c.Status(404).JSON(fiber.Map{"error": "Action not found"})
	}
	return pc.write(c, action+"_promo", func(sc *store.Store) (interface{}, error) { return sc.SetPromoStatus(c.Params("id"), status) })
}
func (pc *PromoController) Audience(c *fiber.Ctx) error {
	if c.Method() == "POST" {
		var input store.AudienceInput
		if err := decodePromoBody(c, &input); err != nil {
			return invalidPromoBody(c, "body", "invalid audience body")
		}
		return pc.write(c, "add_promo_audience", func(sc *store.Store) (interface{}, error) {
			return sc.AddPromoAudience(c.Params("id"), middleware.CurrentStaff(c).ID, input)
		})
	}
	limit, offset, err := promoPage(c)
	if err != nil {
		return promoError(c, err)
	}
	rows, total, err := pc.Store.ListPromoAudience(c.Params("id"), c.Query("q"), limit, offset)
	if err != nil {
		return promoError(c, err)
	}
	return c.JSON(fiber.Map{"total": total, "limit": limit, "offset": offset, "audience": rows})
}
func (pc *PromoController) RemoveAudience(c *fiber.Ctx) error {
	return pc.write(c, "remove_promo_audience", func(sc *store.Store) (interface{}, error) {
		err := sc.RemovePromoAudience(c.Params("id"), c.Params("userId"))
		return fiber.Map{"message": "audience grant removed"}, err
	})
}
func (pc *PromoController) Share(c *fiber.Ctx) error {
	return pc.write(c, "share_promo", func(sc *store.Store) (interface{}, error) {
		count, err := sc.SharePromo(c.UserContext(), c.Params("id"), middleware.CurrentStaff(c).ID)
		return fiber.Map{"notified_count": count}, err
	})
}
func (pc *PromoController) Redemptions(c *fiber.Ctx) error {
	limit, offset, err := promoPage(c)
	if err != nil {
		return promoError(c, err)
	}
	rows, total, err := pc.Store.ListPromoRedemptions(c.Params("id"), limit, offset)
	if err != nil {
		return promoError(c, err)
	}
	return c.JSON(fiber.Map{"total": total, "limit": limit, "offset": offset, "redemptions": rows})
}
func (pc *PromoController) UserPromos(c *fiber.Ctx) error {
	result, err := pc.Store.UserPromos(c.Params("id"), time.Now().UTC())
	if err != nil {
		return promoError(c, err)
	}
	return c.JSON(result)
}
func (pc *PromoController) ReferralSettings(c *fiber.Ctx) error {
	if c.Method() == "PUT" {
		var input models.ReferralSettings
		if err := decodePromoBody(c, &input); err != nil {
			return invalidPromoBody(c, "body", "invalid referral settings")
		}
		return pc.write(c, "update_referral_settings", func(sc *store.Store) (interface{}, error) { return sc.UpdateReferralSettings(input) })
	}
	result, err := pc.Store.GetReferralSettings()
	if err != nil {
		return promoError(c, err)
	}
	return c.JSON(result)
}
func (pc *PromoController) Referrals(c *fiber.Ctx) error {
	limit, offset, err := promoPage(c)
	if err != nil {
		return promoError(c, err)
	}
	rows, total, err := pc.Store.ListReferrals(c.Query("status"), limit, offset)
	if err != nil {
		return promoError(c, err)
	}
	return c.JSON(fiber.Map{"total": total, "limit": limit, "offset": offset, "referrals": rows})
}
func (pc *PromoController) Available(c *fiber.Ctx) error {
	fare, err := strconv.ParseInt(c.Query("fare", "0"), 10, 64)
	if err != nil || fare < 0 {
		return invalidPromoBody(c, "fare", "fare must be a non-negative whole TZS amount")
	}
	id, _ := middleware.CurrentUser(c)
	rows, err := pc.Store.AvailablePromos(id, fare, time.Now().UTC())
	if err != nil {
		return promoError(c, err)
	}
	return c.JSON(fiber.Map{"promos": rows})
}
func (pc *PromoController) Validate(c *fiber.Ctx) error {
	var input struct {
		Code string `json:"code"`
		Fare *int64 `json:"fare"`
	}
	if err := decodePromoBody(c, &input); err != nil {
		return invalidPromoBody(c, "body", "invalid validation body")
	}
	if input.Fare == nil || *input.Fare < 0 {
		return invalidPromoBody(c, "fare", "fare must be a non-negative whole TZS amount")
	}
	id, _ := middleware.CurrentUser(c)
	discount, p, err := pc.Store.EvaluatePromo(id, input.Code, *input.Fare, time.Now().UTC())
	if err != nil {
		return promoError(c, err)
	}
	return c.JSON(fiber.Map{"valid": true, "discount_amount": discount, "final_fare": *input.Fare - discount, "promo": fiber.Map{"code": p.Code, "title": p.Title}})
}
func (pc *PromoController) MyReferral(c *fiber.Ctx) error {
	id, _ := middleware.CurrentUser(c)
	code, err := pc.Store.GetOrCreateReferralCode(id)
	if err != nil {
		return promoError(c, err)
	}
	stats, err := pc.Store.ReferralStats(id)
	if err != nil {
		return promoError(c, err)
	}
	return c.JSON(fiber.Map{"code": code.Code, "share_text": "Join Rida with my referral code " + code.Code, "stats": stats})
}
func (pc *PromoController) ApplyReferral(c *fiber.Ctx) error {
	var input struct {
		Code string `json:"code"`
	}
	if err := decodePromoBody(c, &input); err != nil {
		return invalidPromoBody(c, "body", "invalid referral body")
	}
	id, _ := middleware.CurrentUser(c)
	result, err := pc.Store.ApplyReferralCode(id, strings.TrimSpace(input.Code))
	if err != nil {
		return promoError(c, err)
	}
	return c.JSON(result)
}
