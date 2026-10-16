package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
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

// Updates holds a request briefly until the durable inbox changes. The client
// then fetches the inbox page; this endpoint never sends notification content.
func (nc *AppNotificationController) Updates(c *fiber.Ctx) error {
	after := c.Query("after")
	if after != "" {
		if _, err := uuid.Parse(after); err != nil {
			return operationError(c, store.FieldError("after", "invalid notification ID"))
		}
	}
	userID, _ := middleware.CurrentUser(c)
	sessionID, _ := c.Locals("app_session_id").(string)
	ctx, cancel := context.WithTimeout(c.UserContext(), 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		latest, err := nc.Store.LatestAppNotificationID(ctx, userID)
		if err != nil {
			return operationError(c, err)
		}
		if latest != after && latest != "" {
			if err := nc.Store.ValidateAppSession(ctx, userID, sessionID); err != nil {
				return notificationSessionError(c, err)
			}
			return c.JSON(fiber.Map{"changed": true, "latest_id": latest})
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return ctx.Err()
			}
			if err := nc.Store.ValidateAppSession(c.UserContext(), userID, sessionID); err != nil {
				return notificationSessionError(c, err)
			}
			return c.JSON(fiber.Map{"changed": false, "latest_id": after})
		}
	}
}

func notificationSessionError(c *fiber.Ctx, err error) error {
	if errors.Is(err, store.ErrAuthUnavailable) {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "authentication temporarily unavailable", "code": "AUTH_TEMPORARILY_UNAVAILABLE"})
	}
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid credentials"})
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
