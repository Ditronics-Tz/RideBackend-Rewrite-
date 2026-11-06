package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
	"strconv"
)

func (rc *RideController) TransportSettings(c *fiber.Ctx) error {
	s := rc.Store.WithContext(c.UserContext())
	if c.Method() == "PUT" {
		var in struct {
			Enabled     *bool  `json:"enabled"`
			SharedFee   *int64 `json:"shared_fee"`
			HailPercent *int   `json:"hail_percent"`
			Version     *int   `json:"version"`
			Reason      string `json:"reason"`
		}
		if c.BodyParser(&in) != nil || in.Enabled == nil || in.SharedFee == nil || in.HailPercent == nil || in.Version == nil {
			return fiber.NewError(400, "All fee settings and version are required")
		}
		updated, err := s.SaveTransportSettingsByStaff(middleware.CurrentStaff(c), models.TransportSettings{Enabled: *in.Enabled, SharedFee: *in.SharedFee, HailPercent: *in.HailPercent, Version: *in.Version}, in.Reason)
		if err != nil {
			return operationError(c, err)
		}
		return c.JSON(updated)
	}
	settings, err := s.TransportSettings()
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(settings)
}

func (rc *RideController) SharedTrips(staff bool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		actor, role := middleware.CurrentUser(c)
		scope := c.Query("scope", "nearby")
		roleName := string(role)
		if staff {
			scope = "staff"
			roleName = "staff"
		} else if scope == "staff" {
			return fiber.ErrForbidden
		}
		lat, e1 := strconv.ParseFloat(c.Query("lat"), 64)
		lng, e2 := strconv.ParseFloat(c.Query("lng"), 64)
		radius, e3 := strconv.ParseFloat(c.Query("radius_km", "10"), 64)
		if scope == "nearby" && (e1 != nil || e2 != nil || e3 != nil) {
			return fiber.NewError(400, "Supply pickup latitude and longitude")
		}
		result, err := rc.Store.WithContext(c.UserContext()).SharedList(actor, roleName, scope, rc.Cfg.JWTSecret, lat, lng, radius, c.QueryInt("limit", 50), c.QueryInt("offset", 0))
		if err != nil {
			return operationError(c, err)
		}
		c.Set("Cache-Control", "no-store")
		return c.JSON(fiber.Map{"trips": result})
	}
}

func (rc *RideController) SharedTrip(c *fiber.Ctx) error {
	actor, role := middleware.CurrentUser(c)
	id := c.Params("id")
	s := rc.Store.WithContext(c.UserContext())
	if id != "" {
		if _, err := uuid.Parse(id); err != nil {
			return fiber.ErrNotFound
		}
	}
	if c.Method() == "POST" {
		var err error
		if id == "" {
			var in models.SharedPublish
			if c.BodyParser(&in) != nil {
				return fiber.ErrBadRequest
			}
			id, err = s.PublishShared(actor, c.Get("Idempotency-Key"), in)
		} else {
			var in models.SharedAction
			if len(c.Body()) != 0 && c.BodyParser(&in) != nil {
				return fiber.ErrBadRequest
			}
			if c.Params("action") == "verify" {
				if _, e := uuid.Parse(in.SeatID); e != nil {
					return fiber.ErrBadRequest
				}
			}
			id, err = s.SharedAction(actor, string(role), id, c.Get("Idempotency-Key"), c.Params("action"), rc.Cfg.JWTSecret, in)
		}
		if err != nil {
			return operationError(c, err)
		}
	}
	detail, err := s.SharedDetail(id, actor, rc.Cfg.JWTSecret)
	if err != nil {
		return operationError(c, err)
	}
	c.Set("Cache-Control", "no-store")
	if c.Method() == "POST" {
		s.NotifyShared(detail)
	}
	return c.JSON(detail)
}

// The driver's authenticated active hailed ride is recovered after reconnect or restart.
func (rc *RideController) ActiveHail(c *fiber.Ctx) error {
	actor, _ := middleware.CurrentUser(c)
	r, err := rc.Store.WithContext(c.UserContext()).ActiveDriverHail(actor)
	if err != nil {
		return operationError(c, err)
	}
	if r == nil {
		return c.SendStatus(204)
	}
	out, err := rc.RideResponse(c, r)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(out)
}

