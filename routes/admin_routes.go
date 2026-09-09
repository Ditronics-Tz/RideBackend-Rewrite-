package routes

import (
	"ride-backend/controllers"
	"ride-backend/middleware"
	"ride-backend/models"

	"github.com/gofiber/fiber/v2"
)

func SetupAdminRoutes(app *fiber.App, ac *controllers.AdminController, jwtSecret string) {
	protected := middleware.Protected(jwtSecret)
	staff := middleware.RequireRoles(models.RoleSupport, models.RoleMzee, models.RoleAdmin)
	elevated := middleware.RequireRoles(models.RoleMzee, models.RoleAdmin)

	admin := app.Group("/api/v1/admin", protected, staff)
	admin.Get("/users", ac.ListUsers)
	admin.Get("/users/:id", ac.GetUser)
	admin.Patch("/drivers/:id/verify", ac.VerifyDriver)

	// Role assignment is sensitive — admin + mzee only
	admin.Patch("/users/:id/role", elevated, ac.UpdateUserRole)
}
