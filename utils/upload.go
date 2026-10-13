package utils

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/image/webp"
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
	input, err := readImageHeader(fh)
	if err != nil {
		return "", fmt.Errorf("field %s: %w", field, err)
	}
	normalized, _, err := NormalizeProfileImage(input)
	if err != nil {
		return "", fmt.Errorf("field %s: %w", field, err)
	}
	return SaveNormalizedPublicImage(normalized, prefix)
}

// ReadProfileImage verifies the bytes and fully decodes/re-encodes them as PNG.
// Re-encoding strips source metadata and limits decoded dimensions before allocation.
func ReadProfileImage(c *fiber.Ctx, field string) ([]byte, error) {
	fh, err := c.FormFile(field)
	if err != nil {
		return nil, fmt.Errorf("field %s: image required", field)
	}
	input, err := readImageHeader(fh)
	if err != nil {
		return nil, fmt.Errorf("field %s: %w", field, err)
	}
	normalized, _, err := NormalizeProfileImage(input)
	if err != nil {
		return nil, fmt.Errorf("field %s: %w", field, err)
	}
	return normalized, nil
}

func readImageHeader(fh *multipart.FileHeader) ([]byte, error) {
	if fh.Size < 1 || fh.Size > 5*1024*1024 {
		return nil, fmt.Errorf("image must contain 1-5242880 bytes")
	}
	file, err := fh.Open()
	if err != nil {
		return nil, fmt.Errorf("unable to read image")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 5*1024*1024+1))
	if err != nil || int64(len(data)) != fh.Size {
		return nil, fmt.Errorf("unable to read image")
	}
	return data, nil
}

func NormalizeProfileImage(data []byte) ([]byte, string, error) {
	if len(data) < 1 || len(data) > 5*1024*1024 {
		return nil, "", fmt.Errorf("image must contain 1-5242880 bytes")
	}
	kind := http.DetectContentType(data)
	var config image.Config
	var err error
	switch kind {
	case "image/jpeg":
		config, err = jpeg.DecodeConfig(bytes.NewReader(data))
	case "image/png":
		config, err = png.DecodeConfig(bytes.NewReader(data))
	case "image/webp":
		config, err = webp.DecodeConfig(bytes.NewReader(data))
	default:
		return nil, "", fmt.Errorf("JPEG, PNG or WebP image required")
	}
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 || int64(config.Width)*int64(config.Height) > 16*1024*1024 {
		return nil, "", fmt.Errorf("invalid or oversized image")
	}
	var decoded image.Image
	switch kind {
	case "image/jpeg":
		decoded, err = jpeg.Decode(bytes.NewReader(data))
	case "image/png":
		decoded, err = png.Decode(bytes.NewReader(data))
	case "image/webp":
		decoded, err = webp.Decode(bytes.NewReader(data))
	}
	if err != nil || decoded == nil {
		return nil, "", fmt.Errorf("invalid or unsupported image")
	}
	var normalized bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&normalized, decoded); err != nil || normalized.Len() > 5*1024*1024 {
		return nil, "", fmt.Errorf("normalized image exceeds 5MB")
	}
	return normalized.Bytes(), "image/png", nil
}

// SaveNormalizedPublicImage stores an already decoded, metadata-free PNG.
func SaveNormalizedPublicImage(data []byte, prefix string) (string, error) {
	if len(data) == 0 || len(data) > 5*1024*1024 || strings.TrimSpace(prefix) == "" {
		return "", fmt.Errorf("invalid public image")
	}
	for _, r := range prefix {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return "", fmt.Errorf("invalid public image prefix")
		}
	}
	if err := os.MkdirAll("./uploads/public", 0o755); err != nil {
		return "", fmt.Errorf("could not create public upload dir")
	}
	name := fmt.Sprintf("%s_%d.png", prefix, time.Now().UnixNano())
	dst := filepath.Join("./uploads/public", name)
	file, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("could not save public image")
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(dst)
		return "", fmt.Errorf("could not save public image")
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
