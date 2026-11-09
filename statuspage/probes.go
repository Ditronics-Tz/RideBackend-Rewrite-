package statuspage

import (
	"context"
	"time"
)

// State is the public condition of one component.
type State string

const (
	Operational State = "operational"
	Degraded    State = "degraded"
	Outage      State = "outage"
	Maintenance State = "maintenance"
)

// ProbeResult is one check. Summary is a fixed phrase with no connection
// details, hosts, or request data.
type ProbeResult struct {
	State   State
	Summary string
	Latency time.Duration
}

// Probe is one row on the status page.
type Probe struct {
	ID           string
	Name         string
	Description  string
	Group        string
	LatencyLabel string
	Check        func(context.Context) ProbeResult
}

// Deps are the only hooks the status module needs from the rest of the process.
type Deps struct {
	Ping           func(context.Context) error
	PushEnabled    bool
	BackgroundJobs bool
	Now            func() time.Time
	Started        func() time.Time
}

// DefaultProbes are the operational components this process can actually see.
func DefaultProbes(deps Deps) []Probe {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	started := deps.Started
	if started == nil {
		started = func() time.Time { return now().Add(-time.Minute) }
	}
	probes := []Probe{
		apiProbe(),
		databaseProbe(deps.Ping),
		notificationsProbe(deps.PushEnabled, now, started),
		workerProbe(workerSpec{
			id: WorkerDispatch, name: "Ride dispatch", group: "Operations",
			description: "Offer expiry, scheduled rides and shared-trip catch-up",
			stale:       45 * time.Second, dead: 2 * time.Minute,
		}, now, started),
		workerProbe(workerSpec{
			id: WorkerDocuments, name: "Document checks", group: "Operations",
			description: "Daily driver document expiry sweep",
			stale:       26 * time.Hour, dead: 50 * time.Hour,
		}, now, started),
	}
	if deps.BackgroundJobs {
		probes = append(probes,
			workerProbe(workerSpec{
				id: WorkerOperations, name: "Payments, safety and fraud", group: "Operations",
				description: "Payment, safety, fraud and reconciliation ticks",
				stale:       2 * time.Minute, dead: 5 * time.Minute,
			}, now, started),
			workerProbe(workerSpec{
				id: WorkerPrivacy, name: "Data requests", group: "Operations",
				description: "Account export and deletion processing",
				stale:       12 * time.Minute, dead: 20 * time.Minute,
			}, now, started),
		)
	} else {
		probes = append(probes,
			idleProbe("operations", "Payments, safety and fraud", "Payment, safety, fraud and reconciliation ticks"),
			idleProbe("data-requests", "Data requests", "Account export and deletion processing"),
		)
	}
	return probes
}

func apiProbe() Probe {
	return Probe{
		ID: "api", Name: "Rida API", Group: "Platform", LatencyLabel: "Response time",
		Description: "Ride, account and admin HTTP API",
		Check: func(context.Context) ProbeResult {
			total, failed, avg := requestWindow(5 * time.Minute)
			state := Operational
			summary := "Accepting traffic"
			if total >= 20 {
				ratio := float64(failed) / float64(total)
				switch {
				case ratio >= 0.2:
					state = Outage
					summary = "Many requests are failing"
				case ratio >= 0.05:
					state = Degraded
					summary = "Some requests are failing"
				}
			}
			return ProbeResult{State: state, Summary: summary, Latency: avg}
		},
	}
}

func databaseProbe(ping func(context.Context) error) Probe {
	return Probe{
		ID: "database", Name: "Database", Group: "Platform", LatencyLabel: "Response time",
		Description: "Primary application database",
		Check: func(ctx context.Context) ProbeResult {
			if ping == nil {
				return ProbeResult{State: Outage, Summary: "Database did not respond"}
			}
			start := time.Now()
			err := ping(ctx)
			elapsed := time.Since(start)
			if err != nil || ctx.Err() != nil {
				return ProbeResult{State: Outage, Summary: "Database did not respond", Latency: elapsed}
			}
			if elapsed > 800*time.Millisecond {
				return ProbeResult{State: Degraded, Summary: "Database responses are slow", Latency: elapsed}
			}
			return ProbeResult{State: Operational, Summary: "Database responded", Latency: elapsed}
		},
	}
}

func notificationsProbe(push bool, now func() time.Time, started func() time.Time) Probe {
	if !push {
		return Probe{
			ID: "notifications", Name: "Notifications", Group: "Platform",
			Description: "In-app inbox and optional push delivery",
			Check: func(context.Context) ProbeResult {
				return ProbeResult{State: Operational, Summary: "In-app notifications are served with the API. Push delivery is off."}
			},
		}
	}
	return workerProbe(workerSpec{
		id: WorkerPush, name: "Notifications", group: "Platform",
		description: "In-app inbox and push delivery",
		stale:       60 * time.Second, dead: 3 * time.Minute,
	}, now, started)
}

type workerSpec struct {
	id, name, group, description string
	stale, dead                  time.Duration
}

func workerProbe(spec workerSpec, now func() time.Time, started func() time.Time) Probe {
	return Probe{
		ID: spec.id, Name: spec.name, Group: spec.group, Description: spec.description,
		LatencyLabel: "Heartbeat age",
		Check: func(context.Context) ProbeResult {
			beat, ok := lastBeat(spec.id)
			item, noted := lastNote(spec.id)
			if !noted {
				item = note{state: Operational}
			}
			return judgeWorker(now(), started(), beat, ok, item, spec.stale, spec.dead)
		},
	}
}

func idleProbe(id, name, description string) Probe {
	return Probe{
		ID: id, Name: name, Group: "Operations", Description: description,
		Check: func(context.Context) ProbeResult {
			return ProbeResult{State: Maintenance, Summary: "This process is not running background jobs"}
		},
	}
}

func judgeWorker(now, started, beat time.Time, hasBeat bool, item note, stale, dead time.Duration) ProbeResult {
	age := time.Duration(0)
	if hasBeat {
		age = now.Sub(beat)
		if age < 0 {
			age = 0
		}
	}
	if !hasBeat {
		missing := now.Sub(started)
		switch {
		case missing < 90*time.Second:
			return ProbeResult{State: Operational, Summary: "Worker is starting"}
		case missing < 5*time.Minute:
			return ProbeResult{State: Degraded, Summary: "Worker has not reported"}
		default:
			return ProbeResult{State: Outage, Summary: "Worker has not reported"}
		}
	}
	switch {
	case age > dead:
		return ProbeResult{State: Outage, Summary: "Worker heartbeat is stale", Latency: age}
	case age > stale:
		return ProbeResult{State: Degraded, Summary: "Worker heartbeat is delayed", Latency: age}
	case item.state == Degraded && item.summary != "" && now.Sub(item.at) <= stale:
		return ProbeResult{State: Degraded, Summary: item.summary, Latency: age}
	default:
		return ProbeResult{State: Operational, Summary: "Worker heartbeat is current", Latency: age}
	}
}
