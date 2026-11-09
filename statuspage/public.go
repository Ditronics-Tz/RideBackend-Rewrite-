package statuspage

// Public component ids are the only rows an anonymous visitor can see.
// Internal workers still affect the overall banner when they are actually
// down, without naming the worker or explaining why.
var publicRows = []struct{ id, name, group string }{
	{"api", "Rida API", "Platform"},
	{"database", "Database", "Platform"},
	{"notifications", "Notifications", "Platform"},
	{WorkerDispatch, "Ride matching", "Operations"},
}

// PublicReport is the anonymous status document. It has no process uptime,
// version, probe identifiers, latency, or check log.
type PublicReport struct {
	GeneratedAt string            `json:"generated_at"`
	Overall     string            `json:"overall"`
	Summary     string            `json:"summary"`
	RangeDays   int               `json:"range_days"`
	Components  []PublicComponent `json:"components"`
}

// PublicComponent is one customer-facing service row.
type PublicComponent struct {
	Name          string    `json:"name"`
	Group         string    `json:"group"`
	State         string    `json:"state"`
	Summary       string    `json:"summary"`
	UptimePercent *float64  `json:"uptime_percent"`
	Days          []DayView `json:"days"`
}

// PublicSnapshot is safe to serve without authentication.
func (m *Monitor) PublicSnapshot() PublicReport {
	return redact(m.Snapshot())
}

func redact(full Report) PublicReport {
	byID := map[string]ComponentView{}
	for _, component := range full.Components {
		byID[component.ID] = component
	}
	out := PublicReport{
		GeneratedAt: full.GeneratedAt,
		Overall:     "operational",
		Summary:     "All systems operational",
		RangeDays:   full.RangeDays,
		Components:  []PublicComponent{},
	}
	var states []State
	for _, row := range publicRows {
		source, ok := byID[row.id]
		if !ok {
			continue
		}
		state := source.State
		if state == "" || state == string(Maintenance) {
			state = "none"
		}
		out.Components = append(out.Components, PublicComponent{
			Name: row.name, Group: row.group, State: state,
			Summary: publicSummary(state), UptimePercent: source.UptimePercent, Days: source.Days,
		})
		if state == string(Operational) || state == string(Degraded) || state == string(Outage) {
			states = append(states, State(state))
		}
	}
	for _, component := range full.Components {
		if isPublicRow(component.ID) {
			continue
		}
		if component.State == string(Degraded) || component.State == string(Outage) {
			states = append(states, State(component.State))
		}
	}
	if len(states) > 0 {
		out.Overall, out.Summary = banner(states)
	}
	return out
}

func isPublicRow(id string) bool {
	for _, row := range publicRows {
		if row.id == id {
			return true
		}
	}
	return false
}

func publicSummary(state string) string {
	switch state {
	case string(Degraded):
		return "Performance is reduced"
	case string(Outage):
		return "Unavailable"
	case "none":
		return "No data yet"
	default:
		return "Operating normally"
	}
}
