package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ride-backend/config"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/utils"

	"github.com/gofiber/fiber/v2"
)

type mockStaffStore struct {
	staff map[string]*models.StaffUser
}

func (m *mockStaffStore) GetStaffUserByID(id string) (*models.StaffUser, bool) {
	s, ok := m.staff[id]
	return s, ok
}

func TestAppProtectedMiddleware(t *testing.T) {
	cfg := &config.Config{
		JWTSecret:      "test_app_secret_12345678901234567890",
		StaffJWTSecret: "test_staff_secret_12345678901234567890",
	}

	app := fiber.New()
	app.Get("/app/test", middleware.AppProtected(cfg), func(c *fiber.Ctx) error {
		uid, role := middleware.CurrentUser(c)
		return c.JSON(fiber.Map{"user_id": uid, "role": role})
	})

	t.Run("Valid app token passes", func(t *testing.T) {
		token, _ := utils.GenerateAppToken("user-1", models.RolePassenger, cfg.JWTSecret, 1*time.Hour)
		req := httptest.NewRequest(http.MethodGet, "/app/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200, got %d (err: %v)", resp.StatusCode, err)
		}
	})

	t.Run("Staff token rejected on app route with 401", func(t *testing.T) {
		token, _ := utils.GenerateStaffToken("staff-1", models.StaffRoleAdmin, cfg.StaffJWTSecret, 1*time.Hour)
		req := httptest.NewRequest(http.MethodGet, "/app/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected 401 for staff token on app route, got %d", resp.StatusCode)
		}
	})

	t.Run("Expired token rejected with 401", func(t *testing.T) {
		token, _ := utils.GenerateAppToken("user-1", models.RolePassenger, cfg.JWTSecret, -1*time.Hour)
		req := httptest.NewRequest(http.MethodGet, "/app/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected 401 for expired token, got %d", resp.StatusCode)
		}
	})

	t.Run("Missing token rejected with 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/app/test", nil)
		resp, _ := app.Test(req)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected 401 for missing token, got %d", resp.StatusCode)
		}
	})
}

func TestStaffProtectedMiddleware(t *testing.T) {
	cfg := &config.Config{
		JWTSecret:               "test_app_secret_12345678901234567890",
		StaffJWTSecret:          "test_staff_secret_12345678901234567890",
		StaffRequire2FAForAdmin: true,
	}

	store := &mockStaffStore{
		staff: map[string]*models.StaffUser{
			"active-admin": {
				ID:                 "active-admin",
				Name:               "Admin",
				Email:              "admin@ride.tz",
				Role:               models.StaffRoleAdmin,
				IsActive:           true,
				MustChangePassword: false,
				TOTPEnabled:        true,
			},
			"inactive-staff": {
				ID:                 "inactive-staff",
				Name:               "Inactive",
				Email:              "inactive@ride.tz",
				Role:               models.StaffRoleSupport,
				IsActive:           false,
				MustChangePassword: false,
			},
			"pwd-change-staff": {
				ID:                 "pwd-change-staff",
				Name:               "MustChange",
				Email:              "change@ride.tz",
				Role:               models.StaffRoleSupport,
				IsActive:           true,
				MustChangePassword: true,
			},
		},
	}

	app := fiber.New()
	v1 := app.Group("/api/v1")
	protected := middleware.StaffProtected(cfg, store)

	v1.Get("/staff/dashboard", protected, func(c *fiber.Ctx) error {
		return c.SendString("dashboard")
	})
	v1.Get("/staff/auth/me", protected, func(c *fiber.Ctx) error {
		return c.SendString("me")
	})
	v1.Post("/staff/auth/change-password", protected, func(c *fiber.Ctx) error {
		return c.SendString("password changed")
	})

	t.Run("Active staff passes StaffProtected", func(t *testing.T) {
		token, _ := utils.GenerateStaffToken("active-admin", models.StaffRoleAdmin, cfg.StaffJWTSecret, 1*time.Hour)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/staff/dashboard", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("App token rejected on staff route with 401", func(t *testing.T) {
		token, _ := utils.GenerateAppToken("user-1", models.RoleDriver, cfg.JWTSecret, 1*time.Hour)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/staff/dashboard", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected 401 for app token on staff route, got %d", resp.StatusCode)
		}
	})

	t.Run("Inactive staff rejected with 401", func(t *testing.T) {
		token, _ := utils.GenerateStaffToken("inactive-staff", models.StaffRoleSupport, cfg.StaffJWTSecret, 1*time.Hour)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/staff/dashboard", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected 401 for inactive staff, got %d", resp.StatusCode)
		}
	})

	t.Run("must_change_password blocks regular endpoints with 403", func(t *testing.T) {
		token, _ := utils.GenerateStaffToken("pwd-change-staff", models.StaffRoleSupport, cfg.StaffJWTSecret, 1*time.Hour)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/staff/dashboard", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("Expected 403 for must_change_password on /dashboard, got %d", resp.StatusCode)
		}
	})

	t.Run("must_change_password allows /change-password and /me", func(t *testing.T) {
		token, _ := utils.GenerateStaffToken("pwd-change-staff", models.StaffRoleSupport, cfg.StaffJWTSecret, 1*time.Hour)

		// 1. /me is allowed
		reqMe := httptest.NewRequest(http.MethodGet, "/api/v1/staff/auth/me", nil)
		reqMe.Header.Set("Authorization", "Bearer "+token)
		respMe, err := app.Test(reqMe)
		if err != nil || respMe.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200 for /me under must_change_password, got %d", respMe.StatusCode)
		}

		// 2. /change-password is allowed
		reqPwd := httptest.NewRequest(http.MethodPost, "/api/v1/staff/auth/change-password", nil)
		reqPwd.Header.Set("Authorization", "Bearer "+token)
		respPwd, err := app.Test(reqPwd)
		if err != nil || respPwd.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200 for /change-password under must_change_password, got %d", respPwd.StatusCode)
		}
	})
}

