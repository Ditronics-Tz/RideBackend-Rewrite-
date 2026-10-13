package routes_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ride-backend/routes"
	"ride-backend/testutil"
)

func TestUnrestrictedCORSPreflight(t *testing.T) {
	appStore, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	// An unset environment setting must never leave OPTIONS requests unhandled.
	cfg.CORSAllowedOrigins = ""
	app := routes.BuildApp(cfg, appStore)
	defer app.Shutdown()

	for _, endpoint := range []struct {
		path   string
		method string
	}{
		{"/api/v1/staff/auth/login", http.MethodPost},
		{"/api/v1/admin/staff", http.MethodGet},
		{"/api/v1/admin/users/user-id/role", http.MethodPatch},
	} {
		t.Run(endpoint.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, endpoint.path, nil)
			req.Header.Set("Origin", "http://localhost:5173")
			req.Header.Set("Access-Control-Request-Method", endpoint.method)
			req.Header.Set("Access-Control-Request-Headers", "authorization,content-type,x-device-id,x-device-platform")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("Preflight should succeed without authentication, got %d", resp.StatusCode)
			}
			if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
				t.Fatal("Expected unrestricted CORS origin")
			}
			if !strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), endpoint.method) {
				t.Fatalf("Requested method %s is not allowed", endpoint.method)
			}
			for _, header := range []string{"authorization", "content-type", "x-device-id", "x-device-platform"} {
				if !strings.Contains(strings.ToLower(resp.Header.Get("Access-Control-Allow-Headers")), header) {
					t.Fatalf("Requested header %s is not allowed", header)
				}
			}
		})
	}
}
