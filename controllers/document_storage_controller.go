package controllers

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"ride-backend/models"
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

func (pc *ProfileController) objectProfileImage(c *fiber.Ctx, user string, driver bool) error {
	file, err := c.FormFile("profile_picture")
	if err != nil {
		return operationError(c, store.FieldError("profile_picture", "provide a profile picture"))
	}
	if file.Size < 1 || file.Size > 5*1024*1024 {
		return operationError(c, store.FieldError("profile_picture", "profile picture must contain 1-5242880 bytes"))
	}
	stream, err := file.Open()
	if err != nil {
		return operationError(c, store.FieldError("profile_picture", "unable to read picture"))
	}
	data, readErr := io.ReadAll(io.LimitReader(stream, 5*1024*1024+1))
	closeErr := stream.Close()
	if readErr != nil || closeErr != nil || int64(len(data)) != file.Size {
		return operationError(c, store.FieldError("profile_picture", "unable to read picture"))
	}
	mime := http.DetectContentType(data)
	ext, ok := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}[mime]
	if !ok {
		return operationError(c, store.FieldError("profile_picture", "JPEG, PNG or WebP image required"))
	}
	if pc.Store.Storage == nil {
		return operationError(c, store.OpError(503, "object storage is not configured"))
	}
	key := "profile/" + user + "/" + uuid.NewString() + ext
	if _, err := pc.Store.Storage.Put(c.UserContext(), storage.Public, key, storage.Upload{Body: bytes.NewReader(data), Size: int64(len(data)), ContentType: mime}); err != nil {
		return objectStorageError(c, err)
	}
	model := any(&models.PassengerProfile{})
	if driver {
		pc.Store.EnsureDriverProfile(user)
		model = &models.DriverProfile{}
	} else {
		pc.Store.EnsurePassengerProfile(user, "")
	}
	result := pc.Store.DB().Model(model).Where("user_id = ?", user).Updates(map[string]any{"profile_picture_key": key, "profile_picture_url": ""})
	if result.Error != nil {
		return verificationError(c, result.Error)
	}
	if result.RowsAffected != 1 {
		return operationError(c, store.OpError(409, "profile picture changed"))
	}
	if driver {
		return pc.GetMyDriverProfile(c)
	}
	return pc.GetMyPassengerProfile(c)
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
