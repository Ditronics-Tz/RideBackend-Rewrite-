package routes

import (
	"github.com/gofiber/fiber/v2"
	"ride-backend/config"
	"ride-backend/controllers"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
)

func SetupPromoAdminRoutes(admin fiber.Router, pc *controllers.PromoController, cfg *config.Config) {
	read := middleware.RequireStaffRoles(cfg, models.StaffRoleAdmin, models.StaffRoleSupport)
	write := middleware.RequireStaffRoles(cfg, models.StaffRoleAdmin)
	admin.Post("/promos", write, pc.Create)
	admin.Get("/promos", read, pc.List)
	admin.Get("/promos/:id", read, pc.Get)
	admin.Patch("/promos/:id", write, pc.Update)
	admin.Get("/promos/:id/audience", read, pc.Audience)
	admin.Post("/promos/:id/audience", write, pc.Audience)
	admin.Delete("/promos/:id/audience/:userId", write, pc.RemoveAudience)
	admin.Post("/promos/:id/share", write, pc.Share)
	admin.Post("/promos/:id/:action", write, pc.SetStatus)
	admin.Get("/promos/:id/redemptions", read, pc.Redemptions)
	admin.Get("/users/:id/promos", read, pc.UserPromos)
	admin.Get("/referral-settings", read, pc.ReferralSettings)
	admin.Put("/referral-settings", write, pc.ReferralSettings)
	admin.Get("/referrals", read, pc.Referrals)
}
func SetupPromoAppRoutes(router fiber.Router, pc *controllers.PromoController, cfg *config.Config, s *store.Store) {
	protected := middleware.AppProtected(cfg, s)
	passenger := middleware.RequireAppRoles(models.RolePassenger)
	promos := router.Group("/promos", protected, passenger)
	promos.Get("/available", pc.Available)
	promos.Post("/validate", middleware.PromoValidationLimiter(), pc.Validate)
	referral := router.Group("/referral", protected, passenger)
	referral.Get("/me", pc.MyReferral)
	referral.Post("/apply", pc.ApplyReferral)
}
