package controllers

import (
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"ride-backend/storage"
	"ride-backend/store"
)

func objectStorageError(c *fiber.Ctx, err error) error {
	if errors.Is(err, storage.ErrPresignUnsupported) {
		return c.Status(501).JSON(fiber.Map{"error": "Presigned URLs are unavailable for local files; use the legacy upload/download endpoint"})
	}
	if errors.Is(err, storage.ErrNotFound) {
		return c.Status(404).JSON(fiber.Map{"error": "Stored file not found"})
	}
	return verificationError(c, err)
}

func (fc *FileController) PublicProfileImage(c *fiber.Ctx) error {
	if _, err := uuid.Parse(c.Params("id")); err != nil {
		return fiber.NewError(404, "Profile image not found")
	}
	key, err := fc.Store.PublicProfilePictureKey(c.Params("id"))
	if err != nil {
		return objectStorageError(c, err)
	}
	if key == "" || fc.Cfg.R2PublicBaseURL == "" {
		return fiber.NewError(404, "Profile image not found")
	}
	if err := storage.ValidateKey(key); err != nil {
		return fiber.NewError(404, "Profile image not found")
	}
	c.Set("Cache-Control", "public, max-age=60")
	return c.Redirect(strings.TrimRight(fc.Cfg.R2PublicBaseURL, "/")+"/"+key, fiber.StatusFound)
}

func (pc *ProfileController) PresignDriverDocument(c *fiber.Ctx) error {
	id, err := pc.driverIdentity(c)
	if err != nil {
		return err
	}
	var in store.PresignDocumentInput
	if err := c.BodyParser(&in); err != nil {
		return operationError(c, store.FieldError("body", "invalid document upload request"))
	}
	doc, upload, err := pc.Store.PresignDriverDocument(c.UserContext(), id, in)
	if err != nil {
		return objectStorageError(c, err)
	}
	c.Set("Cache-Control", "private, no-store")
	return c.Status(201).JSON(fiber.Map{"document": doc, "upload": upload})
}

func (pc *ProfileController) ConfirmDriverDocument(c *fiber.Ctx) error {
	id, err := pc.driverIdentity(c)
	if err != nil {
		return err
	}
	if _, err := uuid.Parse(c.Params("id")); err != nil {
		return operationError(c, store.FieldError("id", "valid document UUID required"))
	}
	doc, err := pc.Store.ConfirmDriverDocument(c.UserContext(), id, c.Params("id"))
	if err != nil {
		return objectStorageError(c, err)
	}
	return c.JSON(doc)
}

func (fc *FileController) objectDocument(c *fiber.Ctx, key, contentType string, size int64) error {
	if fc.Store.Storage == nil {
		return operationError(c, store.OpError(503, "object storage is not configured"))
	}
	signed, err := fc.Store.Storage.PresignGet(c.UserContext(), storage.Private, key, 5*time.Minute)
	if err != nil {
		return objectStorageError(c, err)
	}
	c.Set("Cache-Control", "private, no-store")
	c.Set("X-Content-Type-Options", "nosniff")
	if c.QueryBool("presign") {
		return c.JSON(fiber.Map{"url": signed.URL, "method": signed.Method, "headers": signed.Headers, "expires_at": signed.ExpiresAt, "content_type": contentType, "size": size})
	}
	return c.Redirect(signed.URL, fiber.StatusFound)
}
