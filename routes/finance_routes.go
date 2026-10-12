package routes

import (
	"github.com/gofiber/fiber/v2"
	"ride-backend/config"
	"ride-backend/controllers"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
	"strings"
	"time"
)

func moneyKey(c *fiber.Ctx) error {
	key := strings.TrimSpace(c.Get("Idempotency-Key"))
	if len(key) < 8 || len(key) > 100 {
		return c.Status(400).JSON(fiber.Map{"error": "Idempotency-Key must contain 8-100 characters", "fields": fiber.Map{"Idempotency-Key": "required"}})
	}
	return c.Next()
}
func SetupFinanceAppRoutes(r fiber.Router, oc *controllers.OperationsController, cfg *config.Config, s *store.Store) {
	protected := middleware.AppProtected(cfg, s)
	passenger := middleware.RequireAppRoles(models.RolePassenger)
	driver := middleware.RequireAppRoles(models.RoleDriver)
	limit := middleware.WindowLimit(5, time.Minute, func(c *fiber.Ctx) string { uid, _ := middleware.CurrentUser(c); return uid })
	r.Get("/wallet", protected, operationIDs, oc.Wallet)
	r.Get("/wallet/ledger", protected, operationIDs, oc.Wallet)
	r.Post("/wallet/topups", protected, passenger, limit, moneyKey, operationIDs, oc.Pay)
	r.Post("/rides/:rideId/pay", protected, passenger, limit, moneyKey, operationIDs, oc.Pay)
	r.Get("/payments", protected, operationIDs, oc.Payments)
	r.Get("/payments/:id", protected, operationIDs, oc.PaymentDetail)
	r.Get("/driver/earnings", protected, driver, operationIDs, oc.Earnings)
	r.Post("/driver/payouts", protected, driver, limit, moneyKey, operationIDs, oc.Payouts)
	r.Get("/driver/payouts", protected, driver, operationIDs, oc.Payouts)
	r.Delete("/driver/payouts/:id", protected, driver, operationIDs, oc.Payouts)
	r.Post("/webhooks/payments/:provider", operationIDs, oc.PaymentWebhook)
}
func SetupFinanceAdminRoutes(r fiber.Router, oc *controllers.OperationsController, cfg *config.Config) {
	staff := middleware.RequireStaffRoles(cfg, models.StaffRoleAdmin, models.StaffRoleSupport)
	admin := middleware.RequireStaffRoles(cfg, models.StaffRoleAdmin)
	r.Get("/payments/summary", admin, operationIDs, oc.FinanceSummary)
	r.Get("/payments/export", admin, operationIDs, oc.FinanceCSV)
	r.Get("/payments", staff, operationIDs, oc.Payments)
	r.Get("/payments/:id", staff, operationIDs, oc.PaymentDetail)
	r.Post("/payments/:id/refund", staff, moneyKey, operationIDs, oc.RefundRequest)
	r.Get("/refunds", staff, operationIDs, oc.Refunds)
	r.Post("/refunds/:id/:action", staff, moneyKey, operationIDs, oc.RefundAction)
	r.Get("/payouts", admin, operationIDs, oc.Payouts)
	r.Post("/payouts/batch-approve", admin, moneyKey, operationIDs, oc.PayoutAction)
	r.Post("/payouts/:id/:action", admin, moneyKey, operationIDs, oc.PayoutAction)
	r.Get("/wallets/:userId", admin, operationIDs, oc.Wallet)
	r.Post("/wallets/:userId/adjust", admin, moneyKey, operationIDs, oc.AdjustWallet)
	r.Get("/payment-settings", admin, operationIDs, oc.PaymentSettings)
	r.Put("/payment-settings", admin, operationIDs, oc.PaymentSettings)
	r.Get("/reconciliation/runs", admin, operationIDs, oc.Reconciliation)
	r.Post("/reconciliation/run", admin, moneyKey, operationIDs, oc.Reconciliation)
	r.Get("/reconciliation/exceptions", admin, operationIDs, oc.Reconciliation)
	r.Post("/reconciliation/exceptions/:id/resolve", admin, operationIDs, oc.Reconciliation)
}
