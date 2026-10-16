package controllers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
	"ride-backend/middleware"
	"ride-backend/store"
)

type AppNotificationController struct{ Store *store.Store }

func (nc *AppNotificationController) List(c *fiber.Ctx) error {
	userID, _ := middleware.CurrentUser(c)
	page, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	rows, total, unread, err := nc.Store.ListAppNotifications(c.UserContext(), userID, page)
	if err != nil {
		return operationError(c, err)
	}
	items := make([]fiber.Map, 0, len(rows))
	for _, row := range rows {
		data := map[string]string{}
		_ = json.Unmarshal([]byte(row.Data), &data)
		items = append(items, fiber.Map{"id": row.ID, "kind": row.Kind, "title": row.Title, "body": row.Body, "data": data, "read_at": row.ReadAt, "created_at": row.CreatedAt})
	}
	return c.JSON(fiber.Map{"notifications": items, "total": total, "unread_count": unread, "limit": page.Limit, "offset": page.Offset})
}

func (nc *AppNotificationController) MarkRead(c *fiber.Ctx) error {
	userID, _ := middleware.CurrentUser(c)
	if err := nc.Store.MarkAppNotificationRead(c.UserContext(), userID, c.Params("id")); err != nil {
		return operationError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (nc *AppNotificationController) MarkAllRead(c *fiber.Ctx) error {
	userID, _ := middleware.CurrentUser(c)
	if err := nc.Store.MarkAllAppNotificationsRead(c.UserContext(), userID); err != nil {
		return operationError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (nc *AppNotificationController) RegisterDevice(c *fiber.Ctx) error {
	if len(c.Body()) > 4608 {
		return operationError(c, store.FieldError("body", "push device body is too large"))
	}
	var input struct {
		Token string `json:"token"`
	}
	if err := c.BodyParser(&input); err != nil {
		return operationError(c, store.FieldError("token", "invalid push device body"))
	}
	userID, _ := middleware.CurrentUser(c)
	sessionID, _ := c.Locals("app_session_id").(string)
	device, err := nc.Store.RegisterPushDevice(c.UserContext(), userID, sessionID, input.Token)
	if err != nil {
		return operationError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": device.ID, "platform": device.Platform, "created_at": device.CreatedAt})
}

func (nc *AppNotificationController) Devices(c *fiber.Ctx) error {
	userID, _ := middleware.CurrentUser(c)
	devices, err := nc.Store.ListPushDevices(c.UserContext(), userID)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"devices": devices})
}

func (nc *AppNotificationController) UnregisterDevice(c *fiber.Ctx) error {
	userID, _ := middleware.CurrentUser(c)
	if err := nc.Store.UnregisterPushDevice(c.UserContext(), userID, c.Params("id")); err != nil {
		return operationError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
