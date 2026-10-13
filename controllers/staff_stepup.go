package controllers

import (
	"github.com/gofiber/fiber/v2"
	"ride-backend/middleware"
	"ride-backend/store"
	"time"
)

func (sc *StaffAuthController) stepUpRate(c *fiber.Ctx) error {
	staff := middleware.CurrentStaff(c)
	if staff == nil {
		return store.OpError(401, "Unauthorized")
	}
	if len(c.Body()) > 4096 {
		return store.OpError(400, "Invalid request body")
	}
	limiter := store.NewSQLAuthLimiter(sc.Store)
	decision, err := limiter.Allow(c.UserContext(), "staff_stepup_user", staff.ID, store.AuthRate{Limit: 10, Window: 10 * time.Minute})
	if err != nil {
		return store.OpError(503, "authentication temporarily unavailable")
	}
	if !decision.Allowed {
		return &store.OperationError{Status: 429, Message: "Too many requests", RetryAfter: decision.RetryAfter}
	}
	return nil
}
