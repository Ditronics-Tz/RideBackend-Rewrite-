package routes_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
)

func TestDevicePlatformHeaders(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DeviceHashPepper = "test-device-pepper"
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	hash, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser("Passenger", uuid.NewString()+"@test.invalid", string(hash), models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	token, err := testutil.AppToken(s, u.ID, u.Role, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, path, method, header, platform string }{
		{"login documented header", "/api/v1/auth/login", http.MethodPost, "X-Device-Platform", "android"},
		{"authenticated documented header", "/api/v1/reports", http.MethodGet, "X-Device-Platform", "ios"},
		{"legacy header", "/api/v1/reports", http.MethodGet, "X-Platform", "web"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(fmt.Sprintf(`{"email":%q,"password":"test-password"}`, u.Email)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-Device-ID", tc.name)
			req.Header.Set(tc.header, tc.platform)
			resp, err := app.Test(req, 10000)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d", resp.StatusCode)
			}
			var device models.UserDevice
			if err := s.DB().Where("user_id = ? AND platform = ?", u.ID, tc.platform).First(&device).Error; err != nil {
				t.Fatalf("device platform %q was not recorded: %v", tc.platform, err)
			}
		})
	}
}
