package middleware

import (
	"encoding/json"
	"log/slog"
	"regexp"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

var safeRequestID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func RequestID(c *fiber.Ctx) string { id, _ := c.Locals("request_id").(string); return id }

// RequestContext logs route patterns and status, never bodies, headers, query
// strings, tokens or concrete personal identifiers.
func RequestContext(log *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Get("X-Request-ID")
		if !safeRequestID.MatchString(id) {
			id = uuid.NewString()
		}
		c.Locals("request_id", id)
		c.Set("X-Request-ID", id)
		start := time.Now()
		err := c.Next()
		if err != nil {
			err = c.App().Config().ErrorHandler(c, err)
		}
		if c.Response().StatusCode() >= 400 {
			var body map[string]any
			if json.Unmarshal(c.Response().Body(), &body) == nil && body != nil {
				body["request_id"] = id
				if raw, marshalErr := json.Marshal(body); marshalErr == nil {
					c.Response().SetBody(raw)
				}
			}
		}
		if log != nil {
			log.Info("http_request", "request_id", id, "method", c.Method(), "route", c.Route().Path, "status", c.Response().StatusCode(), "duration_ms", time.Since(start).Milliseconds())
		}
		return err
	}
}
