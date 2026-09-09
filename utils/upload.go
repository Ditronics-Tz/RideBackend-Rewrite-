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

// SaveImageField saves a multipart file field to ./uploads and returns its public URL path.
// Returns ("", nil) when the field is absent so callers can treat it as "no upload".
func SaveImageField(c *fiber.Ctx, field, prefix string) (string, error) {
	fh, err := c.FormFile(field)
	if err != nil {
		return "", nil // field not provided — not an error
	}

	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if !allowedImageExt[ext] {
		return "", fmt.Errorf("field %s: only jpg/jpeg/png/webp allowed", field)
	}
	if fh.Size > 5*1024*1024 {
		return "", fmt.Errorf("field %s: max file size is 5MB", field)
	}

	if err := os.MkdirAll("./uploads", 0o755); err != nil {
		return "", fmt.Errorf("could not create upload dir")
	}

	name := fmt.Sprintf("%s_%d%s", prefix, time.Now().UnixNano(), ext)
	dst := filepath.Join("./uploads", name)
	if err := c.SaveFile(fh, dst); err != nil {
		return "", fmt.Errorf("could not save %s", field)
	}
	return "/uploads/" + name, nil
}