func (rc *RideController) WaitForShared(c *fiber.Ctx) error {
	actor, _ := middleware.CurrentUser(c)
	s := rc.Store.WithContext(c.UserContext())
	id := c.Query("trip_id")
	lat, e1 := strconv.ParseFloat(c.Query("lat"), 64)
	lng, e2 := strconv.ParseFloat(c.Query("lng"), 64)
	if id != "" {
		if _, e := uuid.Parse(id); e != nil {
			return fiber.ErrBadRequest
		}
		v, e := s.SharedDetail(id, actor, rc.Cfg.JWTSecret)
		if e != nil {
			return operationError(c, e)
		}
		if actor != v.DriverID && v.MySeat == nil {
			return fiber.ErrNotFound
		}
	} else if e1 != nil || e2 != nil || !(lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180) {
		return fiber.ErrBadRequest
	}
	after, err := strconv.ParseInt(c.Query("after", "0"), 10, 64)
	if err != nil || after < 0 {
		return fiber.ErrBadRequest
	}
	radius, err := strconv.ParseFloat(c.Query("radius_km", "10"), 64)
	if err != nil || !(radius > 0 && radius <= 50) {
		return fiber.ErrBadRequest
	}
	changes, err := s.WaitSharedChanges(c.UserContext(), actor, id, lat, lng, radius, after)
	if err != nil {
		return operationError(c, err)
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(changes)
}

func (rc *RideController) StaffCancelShared(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return fiber.ErrBadRequest
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if c.BodyParser(&in) != nil {
		return fiber.ErrBadRequest
	}
	err := rc.Store.WithContext(c.UserContext()).StaffCancelShared(middleware.CurrentStaff(c), id, in.Reason)
	if err != nil {
		return operationError(c, err)
	}
	return c.SendStatus(204)
}

func (rc *RideController) SharedLocation(c *fiber.Ctx) error {
	actor, _ := middleware.CurrentUser(c)
	p, err := rc.Store.WithContext(c.UserContext()).SharedDriverLocation(c.UserContext(), c.Params("id"), actor)
	if err != nil {
		return operationError(c, err)
	}
	c.Set("Cache-Control", "no-store")
	if p == nil {
		return c.SendStatus(204)
	}
	return c.JSON(p)
}

func (rc *RideController) SharedSOS(c *fiber.Ctx) error {
	actor, _ := middleware.CurrentUser(c)
	var in store.SOSInput
	if c.BodyParser(&in) != nil {
		return fiber.ErrBadRequest
	}
	alert, err := rc.Store.WithContext(c.UserContext()).TriggerSharedSOS(actor, c.Params("id"), in)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(alert)
}
func (rc *RideController) TransportAssessments(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	rows := []models.TransportAssessment{}
	q := rc.Store.DB().Model(&models.TransportAssessment{})
	if status := c.Query("status"); status != "" {
		q = q.Where("status=?", status)
	}
	var total int64
	if err = q.Count(&total).Error; err != nil {
		return operationError(c, err)
	}
	if err = q.Order("created DESC,mode,trip_id").Limit(p.Limit).Offset(p.Offset).Find(&rows).Error; err != nil {
		return operationError(c, err)
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"assessments": rows, "total": total})
}

func (rc *RideController) StaffSharedDetail(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return fiber.ErrBadRequest
	}
	trip, err := rc.Store.SharedStaffDetail(id)
	if err != nil {
		return operationError(c, err)
	}
	var rows []models.TransportEvent
	if err = rc.Store.DB().Where("trip_id=?", id).Order("id").Limit(200).Find(&rows).Error; err != nil {
		return operationError(c, err)
	}
	timeline := []fiber.Map{}
	for _, e := range rows {
		timeline = append(timeline, fiber.Map{"id": e.ID, "version": e.Version, "type": e.Type, "actor_id": e.ActorID, "reason": e.Reason, "at": e.At})
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"trip": trip, "timeline": timeline})
}

func (rc *RideController) SharedRating(c *fiber.Ctx) error {
	actor, _ := middleware.CurrentUser(c)
	var input store.SharedRatingInput
	if c.BodyParser(&input) != nil {
		return fiber.ErrBadRequest
	}
	rating, err := rc.Store.WithContext(c.UserContext()).RateShared(actor, c.Params("id"), input)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(rating)
}
