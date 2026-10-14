package controllers

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"ride-backend/config"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
)

type PassengerController struct {
	Store *store.Store
	Cfg   *config.Config
}

func (pc *PassengerController) SavedPlaces(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	if c.Method() == fiber.MethodGet {
		places, err := pc.Store.ListSavedPlaces(c.UserContext(), uid)
		if err != nil {
			return operationError(c, err)
		}
		return c.JSON(fiber.Map{"places": places})
	}
	var input models.SavedPlaceInput
	if len(c.Body()) > 2048 || c.BodyParser(&input) != nil {
		return operationError(c, store.FieldError("body", "invalid saved place"))
	}
	place, err := pc.Store.SavePlace(c.UserContext(), uid, c.Params("id"), input)
	if err != nil {
		return operationError(c, err)
	}
	if c.Method() == fiber.MethodPost {
		return c.Status(201).JSON(place)
	}
	return c.JSON(place)
}

func (pc *PassengerController) DeleteSavedPlace(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	if err := pc.Store.DeleteSavedPlace(c.UserContext(), uid, c.Params("id")); err != nil {
		return operationError(c, err)
	}
	return c.SendStatus(204)
}

func (pc *PassengerController) Preferences(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	if c.Method() == fiber.MethodGet {
		prefs, err := pc.Store.PassengerPreferences(c.UserContext(), uid)
		if err != nil {
			return operationError(c, err)
		}
		return c.JSON(prefs)
	}
	var input struct {
		QuietRide       *bool   `json:"quiet_ride"`
		ExtraPickupTime *bool   `json:"extra_pickup_time"`
		ShareTripStatus *bool   `json:"share_trip_status"`
		PreciseLocation *bool   `json:"precise_location"`
		RideUpdates     *bool   `json:"ride_updates"`
		TripMessages    *bool   `json:"trip_messages"`
		Promotions      *bool   `json:"promotions"`
		EmailEnabled    *bool   `json:"email_enabled"`
		SMSEnabled      *bool   `json:"sms_enabled"`
		PushEnabled     *bool   `json:"push_enabled"`
		Language        *string `json:"language"`
	}
	if len(c.Body()) > 2048 || c.BodyParser(&input) != nil {
		return operationError(c, store.FieldError("body", "invalid preferences"))
	}
	updates := map[string]any{}
	for key, value := range map[string]*bool{"quiet_ride": input.QuietRide, "extra_pickup_time": input.ExtraPickupTime, "share_trip_status": input.ShareTripStatus, "precise_location": input.PreciseLocation, "ride_updates": input.RideUpdates, "trip_messages": input.TripMessages, "promotions": input.Promotions, "email_enabled": input.EmailEnabled, "sms_enabled": input.SMSEnabled, "push_enabled": input.PushEnabled} {
		if value != nil {
			updates[key] = *value
		}
	}
	if input.Language != nil {
		updates["language"] = strings.ToLower(strings.TrimSpace(*input.Language))
	}
	prefs, err := pc.Store.PatchPassengerPreferences(c.UserContext(), uid, updates)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(prefs)
}

func (pc *PassengerController) ScheduledRides(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	if c.Method() == fiber.MethodPost {
		var input models.ScheduledRideInput
		if len(c.Body()) > 4096 || c.BodyParser(&input) != nil {
			return operationError(c, store.FieldError("body", "invalid scheduled ride"))
		}
		_, distance, err := store.ValidateRideInput(models.CreateRideInput{Pickup: input.Pickup, Dropoff: input.Dropoff, VehicleType: input.VehicleType})
		if err != nil {
			return operationError(c, err)
		}
		if _, err := store.EstimatedRideFare(pc.Cfg, distance); err != nil {
			return operationError(c, err)
		}
		row, err := pc.Store.CreateScheduledRide(c.UserContext(), uid, c.Get("Idempotency-Key"), input)
		if err != nil {
			return operationError(c, err)
		}
		return c.Status(201).JSON(row.Response())
	}
	page, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	rows, total, err := pc.Store.ListScheduledRides(c.UserContext(), uid, c.Query("status"), page)
	if err != nil {
		return operationError(c, err)
	}
	items := make([]models.ScheduledRideResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, row.Response())
	}
	return c.JSON(fiber.Map{"scheduled_rides": items, "total": total, "limit": page.Limit, "offset": page.Offset})
}

func (pc *PassengerController) ScheduledRide(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	var row *models.ScheduledRide
	var err error
	if c.Method() == fiber.MethodPost {
		row, err = pc.Store.CancelScheduledRide(c.UserContext(), uid, c.Params("id"))
	} else {
		row, err = pc.Store.ScheduledRide(c.UserContext(), uid, c.Params("id"))
	}
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(row.Response())
}
