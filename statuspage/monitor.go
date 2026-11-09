// Package statuspage is a small status module inside the API process.
// It samples operational health, keeps ninety days of uptime on disk, and
// serves a public page that does not depend on the admin application.
package statuspage

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const HistoryDays = 90

var eat = time.FixedZone("EAT", 3*60*60)

type counts struct {
	Operational int `json:"operational"`
	Degraded    int `json:"degraded"`
	Outage      int `json:"outage"`
	Maintenance int `json:"maintenance"`
}

type sample struct {
	At    time.Time
	MS    int
	State State
}

type storedLog struct {
	At          time.Time `json:"at"`
	ComponentID string    `json:"component_id"`
	Component   string    `json:"component"`
	State       string    `json:"state"`
	Summary     string    `json:"summary"`
	LatencyMS   int       `json:"latency_ms"`
}

type logMark struct {
	at    time.Time
	state State
}

type fileState struct {
	Days map[string]map[string]counts `json:"days"`
	Logs []storedLog                  `json:"logs"`
}

// DayView is one bar in the 90-day history. State is the worst sample that day.
type DayView struct {
	Date  string `json:"date"`
	State string `json:"state"`
}

// LatencyPoint is one bucket in the last 24 hours.
type LatencyPoint struct {
	At string `json:"at"`
	MS int    `json:"ms"`
}

// LogView is one recent check, newest first in a report.
type LogView struct {
	At          string `json:"at"`
	ComponentID string `json:"component_id"`
	Component   string `json:"component"`
	State       string `json:"state"`
	Summary     string `json:"summary"`
	LatencyMS   int    `json:"latency_ms"`
}

// ComponentView is one service row.
type ComponentView struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Group         string         `json:"group"`
	State         string         `json:"state"`
	Summary       string         `json:"summary"`
	UptimePercent *float64       `json:"uptime_percent"`
	LatencyMS     int            `json:"latency_ms"`
	LatencyLabel  string         `json:"latency_label"`
	Days          []DayView      `json:"days"`
	Latency       []LatencyPoint `json:"latency"`
}

// Report is the public status snapshot.
type Report struct {
	GeneratedAt   string          `json:"generated_at"`
	Overall       string          `json:"overall"`
	Summary       string          `json:"summary"`
	Version       string          `json:"version"`
	UptimeSeconds int64           `json:"uptime_seconds"`
	RangeDays     int             `json:"range_days"`
	Components    []ComponentView `json:"components"`
	Logs          []LogView       `json:"logs"`
}

// Monitor samples probes and stores daily uptime.
type Monitor struct {
	mu         sync.Mutex
	now        func() time.Time
	dir        string
	version    string
	started    time.Time
	probes     []Probe
	days       map[string]map[string]*counts
	current    map[string]ProbeResult
	recent     map[string][]sample
	logs       []storedLog
	lastLog    map[string]logMark
	saveWarned bool
}

type outcome struct {
	probe  Probe
	result ProbeResult
}

var active atomic.Pointer[Monitor]

// New starts an idle monitor. An empty dir keeps history in memory only.
func New(dir string, now func() time.Time) *Monitor {
	if now == nil {
		now = time.Now
	}
	m := &Monitor{
		now:     now,
		version: "1.0.0",
		started: now(),
		days:    map[string]map[string]*counts{},
		current: map[string]ProbeResult{},
		recent:  map[string][]sample{},
		lastLog: map[string]logMark{},
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			slog.Warn("status history directory is not writable")
		} else {
			m.dir = dir
			m.load()
		}
	}
	return m
}

// Active is the monitor attached to this process, if BuildApp has run.
func Active() *Monitor { return active.Load() }

// Run samples the active monitor until ctx is cancelled.
func Run(ctx context.Context, every time.Duration) {
	m := active.Load()
	if m == nil || every <= 0 {
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Tick(ctx)
		}
	}
}

func (m *Monitor) setVersion(version string) {
	m.mu.Lock()
	m.version = version
	m.mu.Unlock()
}

func (m *Monitor) clock() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now()
}

func (m *Monitor) startedAt() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started
}

// Add registers a component. Later samples use registration order.
func (m *Monitor) Add(probe Probe) {
	m.mu.Lock()
	m.probes = append(m.probes, probe)
	m.mu.Unlock()
}

