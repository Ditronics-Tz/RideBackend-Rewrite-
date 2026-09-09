package routes

import (
	"ride-backend/controllers"
	"ride-backend/middleware"
	"ride-backend/models"

	"github.com/gofiber/fiber/v2"
)

func SetupProfileRoutes(app *fiber.App, pc *controllers.ProfileController, jwtSecret string) {
	protected := middleware.Protected(jwtSecret)

	driver := app.Group("/api/v1/driver", protected)
	// Driver self-service (driver role only — enforced in controller)
	driver.Get("/profile/me", pc.GetMyDriverProfile)
	driver.Put("/profile/me", pc.UpdateMyDriverProfile)
	driver.Post("/profile/me/images", pc.UploadMyDriverImages)
	// Public driver lookup (any logged-in user)
	driver.Get("/profile/:userId", pc.GetDriverProfileByID)

	passenger := app.Group("/api/v1/passenger", protected)
	passenger.Get("/profile/me", pc.GetMyPassengerProfile)
	passenger.Put("/profile/me", pc.UpdateMyPassengerProfile)
	passenger.Post("/profile/me/image", pc.UploadMyPassengerImage)
	passenger.Get("/profile/:userId", pc.GetPassengerProfileByID)

	_ = models.RoleDriver // keep models import if unused in future guards
}
