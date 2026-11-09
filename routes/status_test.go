package routes_test

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"ride-backend/routes"
	"ride-backend/testutil"
)

func TestPublicStatusPageAndSnapshot(t *testing.T) {
	appStore, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, appStore)
	pageResp, err := app.Test(httptest.NewRequest("GET", "/status", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer pageResp.Body.Close()
	if pageResp.StatusCode != 200 || !strings.Contains(pageResp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("page status %d type %s", pageResp.StatusCode, pageResp.Header.Get("Content-Type"))
	}
	page, err := io.ReadAll(pageResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, phrase := range []string{"Uptime over the past 90 days", "Rida API", "All systems operational", "Recent checks"} {
		if !strings.Contains(html, phrase) {
			t.Fatalf("page missing %q", phrase)
		}
	}
	if strings.Contains(html, "super-secret") || strings.Contains(html, "password=") {
		t.Fatal("public page leaked a secret")
	}

	jsonResp, err := app.Test(httptest.NewRequest("GET", "/api/v1/status", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer jsonResp.Body.Close()
	if jsonResp.StatusCode != 200 {
		t.Fatalf("json status %d", jsonResp.StatusCode)
	}
	var report struct {
		Overall    string `json:"overall"`
		Components []struct {
			ID   string `json:"id"`
			Days []struct {
				State string `json:"state"`
			} `json:"days"`
			Summary string `json:"summary"`
		} `json:"components"`
		Logs []any `json:"logs"`
	}
	if err := json.NewDecoder(jsonResp.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if report.Overall != "operational" || len(report.Components) < 5 {
		t.Fatalf("report = %#v", report)
	}
	for _, component := range report.Components {
		if len(component.Days) != 90 {
			t.Fatalf("%s days = %d", component.ID, len(component.Days))
		}
		if strings.Contains(component.Summary, "password") {
			t.Fatal("summary leaked a secret")
		}
	}
}
