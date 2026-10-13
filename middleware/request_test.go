package middleware_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"ride-backend/middleware"
)

func TestRequestIDAndRedactedLogs(t *testing.T) {
	var logs bytes.Buffer
	app := fiber.New()
	app.Use(middleware.RequestContext(slog.New(slog.NewJSONHandler(&logs, nil))))
	app.Get("/users/:id", func(c *fiber.Ctx) error { return c.Status(403).JSON(fiber.Map{"error": "forbidden"}) })
	for _, id := range []string{"client-request_123", "unsafe request", strings.Repeat("a", 65)} {
		req := httptest.NewRequest("GET", "/users/private-user?token=secret-query", nil)
		req.Header.Set("X-Request-ID", id)
		req.Header.Set("Authorization", "Bearer secret-token")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		actual := resp.Header.Get("X-Request-ID")
		if actual == "" || body["request_id"] != actual {
			t.Fatal("missing correlated ID")
		}
		if id == "client-request_123" && actual != id {
			t.Fatal("safe incoming ID not preserved")
		}
		if id != "client-request_123" && actual == id {
			t.Fatal("unsafe incoming ID preserved")
		}
	}
	for _, sensitive := range []string{"Authorization", "secret-token", "secret-query", "private-user"} {
		if strings.Contains(logs.String(), sensitive) {
			t.Fatal("request log leaked private request data")
		}
	}
	if !strings.Contains(logs.String(), "/users/:id") {
		t.Fatal("route pattern missing")
	}
}
