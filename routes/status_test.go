package routes_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
)

func TestPublicStatusHidesInternalDetail(t *testing.T) {
	appStore, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, appStore)
	pageResp, err := app.Test(httptest.NewRequest(http.MethodGet, "/status", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer pageResp.Body.Close()
	if pageResp.StatusCode != 200 || !strings.Contains(pageResp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("page status %d type %s", pageResp.StatusCode, pageResp.Header.Get("Content-Type"))
	}
	if pageResp.Header.Get("X-Frame-Options") != "DENY" || !strings.Contains(pageResp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("public status page is missing frame protection")
	}
	page, err := io.ReadAll(pageResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	if !strings.Contains(html, "Uptime over the past 90 days") || !strings.Contains(html, "Ride matching") {
		t.Fatal("public page is missing the customer-facing status")
	}
	assertNoInternalDetail(t, html)

	jsonResp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer jsonResp.Body.Close()
	body, err := io.ReadAll(jsonResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if jsonResp.StatusCode != 200 {
		t.Fatalf("json status %d", jsonResp.StatusCode)
	}
	assertNoInternalDetail(t, string(body))
	var report struct {
		Overall    string `json:"overall"`
		Components []struct {
			Name string `json:"name"`
			Days []struct {
				State string `json:"state"`
			} `json:"days"`
		} `json:"components"`
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.Overall != "operational" || len(report.Components) != 4 {
		t.Fatalf("public report = %#v", report)
	}
	for _, component := range report.Components {
		if len(component.Days) != 90 {
			t.Fatalf("%s days = %d", component.Name, len(component.Days))
		}
	}
}

func TestStaffStatusRequiresAdmin(t *testing.T) {
	appStore, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, appStore)
	open, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/admin/status", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer open.Body.Close()
	if open.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status %d", open.StatusCode)
	}

	support, err := appStore.CreateStaffUser("Support", uuid.NewString()+"@example.test", "unused", models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	supportToken, err := testutil.StaffToken(appStore, support.ID, models.StaffRoleSupport, cfg.StaffJWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	supportReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/status", nil)
	supportReq.Header.Set("Authorization", "Bearer "+supportToken)
	supportResp, err := app.Test(supportReq)
	if err != nil {
		t.Fatal(err)
	}
	defer supportResp.Body.Close()
	if supportResp.StatusCode != http.StatusForbidden {
		t.Fatalf("support status %d", supportResp.StatusCode)
	}

	admin, err := appStore.CreateStaffUser("Admin", uuid.NewString()+"@example.test", "unused", models.StaffRoleAdmin, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	token, err := testutil.StaffToken(appStore, admin.ID, models.StaffRoleAdmin, cfg.StaffJWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "background jobs") {
		t.Fatalf("admin status %d %s", resp.StatusCode, body)
	}
}

func assertNoInternalDetail(t *testing.T, body string) {
	t.Helper()
	lower := strings.ToLower(body)
	for _, phrase := range []string{"push delivery", "background jobs", "heartbeat", "data-requests", "document-expiry", "ride-dispatch", "latency_ms", "uptime_seconds", "password"} {
		if strings.Contains(lower, phrase) {
			t.Fatalf("status response contains %q", phrase)
		}
	}
}
