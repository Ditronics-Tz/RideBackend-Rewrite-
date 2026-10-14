package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"ride-backend/config"
	"ride-backend/controllers"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
)

func SetupOperationsAppRoutes(r fiber.Router, oc *controllers.OperationsController, cfg *config.Config, s *store.Store) {
	support := r.Group("/support/tickets", middleware.AppProtected(cfg, s), middleware.RequireAppRoles(models.RoleDriver, models.RolePassenger))
	support.Get("", operationIDs, oc.SupportTickets)
	support.Post("", operationIDs, oc.SupportTickets)
	support.Get("/:id", operationIDs, oc.TicketConversation)
	support.Get("/:id/messages", operationIDs, oc.TicketConversation)
	support.Post("/:id/messages", operationIDs, oc.TicketMessage)
	support.Post("/:id/attachments", operationIDs, oc.TicketAttachment)
	support.Get("/:id/attachments/:attachmentId", operationIDs, oc.TicketAttachment)
	blocks := r.Group("/blocks", middleware.AppProtected(cfg, s), middleware.RequireAppRoles(models.RoleDriver, models.RolePassenger))
	blocks.Post("", operationIDs, oc.Blocks)
	blocks.Get("", operationIDs, oc.Blocks)
	blocks.Delete("/:userId", operationIDs, oc.Blocks)
	reports := r.Group("/reports", middleware.AppProtected(cfg, s), middleware.RequireAppRoles(models.RoleDriver, models.RolePassenger))
	reports.Post("", operationIDs, oc.AppReports)
	reports.Get("", operationIDs, oc.AppReports)
	contacts := r.Group("/emergency-contacts", middleware.AppProtected(cfg, s))
	contacts.Get("", operationIDs, oc.Contacts)
	contacts.Post("", operationIDs, oc.Contacts)
	contacts.Put("/:id", operationIDs, oc.Contacts)
	contacts.Delete("/:id", operationIDs, oc.Contacts)
	sos := r.Group("/sos", middleware.AppProtected(cfg, s))
	sos.Get("/:id", operationIDs, oc.AppSOS)
	sos.Post("/:id/:action", operationIDs, oc.AppSOS)
	r.Post("/rides/:rideId/sos", middleware.AppProtected(cfg, s), operationIDs, oc.AppSOS)
	r.Post("/rides/:rideId/share", middleware.AppProtected(cfg, s), operationIDs, oc.ShareRide)
	r.Delete("/rides/:rideId/share", middleware.AppProtected(cfg, s), operationIDs, oc.ShareRide)
	r.Get("/share/:token", controllers.PublicShareLimiter(), operationIDs, oc.PublicShare)
	incidents := r.Group("/incidents", middleware.AppProtected(cfg, s))
	incidents.Get("", operationIDs, oc.Incidents)
	incidents.Post("", operationIDs, oc.Incidents)
	incidents.Get("/files/:id", operationIDs, oc.IncidentAttachment)
	incidents.Post("/:id/attachments", operationIDs, oc.IncidentAttachment)
	r.Post("/rides/:rideId/location", middleware.AppProtected(cfg, s), operationIDs, oc.RideLocation)

}
func SetupOperationsAdminRoutes(r fiber.Router, oc *controllers.OperationsController, cfg *config.Config) {
	staff := middleware.RequireStaffRoles(cfg, models.StaffRoleAdmin, models.StaffRoleSupport)
	r.Get("/reports", staff, operationIDs, oc.Reports)
	r.Get("/reports/:id", staff, operationIDs, oc.ReportDetail)
	r.Post("/reports/:id/:action", staff, operationIDs, oc.ReportAction)
	r.Get("/flags", staff, operationIDs, oc.Flags)
	r.Post("/flags/:id/resolve", staff, operationIDs, oc.Flags)
	r.Get("/users/:id/reports", staff, operationIDs, oc.UserReports)
	r.Get("/sos/export", middleware.RequireStaffRoles(cfg, models.StaffRoleAdmin), operationIDs, oc.SOSCSV)
	r.Get("/sos/stream", staff, operationIDs, oc.SOSStream)
	r.Get("/sos", staff, operationIDs, oc.SOSList)
	r.Get("/sos/:id", staff, operationIDs, oc.SOSDetail)
	r.Post("/sos/:id/:action", staff, operationIDs, oc.SOSAction)
	r.Get("/incidents", staff, operationIDs, oc.Incidents)
	r.Get("/incidents/files/:id", staff, operationIDs, oc.IncidentAttachment)
	r.Get("/incidents/:id", staff, operationIDs, oc.IncidentDetail)
	r.Post("/incidents/:id/:action", staff, operationIDs, oc.IncidentAction)
	r.Get("/fraud/rides/:id/location", staff, operationIDs, oc.FraudRideTrail)
	r.Get("/fraud/stats", staff, operationIDs, oc.FraudStats)
	r.Get("/fraud/flags", staff, operationIDs, oc.FraudFlags)
	r.Get("/fraud/flags/:id", staff, operationIDs, oc.FraudDetail)
	r.Post("/fraud/flags/:id/:action", staff, operationIDs, oc.FraudAction)
	r.Get("/users/:id/fraud", staff, operationIDs, oc.UserFraud)
	r.Get("/fraud/rules", staff, operationIDs, oc.FraudRules)
	r.Put("/fraud/rules", middleware.RequireStaffRoles(cfg, models.StaffRoleAdmin), operationIDs, oc.FraudRules)
	r.Post("/fraud/rules/:key/dry-run", middleware.RequireStaffRoles(cfg, models.StaffRoleAdmin), operationIDs, oc.FraudDryRun)

}

func operationIDs(c *fiber.Ctx) error {
	for _, field := range []string{"id", "rideId", "userId"} {
		value := c.Params(field)
		if value != "" {
			if _, err := uuid.Parse(value); err != nil {
				return c.Status(400).JSON(fiber.Map{"error": "invalid UUID", "fields": fiber.Map{field: "valid UUID required"}})
			}
		}
	}
	return c.Next()
}
