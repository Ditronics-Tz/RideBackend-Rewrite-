package routes

import (
	"ride-backend/controllers"

	"github.com/gofiber/fiber/v2"
)

func SetupAuthRoutes(app *fiber.App, authController *controllers.AuthController) {
	api := app.Group("/api/v1/auth")

	// Public auth routes
	api.Post("/register", authController.Register)
	api.Post("/login", authController.Login)

	// OAuth routes
	api.Get("/google", authController.GoogleLogin)
	api.Get("/google/callback", authController.GoogleCallback)

	// Example protected route
	api.Get("/me", middleware.Protected(authController.Cfg.JWTSecret), func(c *fiber.Ctx) error {
		userID := c.Locals("user_id")
		return c.JSON(fiber.Map{
			"message": "Access granted to protected endpoint",
			"user_id": userID,
		})
	})
}