// Tick checks every component once.
func (m *Monitor) Tick(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	probes := append([]Probe(nil), m.probes...)
	m.mu.Unlock()
	results := make([]outcome, len(probes))
	var group sync.WaitGroup
	for i, probe := range probes {
		group.Add(1)
		go func(i int, probe Probe) {
			defer group.Done()
			defer func() {
				if recover() != nil {
					results[i] = outcome{probe: probe, result: ProbeResult{State: Outage, Summary: "Check failed"}}
				}
			}()
			checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			start := time.Now()
			result := ProbeResult{State: Outage, Summary: "Check failed"}
			if probe.Check != nil {
				result = probe.Check(checkCtx)
			}
			if result.Latency <= 0 {
				result.Latency = time.Since(start)
			}
			result.Summary = clean(result.Summary)
			switch result.State {
			case Operational, Degraded, Outage, Maintenance:
			default:
				result.State = Outage
				if result.Summary == "" {
					result.Summary = "Check failed"
				}
			}
			results[i] = outcome{probe: probe, result: result}
		}(i, probe)
	}
	group.Wait()
	m.record(results)
}

func (m *Monitor) record(results []outcome) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	day := dayKey(now)
	for _, item := range results {
		id := item.probe.ID
		bucket := m.days[id]
		if bucket == nil {
			bucket = map[string]*counts{}
			m.days[id] = bucket
		}
		count := bucket[day]
		if count == nil {
			count = &counts{}
			bucket[day] = count
		}
		switch item.result.State {
		case Degraded:
			count.Degraded++
		case Outage:
			count.Outage++
		case Maintenance:
			count.Maintenance++
		default:
			item.result.State = Operational
			count.Operational++
		}
		m.current[id] = item.result
		ms := int(item.result.Latency / time.Millisecond)
		if ms < 0 {
			ms = 0
		}
		kept := make([]sample, 0, len(m.recent[id])+1)
		cutoff := now.Add(-26 * time.Hour)
		for _, seen := range m.recent[id] {
			if !seen.At.Before(cutoff) {
				kept = append(kept, seen)
			}
		}
		kept = append(kept, sample{At: now, MS: ms, State: item.result.State})
		if len(kept) > 3000 {
			kept = kept[len(kept)-3000:]
		}
		m.recent[id] = kept
		prev := m.lastLog[id]
		changed := prev.at.IsZero() || prev.state != item.result.State
		repeat := item.result.State == Operational && !prev.at.IsZero() && now.Sub(prev.at) >= 15*time.Minute
		trouble := item.result.State != Operational && item.result.State != Maintenance && !prev.at.IsZero() && now.Sub(prev.at) >= time.Minute
		if changed || repeat || trouble {
			m.logs = append(m.logs, storedLog{
				At: now, ComponentID: id, Component: item.probe.Name,
				State: string(item.result.State), Summary: item.result.Summary, LatencyMS: ms,
			})
			m.lastLog[id] = logMark{at: now, state: item.result.State}
		}
	}
	if len(m.logs) > 80 {
		m.logs = m.logs[len(m.logs)-80:]
	}
	m.prune(now)
	m.save()
}

// Snapshot is the public report. It never includes probe errors or secrets.
func (m *Monitor) Snapshot() Report {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	dates := dateList(now)
	uptime := int64(now.Sub(m.started).Seconds())
	if uptime < 0 {
		uptime = 0
	}
	report := Report{
		GeneratedAt: now.UTC().Format(time.RFC3339), Overall: "operational", Summary: "Collecting status",
		Version: m.version, UptimeSeconds: uptime, RangeDays: HistoryDays,
		Components: []ComponentView{}, Logs: []LogView{},
	}
	var states []State
	for _, probe := range m.probes {
		current, seen := m.current[probe.ID]
		state := "none"
		summary := "No sample yet"
		if seen {
			state = string(current.State)
			summary = current.Summary
			states = append(states, current.State)
		}
		days := make([]DayView, len(dates))
		var total counts
		for i, date := range dates {
			var count counts
			if bucket := m.days[probe.ID]; bucket != nil && bucket[date] != nil {
				count = *bucket[date]
			}
			days[i] = DayView{Date: date, State: worst(count)}
			total.Operational += count.Operational
			total.Degraded += count.Degraded
			total.Outage += count.Outage
			total.Maintenance += count.Maintenance
		}
		view := ComponentView{
			ID: probe.ID, Name: probe.Name, Description: probe.Description, Group: probe.Group,
			State: state, Summary: summary, LatencyMS: int(current.Latency / time.Millisecond),
			LatencyLabel: probe.LatencyLabel, Days: days, Latency: latencySeries(m.recent[probe.ID], now),
		}
		if view.LatencyMS < 0 {
			view.LatencyMS = 0
		}
		if pct, ok := uptimePercent(total); ok {
			rounded := math.Round(pct*100) / 100
			view.UptimePercent = &rounded
		}
		report.Components = append(report.Components, view)
	}
	if len(states) > 0 {
		report.Overall, report.Summary = banner(states)
	}
	for i := len(m.logs) - 1; i >= 0; i-- {
		item := m.logs[i]
		report.Logs = append(report.Logs, LogView{
			At: item.At.UTC().Format(time.RFC3339), ComponentID: item.ComponentID, Component: item.Component,
			State: item.State, Summary: item.Summary, LatencyMS: item.LatencyMS,
		})
	}
	return report
}

