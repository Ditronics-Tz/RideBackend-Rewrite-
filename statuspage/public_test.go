package statuspage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublicSnapshotOmitsInternalDetail(t *testing.T) {
	full := Report{
		GeneratedAt: "2026-11-09T10:00:00Z", Version: "1.0.0", UptimeSeconds: 400,
		RangeDays: HistoryDays, Overall: "degraded", Summary: "Some systems are degraded",
		Components: []ComponentView{
			{ID: "api", Name: "Rida API", State: "operational", Summary: "Accepting traffic", Days: []DayView{{Date: "2026-11-09", State: "operational"}}},
			{ID: "notifications", Name: "Notifications", State: "operational", Summary: "In-app notifications are served with the API. Push delivery is off."},
			{ID: "operations", Name: "Payments, safety and fraud", State: "maintenance", Summary: "This process is not running background jobs"},
			{ID: "data-requests", Name: "Data requests", State: "outage", Summary: "Worker heartbeat is stale"},
			{ID: WorkerDispatch, Name: "Ride dispatch", State: "degraded", Summary: "Ride dispatch tick failed"},
		},
		Logs: []LogView{{ComponentID: "data-requests", Summary: "Worker heartbeat is stale", LatencyMS: 12}},
	}
	public := redact(full)
	encoded := strings.ToLower(mustJSON(t, public))
	for _, secret := range []string{"push delivery", "background jobs", "heartbeat", "data-requests", "ride-dispatch", "latency_ms", "uptime_seconds", "1.0.0", "tick failed"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("public status contains %q", secret)
		}
	}
	if public.Overall != "outage" || public.Summary != "Service disruption" {
		t.Fatalf("hidden outage did not affect the banner: %#v", public)
	}
	names := map[string]string{}
	for _, component := range public.Components {
		names[component.Name] = component.Summary
	}
	if names["Notifications"] != "Operating normally" || names["Ride matching"] != "Performance is reduced" {
		t.Fatalf("public rows = %#v", public.Components)
	}
	if _, leaked := names["Data requests"]; leaked {
		t.Fatal("internal component was published")
	}
	if _, leaked := names["Payments, safety and fraud"]; leaked {
		t.Fatal("internal component was published")
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
