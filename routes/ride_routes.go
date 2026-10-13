package routes

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"ride-backend/config"
	"ride-backend/controllers"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
)

func rideIdempotencyKey(c *fiber.Ctx) error {
	key := strings.TrimSpace(c.Get("Idempotency-Key"))
	if len(key) < 8 || len(key) > 100 {
		return c.Status(400).JSON(fiber.Map{"error": "Idempotency-Key must contain 8-100 characters", "fields": fiber.Map{"Idempotency-Key": "required"}})
	}
	return c.Next()
}

func SetupRideRoutes(router fiber.Router, rc *controllers.RideController, cfg *config.Config, s *store.Store) {
	protected := middleware.AppProtected(cfg, s)
	passenger := middleware.RequireAppRoles(models.RolePassenger)
	driver := middleware.RequireAppRoles(models.RoleDriver)
	participant := middleware.RequireAppRoles(models.RolePassenger, models.RoleDriver)
	router.Post("/rides", protected, passenger, rideIdempotencyKey, rc.Create)
	router.Get("/rides", protected, participant, rc.List)
	router.Get("/rides/:id", protected, participant, operationIDs, rc.Detail)
	router.Post("/rides/:id/cancel", protected, participant, rideIdempotencyKey, operationIDs, rc.Cancel)
	router.Get("/driver/offers", protected, driver, rc.Offers)
	router.Post("/rides/:id/accept", protected, driver, rideIdempotencyKey, operationIDs, rc.Accept)
	router.Post("/rides/:id/decline", protected, driver, rideIdempotencyKey, operationIDs, rc.Decline)
	router.Post("/rides/:id/arrived", protected, driver, rideIdempotencyKey, operationIDs, rc.Arrived)
	router.Post("/rides/:id/start", protected, driver, rideIdempotencyKey, operationIDs, rc.Start)
	router.Post("/rides/:id/complete", protected, driver, rideIdempotencyKey, operationIDs, rc.Complete)
	router.Put("/driver/availability", protected, driver, rideIdempotencyKey, rc.SetAvailability)
	router.Post("/driver/location", protected, driver, rideIdempotencyKey, rc.Location)
}
