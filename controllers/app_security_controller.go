package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
)

func (ac *AdminController) UserSessions(c *fiber.Ctx) error {
	id := c.Params("id")
	u, ok := ac.Store.GetUserByID(id)
	if !ok {
		return fiber.NewError(404, "User not found")
	}
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	rows, total, err := ac.Store.ListAppSessions(c.UserContext(), id, p)
	if err != nil {
		return authError(c, err)
	}
	return c.JSON(fiber.Map{"sessions": rows, "total": total, "limit": p.Limit, "offset": p.Offset, "locked_until": u.LockedUntil, "failed_login_count": u.FailedLoginCount})
}
func (ac *AdminController) RevokeUserSessions(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, ok := ac.Store.GetUserByID(id); !ok {
		return fiber.NewError(404, "User not found")
	}
	var input struct {
		SessionID string `json:"session_id"`
	}
	if len(c.Body()) > 0 && c.BodyParser(&input) != nil {
		return fiber.NewError(400, "Invalid request body")
	}
	if input.SessionID != "" {
		if _, err := uuid.Parse(input.SessionID); err != nil {
			return fiber.NewError(400, "Invalid session ID")
		}
		var count int64
		if err := ac.Store.DB().Model(&models.AppSession{}).Where("id = ? AND user_id = ?", input.SessionID, id).Count(&count).Error; err != nil {
			return operationError(c, err)
		}
		if count == 0 {
			return fiber.NewError(404, "Session not found")
		}
	}
	err := ac.Store.Transaction(func(tx *store.Store) error {
		var err error
		if input.SessionID == "" {
			err = tx.RevokeAllAppSessions(c.UserContext(), id)
		} else {
			err = tx.RevokeAppSession(c.UserContext(), id, input.SessionID)
		}
		if err != nil {
			return err
		}
		return tx.CreateAuditLog(middleware.CurrentStaff(c).ID, "revoke_app_sessions", "user", id, c.IP(), nil)
	})
	if err != nil {
		return authError(c, err)
	}
	return c.JSON(fiber.Map{"message": "Sessions revoked"})
}
func (ac *AdminController) UserSecurityEvents(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, ok := ac.Store.GetUserByID(id); !ok {
		return fiber.NewError(404, "User not found")
	}
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	rows, total, err := ac.Store.ListAppSecurityEvents(c.UserContext(), id, c.Query("type"), p)
	if err != nil {
		return authError(c, err)
	}
	return c.JSON(fiber.Map{"events": rows, "total": total, "limit": p.Limit, "offset": p.Offset})
}
func (ac *AdminController) UnlockUser(c *fiber.Ctx) error {
	err := ac.Store.UnlockAppLogin(c.UserContext(), c.Params("id"), middleware.CurrentStaff(c).ID, c.IP())
	if err != nil {
		return authError(c, err)
	}
	return c.JSON(fiber.Map{"message": "Account unlocked"})
}
