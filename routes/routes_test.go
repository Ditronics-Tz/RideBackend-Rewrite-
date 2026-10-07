package routes_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ride-backend/routes"
	"ride-backend/testutil"
)

func TestAllRoutesPrefixAndJSON404(t *testing.T) {
	appStore, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatalf("Failed to setup test store: %v", err)
	}

	app := routes.BuildApp(cfg, appStore)

	t.Run("Every route in app.GetRoutes() starts with /api/v1", func(t *testing.T) {
		allRoutes := app.GetRoutes()
		if len(allRoutes) == 0 {
			t.Fatal("Expected registered routes, got 0")
		}

		for _, r := range allRoutes {
			if !strings.HasPrefix(r.Path, "/api/v1") {
				t.Fatalf("Route %s %s does NOT start with /api/v1", r.Method, r.Path)
			}
		}
	})

	t.Run("Health check returns 200 with status ok and version", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		resp, err := app.Test(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200 on /api/v1/health, got %d", resp.StatusCode)
		}

		var body map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		if body["status"] != "ok" || body["version"] == nil {
			t.Fatalf("Expected status ok and version, got %v", body)
		}
	})

	t.Run("Unknown paths return a JSON 404", func(t *testing.T) {
		unknownPaths := []string{
			"/",
			"/health",
			"/unknown",
			"/api/v2/anything",
			"/api/v1/nonexistent/endpoint",
		}

		for _, p := range unknownPaths {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("Test request failed for %s: %v", p, err)
			}
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("Expected 404 for %s, got %d", p, resp.StatusCode)
			}

			b, _ := io.ReadAll(resp.Body)
			var data map[string]interface{}
			if err := json.Unmarshal(b, &data); err != nil {
				t.Fatalf("Expected valid JSON 404 for %s, got %v (body: %s)", p, err, string(b))
			}
			if data["error"] == nil {
				t.Fatalf("Expected error field in JSON response for %s, got %v", p, data)
			}
		}
	})
}
