package controllers

import (
	"io"

	"github.com/gofiber/fiber/v2"
	"ride-backend/middleware"
	"ride-backend/store"
)

func (oc *OperationsController) TicketConversation(c *fiber.Ctx) error {
	actorID, _ := middleware.CurrentUser(c)
	staff := middleware.CurrentStaff(c) != nil
	if staff {
		actorID = middleware.CurrentStaff(c).ID
	}
	page, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	conversation, err := oc.Store.TicketConversation(c.UserContext(), c.Params("id"), actorID, staff, page)
	if err != nil {
		return operationError(c, err)
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(conversation)
}

func (oc *OperationsController) TicketMessage(c *fiber.Ctx) error {
	actorID, _ := middleware.CurrentUser(c)
	staff := middleware.CurrentStaff(c) != nil
	if staff {
		actorID = middleware.CurrentStaff(c).ID
	}
	var input struct {
		Body string `json:"body"`
	}
	if len(c.Body()) > 8192 || c.BodyParser(&input) != nil {
		return operationError(c, store.FieldError("body", "invalid ticket message"))
	}
	message, err := oc.Store.AddTicketMessage(c.UserContext(), c.Params("id"), actorID, c.Get("Idempotency-Key"), input.Body, staff)
	if err != nil {
		return operationError(c, err)
	}
	return c.Status(201).JSON(message)
}

func (oc *OperationsController) TicketAttachment(c *fiber.Ctx) error {
	actorID, _ := middleware.CurrentUser(c)
	staff := middleware.CurrentStaff(c) != nil
	if staff {
		actorID = middleware.CurrentStaff(c).ID
	}
	if c.Method() == fiber.MethodGet {
		data, attachment, err := oc.Store.ReadTicketAttachment(c.UserContext(), c.Params("id"), c.Params("attachmentId"), actorID, staff)
		if err != nil {
			return operationError(c, err)
		}
		c.Set("Cache-Control", "private, no-store")
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("Content-Type", attachment.ContentType)
		c.Set("Content-Disposition", "attachment; filename=attachment")
		return c.Send(data)
	}
	file, err := c.FormFile("file")
	if err != nil || file.Size < 1 || file.Size > 5<<20 {
		return operationError(c, store.FieldError("file", "file must be 1 byte to 5 MiB"))
	}
	reader, err := file.Open()
	if err != nil {
		return operationError(c, store.FieldError("file", "file unavailable"))
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, 5<<20+1))
	if err != nil || len(data) > 5<<20 {
		return operationError(c, store.FieldError("file", "file must be 1 byte to 5 MiB"))
	}
	attachment, err := oc.Store.UploadTicketAttachment(c.UserContext(), c.Params("id"), actorID, c.Get("Idempotency-Key"), staff, data)
	if err != nil {
		return operationError(c, err)
	}
	return c.Status(201).JSON(attachment)
}
