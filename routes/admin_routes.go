package routes

import (
	"ride-backend/config"
	"ride-backend/controllers"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"

	"github.com/gofiber/fiber/v2"
)

func SetupAdminRoutes(router fiber.Router, ac *controllers.AdminController, cfg *config.Config, s *store.Store) {
	protected := middleware.StaffProtected(cfg, s)
	adminOnly := middleware.RequireStaffRoles(cfg, models.StaffRoleAdmin)
	supportAndAdmin := middleware.RequireStaffRoles(cfg, models.StaffRoleSupport, models.StaffRoleAdmin)

	admin := router.Group("/admin", protected)

	// Support + Admin permissions
	admin.Get("/users", supportAndAdmin, ac.ListUsers)
	admin.Get("/users/:id", supportAndAdmin, ac.GetUser)
	admin.Patch("/drivers/:id/verify", supportAndAdmin, ac.VerifyDriver)
	admin.Get("/stats", supportAndAdmin, ac.GetStats)
	admin.Get("/rides", supportAndAdmin, ac.ListRides)
	admin.Get("/complaints", supportAndAdmin, ac.ListComplaints)
	admin.Patch("/complaints/:id", supportAndAdmin, ac.UpdateComplaint)
	admin.Get("/tickets", supportAndAdmin, ac.ListTickets)
	admin.Patch("/tickets/:id", supportAndAdmin, ac.UpdateTicket)

	// Admin-only permissions
	admin.Patch("/users/:id/role", adminOnly, ac.UpdateUserRole)
	admin.Get("/audit-logs", adminOnly, ac.ListAuditLogs)

	// Staff management (Admin only)
	admin.Post("/staff", adminOnly, ac.CreateStaff)
	admin.Get("/staff", adminOnly, ac.ListStaff)
	admin.Patch("/staff/:id", adminOnly, ac.UpdateStaff)
	admin.Patch("/staff/:id/role", adminOnly, ac.UpdateStaffRole)
	admin.Post("/staff/:id/reset-password", adminOnly, ac.ResetStaffPassword)
	admin.Post("/staff/:id/revoke-sessions", adminOnly, ac.RevokeStaffSessions)
}
