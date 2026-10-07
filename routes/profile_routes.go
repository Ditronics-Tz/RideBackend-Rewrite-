package routes

import (
	"ride-backend/config"
	"ride-backend/controllers"
	"ride-backend/middleware"

	"github.com/gofiber/fiber/v2"
)

func SetupProfileRoutes(router fiber.Router, pc *controllers.ProfileController, cfg *config.Config) {
	protected := middleware.AppProtected(cfg)

	driver := router.Group("/driver", protected)
	// Driver self-service (driver role only — enforced in controller)
	driver.Get("/profile/me", pc.GetMyDriverProfile)
	driver.Put("/profile/me", pc.UpdateMyDriverProfile)
	driver.Post("/profile/me/images", pc.UploadMyDriverImages)
	// Public driver lookup (any logged-in app user)
	driver.Get("/profile/:userId", pc.GetDriverProfileByID)

	passenger := router.Group("/passenger", protected)
	passenger.Get("/profile/me", pc.GetMyPassengerProfile)
	passenger.Put("/profile/me", pc.UpdateMyPassengerProfile)
	passenger.Post("/profile/me/image", pc.UploadMyPassengerImage)
	passenger.Get("/profile/:userId", pc.GetPassengerProfileByID)
}