func TestRoleGuardsAnd2FA(t *testing.T) {
	cfg := &config.Config{
		StaffJWTSecret:          "test_staff_secret_12345678901234567890",
		StaffRequire2FAForAdmin: true,
	}

	store := &mockStaffStore{
		staff: map[string]*models.StaffUser{
			"admin-with-2fa": {
				ID:          "admin-with-2fa",
				Role:        models.StaffRoleAdmin,
				IsActive:    true,
				TOTPEnabled: true,
			},
			"admin-no-2fa": {
				ID:          "admin-no-2fa",
				Role:        models.StaffRoleAdmin,
				IsActive:    true,
				TOTPEnabled: false,
			},
			"support-staff": {
				ID:       "support-staff",
				Role:     models.StaffRoleSupport,
				IsActive: true,
			},
		},
	}

	app := fiber.New()
	v1 := app.Group("/api/v1")
	protected := middleware.StaffProtected(cfg, store)

	// Admin-only route
	v1.Get("/admin/sensitive", protected, middleware.RequireStaffRoles(cfg, models.StaffRoleAdmin), func(c *fiber.Ctx) error {
		return c.SendString("admin-ok")
	})

	// Support + Admin route
	v1.Get("/support/work", protected, middleware.RequireStaffRoles(cfg, models.StaffRoleSupport, models.StaffRoleAdmin), func(c *fiber.Ctx) error {
		return c.SendString("support-ok")
	})

	t.Run("Support allowed on support+admin route", func(t *testing.T) {
		token, _ := utils.GenerateStaffToken("support-staff", models.StaffRoleSupport, cfg.StaffJWTSecret, 1*time.Hour)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/support/work", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("Support forbidden on admin-only route", func(t *testing.T) {
		token, _ := utils.GenerateStaffToken("support-staff", models.StaffRoleSupport, cfg.StaffJWTSecret, 1*time.Hour)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/sensitive", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("Expected 403, got %d", resp.StatusCode)
		}
	})

	t.Run("Admin without 2FA blocked on admin-only route when 2FA required", func(t *testing.T) {
		token, _ := utils.GenerateStaffToken("admin-no-2fa", models.StaffRoleAdmin, cfg.StaffJWTSecret, 1*time.Hour)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/sensitive", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("Expected 403 (2FA required), got %d", resp.StatusCode)
		}
	})

	t.Run("Admin with 2FA allowed on admin-only route", func(t *testing.T) {
		token, _ := utils.GenerateStaffToken("admin-with-2fa", models.StaffRoleAdmin, cfg.StaffJWTSecret, 1*time.Hour)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/sensitive", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200, got %d", resp.StatusCode)
		}
	})
}
