package routes

import (
	"ride-backend/controllers"
	"ride-backend/middleware"

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

	// Logged-in user info (any role)
	api.Get("/me",
		middleware.Protected(authController.Cfg.JWTSecret),
		authController.Me,
	)
}
