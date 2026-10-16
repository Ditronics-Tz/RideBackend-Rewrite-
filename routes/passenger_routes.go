package routes

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"ride-backend/config"
	"ride-backend/controllers"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
)

func SetupPassengerRoutes(r fiber.Router, cfg *config.Config, s *store.Store) {
	pc := &controllers.PassengerController{Store: s, Cfg: cfg}
	guard := middleware.AppProtected(cfg, s)
	role := middleware.RequireAppRoles(models.RolePassenger)
	private := func(c *fiber.Ctx) error { c.Set("Cache-Control", "private, no-store"); return c.Next() }
	passenger := r.Group("/passenger", guard, role, private)
	passenger.Get("/saved-places", pc.SavedPlaces)
	passenger.Post("/saved-places", pc.SavedPlaces)
	passenger.Put("/saved-places/:id", pc.SavedPlaces)
	passenger.Delete("/saved-places/:id", pc.DeleteSavedPlace)
	passenger.Get("/preferences", pc.Preferences)
	passenger.Patch("/preferences", pc.Preferences)
	nc := &controllers.AppNotificationController{Store: s}
	passenger.Get("/notifications", nc.List)
	passenger.Get("/notifications/updates", middleware.WindowLimit(60, time.Minute, func(c *fiber.Ctx) string { id, _ := middleware.CurrentUser(c); return id }), nc.Updates)
	passenger.Post("/notifications/read-all", nc.MarkAllRead)
	passenger.Post("/notifications/:id/read", nc.MarkRead)
	passenger.Get("/push-devices", nc.Devices)
	passenger.Post("/push-devices", nc.RegisterDevice)
	passenger.Delete("/push-devices/:id", nc.UnregisterDevice)
	schedules := r.Group("/scheduled-rides", guard, role, private)
	schedules.Get("", pc.ScheduledRides)
	schedules.Post("", pc.ScheduledRides)
	schedules.Get("/:id", pc.ScheduledRide)
	schedules.Post("/:id/cancel", pc.ScheduledRide)
}
