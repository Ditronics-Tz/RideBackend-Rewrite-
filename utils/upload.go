package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

var allowedImageExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
}

// SavePublicImage saves an image to ./uploads/public and returns "/api/v1/uploads/" + filename.
// Returns ("", nil) if field is absent.
func SavePublicImage(c *fiber.Ctx, field, prefix string) (string, error) {
	fh, err := c.FormFile(field)
	if err != nil {
		return "", nil // field not provided
	}

	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if !allowedImageExt[ext] {
		return "", fmt.Errorf("field %s: only jpg/jpeg/png/webp allowed", field)
	}
	if fh.Size > 5*1024*1024 {
		return "", fmt.Errorf("field %s: max file size is 5MB", field)
	}

	if err := os.MkdirAll("./uploads/public", 0o755); err != nil {
		return "", fmt.Errorf("could not create public upload dir")
	}

	name := fmt.Sprintf("%s_%d%s", prefix, time.Now().UnixNano(), ext)
	dst := filepath.Join("./uploads/public", name)
	if err := c.SaveFile(fh, dst); err != nil {
		return "", fmt.Errorf("could not save %s", field)
	}
	return "/api/v1/uploads/" + name, nil
}

// SaveLicenceImage saves a private licence document to ./uploads/licences
// and returns the filename (or "/api/v1/files/licences/" + filename).
func SaveLicenceImage(c *fiber.Ctx, field, prefix string) (string, error) {
	fh, err := c.FormFile(field)
	if err != nil {
		return "", nil
	}

	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if !allowedImageExt[ext] {
		return "", fmt.Errorf("field %s: only jpg/jpeg/png/webp allowed", field)
	}
	if fh.Size > 10*1024*1024 {
		return "", fmt.Errorf("field %s: max file size is 10MB", field)
	}

	if err := os.MkdirAll("./uploads/licences", 0o755); err != nil {
		return "", fmt.Errorf("could not create licence upload dir")
	}

	name := fmt.Sprintf("%s_%d%s", prefix, time.Now().UnixNano(), ext)
	dst := filepath.Join("./uploads/licences", name)
	if err := c.SaveFile(fh, dst); err != nil {
		return "", fmt.Errorf("could not save %s", field)
	}
	return "/api/v1/files/licences/" + name, nil
}

// SaveImageField is maintained for backwards compatibility, saves to public uploads.
func SaveImageField(c *fiber.Ctx, field, prefix string) (string, error) {
	return SavePublicImage(c, field, prefix)
}
