package routes

import (
	"github.com/gofiber/fiber/v2"
	"ride-backend/config"
	"ride-backend/controllers"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
)

func SetupDataRequestRoutes(router fiber.Router, dc *controllers.DataRequestController, cfg *config.Config, s *store.Store) {
	app := router.Group("/account", middleware.AppProtected(cfg, s), middleware.RequireAppRoles(models.RolePassenger, models.RoleDriver))
	app.Post("/deletion-request", middleware.AccountRequestLimiter(), dc.RequestDeletion)
	app.Delete("/deletion-request", dc.CancelDeletion)
	app.Get("/deletion-request", dc.DeletionStatus)
	app.Post("/export-request", middleware.AccountRequestLimiter(), dc.RequestExport)
	app.Get("/export-requests", dc.ExportRequests)
	app.Get("/export-requests/:id/download", dc.DownloadExport)
}