func (m *Monitor) prune(now time.Time) {
	valid := map[string]bool{}
	for _, date := range dateList(now) {
		valid[date] = true
	}
	for id, days := range m.days {
		for date := range days {
			if !valid[date] {
				delete(days, date)
			}
		}
		if len(days) == 0 {
			delete(m.days, id)
		}
	}
	cutoff := now.Add(-7 * 24 * time.Hour)
	kept := m.logs[:0]
	for _, item := range m.logs {
		if !item.At.Before(cutoff) {
			kept = append(kept, item)
		}
	}
	m.logs = kept
}

func (m *Monitor) load() {
	body, err := os.ReadFile(m.historyPath())
	if err != nil {
		return
	}
	var state fileState
	if err := json.Unmarshal(body, &state); err != nil {
		slog.Warn("status history file was ignored")
		return
	}
	for id, days := range state.Days {
		bucket := map[string]*counts{}
		for date, count := range days {
			copied := count
			bucket[date] = &copied
		}
		m.days[id] = bucket
	}
	m.logs = state.Logs
	for _, item := range m.logs {
		m.lastLog[item.ComponentID] = logMark{at: item.At, state: State(item.State)}
	}
	m.prune(m.now())
}

func (m *Monitor) save() {
	if m.dir == "" {
		return
	}
	state := fileState{Days: map[string]map[string]counts{}, Logs: m.logs}
	for id, days := range m.days {
		bucket := map[string]counts{}
		for date, count := range days {
			if count != nil {
				bucket[date] = *count
			}
		}
		state.Days[id] = bucket
	}
	body, err := json.Marshal(state)
	if err != nil {
		return
	}
	tmp := m.historyPath() + ".tmp"
	if err := os.WriteFile(tmp, body, 0o640); err != nil {
		m.warnSave()
		return
	}
	if err := os.Rename(tmp, m.historyPath()); err != nil {
		m.warnSave()
	}
}

func (m *Monitor) warnSave() {
	if m.saveWarned {
		return
	}
	m.saveWarned = true
	slog.Warn("status history was not saved")
}

func (m *Monitor) historyPath() string {
	return filepath.Join(m.dir, "history.json")
}

func dayKey(now time.Time) string {
	return now.In(eat).Format("2006-01-02")
}

func dateList(now time.Time) []string {
	today := now.In(eat)
	start := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, eat).AddDate(0, 0, -(HistoryDays - 1))
	dates := make([]string, HistoryDays)
	for i := 0; i < HistoryDays; i++ {
		dates[i] = start.AddDate(0, 0, i).Format("2006-01-02")
	}
	return dates
}

func worst(count counts) string {
	switch {
	case count.Outage > 0:
		return "outage"
	case count.Degraded > 0:
		return "degraded"
	case count.Operational > 0:
		return "operational"
	case count.Maintenance > 0:
		return "maintenance"
	default:
		return "none"
	}
}

func uptimePercent(count counts) (float64, bool) {
	total := count.Operational + count.Degraded + count.Outage
	if total == 0 {
		return 0, false
	}
	return (float64(count.Operational) + 0.5*float64(count.Degraded)) / float64(total) * 100, true
}

func latencySeries(samples []sample, now time.Time) []LatencyPoint {
	from := now.Add(-24 * time.Hour)
	type acc struct{ n, sum int }
	buckets := make([]acc, 48)
	for _, item := range samples {
		if item.At.Before(from) || item.At.After(now.Add(time.Second)) {
			continue
		}
		index := int(item.At.Sub(from) / (30 * time.Minute))
		if index < 0 {
			index = 0
		}
		if index > 47 {
			index = 47
		}
		buckets[index].n++
		buckets[index].sum += item.MS
	}
	points := []LatencyPoint{}
	for i, bucket := range buckets {
		if bucket.n == 0 {
			continue
		}
		points = append(points, LatencyPoint{
			At: from.Add(time.Duration(i) * 30 * time.Minute).UTC().Format(time.RFC3339),
			MS: bucket.sum / bucket.n,
		})
	}
	return points
}

func banner(states []State) (string, string) {
	outage, degraded := false, false
	for _, state := range states {
		switch state {
		case Outage:
			outage = true
		case Degraded:
			degraded = true
		}
	}
	switch {
	case outage:
		return "outage", "Service disruption"
	case degraded:
		return "degraded", "Some systems are degraded"
	default:
		return "operational", "All systems operational"
	}
}
