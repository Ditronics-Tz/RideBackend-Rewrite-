package statuspage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestUptimeBarsKeepAnOutageVisibleWithoutErasingThePercentage(t *testing.T) {
	current := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	monitor := New(t.TempDir(), func() time.Time { return current })
	state := Outage
	monitor.Add(Probe{
		ID: "database", Name: "Database", Group: "Platform", Description: "Primary application database",
		LatencyLabel: "Response time",
		Check: func(context.Context) ProbeResult {
			summary := "Database did not respond"
			if state == Operational {
				summary = "Database responded"
			}
			return ProbeResult{State: state, Summary: summary, Latency: 4 * time.Millisecond}
		},
	})
	monitor.Tick(context.Background())
	state = Operational
	for i := 0; i < 99; i++ {
		monitor.Tick(context.Background())
	}
	report := monitor.Snapshot()
	if len(report.Components) != 1 || len(report.Components[0].Days) != HistoryDays {
		t.Fatalf("expected 90 days, got %#v", report.Components)
	}
	last := report.Components[0].Days[len(report.Components[0].Days)-1]
	if last.Date != "2026-11-09" || last.State != "outage" {
		t.Fatalf("outage day = %#v", last)
	}
	if report.Components[0].Days[0].Date != "2026-08-12" || report.Components[0].Days[0].State != "none" {
		t.Fatalf("window start = %#v", report.Components[0].Days[0])
	}
	if report.Components[0].UptimePercent == nil || *report.Components[0].UptimePercent != 99 {
		t.Fatalf("uptime = %v", report.Components[0].UptimePercent)
	}
	if report.Components[0].State != "operational" || report.Overall != "operational" {
		t.Fatalf("current state = %s overall %s", report.Components[0].State, report.Overall)
	}
	body, err := renderPage(redact(report))
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	if !strings.Contains(page, "Uptime over the past 90 days") || strings.Count(page, `class="bar `) != HistoryDays {
		t.Fatal("status page is missing the 90-day bars")
	}
	if strings.Contains(page, "password") {
		t.Fatal("status page included an unexpected secret marker")
	}
}

func TestHistorySurvivesRestartAndDropsDaysOutsideNinety(t *testing.T) {
	dir := t.TempDir()
	current := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	monitor := New(dir, func() time.Time { return current })
	monitor.Add(Probe{ID: "api", Name: "Rida API", Group: "Platform", Check: func(context.Context) ProbeResult {
		return ProbeResult{State: Outage, Summary: "Many requests are failing"}
	}})
	monitor.Tick(context.Background())
	current = time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	monitor.Tick(context.Background())
	current = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	restored := New(dir, func() time.Time { return current })
	restored.Add(Probe{ID: "api", Name: "Rida API", Group: "Platform", Check: func(context.Context) ProbeResult {
		return ProbeResult{State: Operational, Summary: "Accepting traffic"}
	}})
	loaded := restored.Snapshot()
	var remembered string
	for _, day := range loaded.Components[0].Days {
		if day.Date == "2026-08-11" {
			t.Fatal("day outside the 90-day window was kept")
		}
		if day.Date == "2026-11-08" {
			remembered = day.State
		}
	}
	if remembered != "outage" {
		t.Fatalf("in-window history = %q", remembered)
	}
	restored.Tick(context.Background())
	report := restored.Snapshot()
	if report.Components[0].Days[len(report.Components[0].Days)-1].State != "operational" {
		t.Fatalf("today = %#v", report.Components[0].Days[len(report.Components[0].Days)-1])
	}
}

func TestDatabaseProbeHidesDriverErrors(t *testing.T) {
	probe := databaseProbe(func(context.Context) error {
		return errors.New("pq: password=super-secret host=10.1.2.3")
	})
	result := probe.Check(context.Background())
	if result.State != Outage || result.Summary != "Database did not respond" {
		t.Fatalf("result = %#v", result)
	}
	if strings.Contains(result.Summary, "super-secret") || strings.Contains(result.Summary, "10.1.2.3") {
		t.Fatal("probe leaked the driver error")
	}
}

func TestWorkerHeartbeats(t *testing.T) {
	started := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	stale := 45 * time.Second
	dead := 2 * time.Minute
	cases := []struct {
		name    string
		now     time.Time
		beat    time.Time
		hasBeat bool
		item    note
		want    State
		summary string
	}{
		{"starting", started.Add(10 * time.Second), time.Time{}, false, note{}, Operational, "Worker is starting"},
		{"missing", started.Add(2 * time.Minute), time.Time{}, false, note{}, Degraded, "Worker has not reported"},
		{"gone", started.Add(6 * time.Minute), time.Time{}, false, note{}, Outage, "Worker has not reported"},
		{"fresh", started.Add(time.Hour), started.Add(time.Hour - 5*time.Second), true, note{state: Operational, at: started}, Operational, "Worker heartbeat is current"},
		{"delayed", started.Add(time.Hour), started.Add(time.Hour - time.Minute), true, note{state: Operational}, Degraded, "Worker heartbeat is delayed"},
		{"stale", started.Add(time.Hour), started.Add(time.Hour - 3*time.Minute), true, note{state: Operational}, Outage, "Worker heartbeat is stale"},
		{"failed tick", started.Add(time.Hour), started.Add(time.Hour - 5*time.Second), true, note{state: Degraded, summary: "Ride dispatch tick failed", at: started.Add(time.Hour - 5*time.Second)}, Degraded, "Ride dispatch tick failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := judgeWorker(tc.now, started, tc.beat, tc.hasBeat, tc.item, stale, dead)
			if got.State != tc.want || got.Summary != tc.summary {
				t.Fatalf("got %#v", got)
			}
		})
	}
}

func TestUptimeMath(t *testing.T) {
	pct, ok := uptimePercent(counts{Operational: 50, Degraded: 50})
	if !ok || pct != 75 {
		t.Fatalf("degraded credit = %v %v", pct, ok)
	}
	if _, ok := uptimePercent(counts{Maintenance: 10}); ok {
		t.Fatal("maintenance counted as uptime")
	}
	if worst(counts{Operational: 10, Outage: 1}) != "outage" {
		t.Fatal("worst sample did not color the day")
	}
}

func TestDisabledJobsStayInMaintenance(t *testing.T) {
	probes := DefaultProbes(Deps{Ping: func(context.Context) error { return nil }, Now: time.Now, Started: time.Now})
	var found bool
	for _, probe := range probes {
		if probe.ID == "operations" {
			found = true
			result := probe.Check(context.Background())
			if result.State != Maintenance || strings.Contains(result.Summary, "password") {
				t.Fatalf("operations = %#v", result)
			}
		}
	}
	if !found {
		t.Fatal("operations component missing")
	}
}
