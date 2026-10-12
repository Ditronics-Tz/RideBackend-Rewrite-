package middleware_test

import (
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"net/http/httptest"
	"ride-backend/config"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/testutil"
	"ride-backend/utils"
	"testing"
	"time"
)

func TestPendingDeletionAllowsOnlyStatusAndExportPaths(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser("Pending", "pending@example.test", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DB().Model(&models.User{}).Where("id = ?", u.ID).Update("status", "pending_deletion").Error; err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{JWTSecret: "test_app_secret_12345678901234567890"}
	token, err := utils.GenerateAppToken(u.ID, u.Role, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/api/v1/account/deletion-request", middleware.AppProtected(cfg, s), func(c *fiber.Ctx) error { return c.SendStatus(200) })
	app.Get("/api/v1/ratings", middleware.AppProtected(cfg, s), func(c *fiber.Ctx) error { return c.SendStatus(200) })
	request := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, _ := app.Test(req)
		rec := httptest.NewRecorder()
		rec.Code = resp.StatusCode
		return rec
	}
	if got := request("/api/v1/account/deletion-request"); got.Code != 200 {
		t.Fatalf("status route blocked: %d", got.Code)
	}
	req := httptest.NewRequest("GET", "/api/v1/ratings", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ := app.Test(req)
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403 for normal endpoint, got %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["code"] != "ACCOUNT_PENDING_DELETION" {
		t.Fatalf("missing pending-deletion error code: %#v", body)
	}
}
