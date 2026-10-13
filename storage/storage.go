// Package storage keeps public and private objects separate without exposing bucket names to callers.
package storage

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/url"
	"strings"
	"time"
)

type Visibility string

const (
	Public            Visibility = "public"
	Private           Visibility = "private"
	DefaultPresignTTL            = 5 * time.Minute
	MaxPresignTTL                = 15 * time.Minute
)

var (
	ErrInvalidKey         = errors.New("invalid storage object key")
	ErrInvalidVisibility  = errors.New("invalid storage visibility")
	ErrNotFound           = errors.New("storage object not found")
	ErrPresignUnsupported = errors.New("presigned uploads and reads are unavailable for local storage; use the local upload and authenticated file endpoints")
	ErrInvalidTTL         = errors.New("presign duration must be between one second and fifteen minutes")
	ErrInvalidUpload      = errors.New("invalid storage upload")
)

type Upload struct {
	Body        io.Reader
	Size        int64
	ContentType string
}

type ObjectInfo struct {
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	ETag        string `json:"etag,omitempty"`
}

type PresignedURL struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}

type Storage interface {
	Put(context.Context, Visibility, string, Upload) (ObjectInfo, error)
	Get(context.Context, Visibility, string) (io.ReadCloser, ObjectInfo, error)
	Delete(context.Context, Visibility, string) error
	PresignPut(context.Context, Visibility, string, string, time.Duration) (PresignedURL, error)
	PresignGet(context.Context, Visibility, string, time.Duration) (PresignedURL, error)
	Head(context.Context, Visibility, string) (ObjectInfo, error)
}

// ValidateKey accepts portable relative object keys only. Percent encoding is excluded
// so a key cannot acquire traversal semantics when another HTTP layer decodes it.
func ValidateKey(key string) error {
	if key == "" || len(key) > 1024 {
		return ErrInvalidKey
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") {
			return ErrInvalidKey
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return ErrInvalidKey
			}
		}
	}
	return nil
}

func validate(visibility Visibility, key string) error {
	if visibility != Public && visibility != Private {
		return ErrInvalidVisibility
	}
	return ValidateKey(key)
}

func validateTTL(ttl time.Duration) error {
	if ttl < time.Second || ttl > MaxPresignTTL {
		return ErrInvalidTTL
	}
	return nil
}

func validateContentType(contentType string) error {
	if strings.ContainsAny(contentType, "\r\n") {
		return ErrInvalidUpload
	}
	parsed, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.Contains(parsed, "/") {
		return ErrInvalidUpload
	}
	return nil
}

func publicURL(base, key string) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	if base == "" {
		return "", errors.New("public storage base URL is not configured")
	}
	return strings.TrimRight(base, "/") + "/" + key, nil
}

func validateBaseURL(base string, relative bool) error {
	u, err := url.Parse(base)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid public storage base URL")
	}
	if relative && strings.HasPrefix(base, "/") && !strings.HasPrefix(base, "//") {
		return nil
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("invalid public storage base URL")
	}
	return nil
}
