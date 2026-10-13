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

	admin := router.Group("/admin", protected, func(c *fiber.Ctx) error {
		err := c.Next()
		if actor := middleware.CurrentStaff(c); actor != nil {
			target := c.Params("id")
			if target == "" {
				target = c.Params("documentId")
			}
			if target == "" {
				target = "collection"
			}
			if auditErr := s.CreateAuditLog(actor.ID, "admin_request", "route", target, c.IP(), fiber.Map{"route": c.Route().Path, "method": c.Method(), "status": c.Response().StatusCode(), "failed": err != nil}); auditErr != nil && err == nil {
				return fiber.NewError(500, "Unable to record staff audit event")
			}
		}
		return err
	})
	dc := controllers.NewDataRequestController(cfg, s)
	pc := controllers.NewProfileController(cfg, s)
	admin.Get("/data-requests", supportAndAdmin, dc.AdminList)
	admin.Get("/data-requests/:id", supportAndAdmin, dc.AdminDetail)
	admin.Post("/users/:id/deletion-request", adminOnly, dc.AdminRequestDeletion)
	admin.Post("/data-requests/:id/hold", adminOnly, dc.Hold)
	admin.Post("/data-requests/:id/release", adminOnly, dc.Release)
	admin.Post("/data-requests/:id/execute", adminOnly, dc.Execute)

	// Support + Admin permissions
	SetupPromoAdminRoutes(admin, controllers.NewPromoController(cfg, s), cfg)
	SetupFinanceAdminRoutes(admin, controllers.NewOperationsController(cfg, s), cfg)
	SetupOperationsAdminRoutes(admin, controllers.NewOperationsController(cfg, s), cfg)
	admin.Get("/users", supportAndAdmin, ac.ListUsers)
	admin.Get("/users/:id", supportAndAdmin, ac.GetUser)
	admin.Get("/users/:id/profile", supportAndAdmin, pc.GetFullProfileForStaff)
	admin.Get("/users/:id/sessions", supportAndAdmin, ac.UserSessions)
	admin.Post("/users/:id/revoke-sessions", supportAndAdmin, ac.RevokeUserSessions)
	admin.Get("/users/:id/security-events", supportAndAdmin, ac.UserSecurityEvents)
	admin.Post("/users/:id/unlock", adminOnly, ac.UnlockUser)
	admin.Post("/users/:id/suspend", supportAndAdmin, ac.ChangeAccountStatus)
	admin.Post("/users/:id/reactivate", supportAndAdmin, ac.ChangeAccountStatus)
	admin.Get("/users/:id/notes", supportAndAdmin, ac.UserNotes)
	admin.Post("/users/:id/notes", supportAndAdmin, ac.UserNotes)
	admin.Get("/users/:id/history", supportAndAdmin, ac.UserHistory)
	admin.Get("/verifications", supportAndAdmin, ac.VerificationQueue)
	admin.Post("/verifications/:id/claim", supportAndAdmin, ac.ClaimDriverVerification)
	admin.Patch("/documents/:documentId/review", supportAndAdmin, ac.ReviewDocumentByID)
	admin.Post("/drivers/:id/approve", supportAndAdmin, ac.FinalApproveDriver)
	admin.Patch("/drivers/:id/verify", supportAndAdmin, ac.VerifyDriver)
	admin.Get("/drivers/:id/verification", supportAndAdmin, ac.GetDriverVerification)
	admin.Post("/drivers/:id/verification/claim", supportAndAdmin, ac.ClaimDriverVerification)
	admin.Post("/drivers/:id/verification/release", supportAndAdmin, ac.ReleaseDriverVerification)
	admin.Post("/drivers/:id/documents/:documentId/reviews", supportAndAdmin, ac.ReviewDriverDocument)
	admin.Patch("/drivers/:id/suspension", supportAndAdmin, ac.SuspendDriverVerification)
	admin.Get("/stats", supportAndAdmin, ac.GetStats)
	admin.Get("/rides", supportAndAdmin, ac.ListRides)
	admin.Get("/complaints", supportAndAdmin, ac.ListComplaints)
	admin.Patch("/complaints/:id", supportAndAdmin, ac.UpdateComplaint)
	admin.Get("/tickets", supportAndAdmin, ac.ListTickets)
	admin.Patch("/tickets/:id", supportAndAdmin, ac.UpdateTicket)

	// Admin-only permissions
	admin.Patch("/users/:id/role", adminOnly, ac.UpdateUserRole)
	admin.Post("/users/:id/ban", adminOnly, ac.ChangeAccountStatus)
	admin.Delete("/users/:id/notes/:noteId", adminOnly, ac.DeleteUserNote)
	admin.Get("/audit-logs", adminOnly, ac.ListAuditLogs)

	// Staff management (Admin only)
	admin.Post("/staff", adminOnly, ac.CreateStaff)
	admin.Get("/staff", adminOnly, ac.ListStaff)
	admin.Patch("/staff/:id", adminOnly, ac.UpdateStaff)
	admin.Patch("/staff/:id/role", adminOnly, ac.UpdateStaffRole)
	admin.Post("/staff/:id/reset-password", adminOnly, ac.ResetStaffPassword)
	admin.Post("/staff/:id/revoke-sessions", adminOnly, ac.RevokeStaffSessions)
}
