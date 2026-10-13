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
	api.Post("/refresh", authController.Refresh)
	protected := middleware.AppProtected(cfg, authController.Store)
	api.Post("/logout", protected, authController.Logout)
	api.Post("/logout-all", protected, authController.LogoutAll)
	api.Get("/sessions", protected, authController.Sessions)
	api.Delete("/sessions/:id", protected, authController.DeleteSession)
	api.Post("/phone/request-otp", middleware.AppProtected(cfg, authController.Store), authController.RequestPhoneOTP)
	api.Post("/phone/verify-otp", middleware.AppProtected(cfg, authController.Store), authController.VerifyPhoneOTP)

	// OAuth routes
	api.Get("/google", authController.GoogleLogin)
	api.Get("/google/callback", authController.GoogleCallback)

	// Logged-in app user info
	api.Get("/me",
		middleware.AppProtected(cfg, authController.Store),
		authController.Me,
	)
}
