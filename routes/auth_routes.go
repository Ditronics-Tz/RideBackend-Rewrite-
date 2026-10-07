package routes

import (
	"ride-backend/config"
	"ride-backend/controllers"
	"ride-backend/middleware"

	"github.com/gofiber/fiber/v2"
)

func SetupAuthRoutes(router fiber.Router, authController *controllers.AuthController, cfg *config.Config) {
	api := router.Group("/auth")

	// Public app auth routes
	api.Post("/register", authController.Register)
	api.Post("/login", authController.Login)

	// OAuth routes
	api.Get("/google", authController.GoogleLogin)
	api.Get("/google/callback", authController.GoogleCallback)

	// Logged-in app user info
	api.Get("/me",
		middleware.AppProtected(cfg),
		authController.Me,
	)
}
