package routes

import (
	"ride-backend/config"
	"ride-backend/controllers"
	"ride-backend/middleware"
	"ride-backend/store"

	"github.com/gofiber/fiber/v2"
)

func SetupStaffAuthRoutes(router fiber.Router, sc *controllers.StaffAuthController, cfg *config.Config, s *store.Store) {
	staffAuth := router.Group("/staff/auth")

	// Public routes
	staffAuth.Post("/login", sc.Login)
	staffAuth.Post("/refresh", sc.Refresh)

	// Protected routes
	protected := middleware.StaffProtected(cfg, s)
	staffAuth.Post("/logout", protected, sc.Logout)
	staffAuth.Post("/logout-all", protected, sc.LogoutAll)
	staffAuth.Get("/me", protected, sc.Me)
	staffAuth.Post("/change-password", protected, sc.ChangePassword)
	staffAuth.Post("/2fa/setup", protected, sc.Setup2FA)
	staffAuth.Post("/2fa/verify", protected, sc.Verify2FA)
	staffAuth.Post("/2fa/disable", protected, sc.Disable2FA)
}
