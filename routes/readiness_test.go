package routes_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"ride-backend/routes"
	"ride-backend/testutil"
)

func TestReadinessChecksDatabaseButHealthStaysCheap(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	check := func(path string, status int) {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != status {
			t.Fatalf("%s got %d", path, resp.StatusCode)
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if status >= 400 && body["request_id"] == nil {
			t.Fatal("failure missing request ID")
		}
	}
	check("/ready", 200)
	check("/api/v1/ready", 200)
	sqlDB, err := s.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	check("/ready", 503)
	check("/api/v1/ready", 503)
	check("/health", 200)
	check("/api/v1/health", 200)
}
