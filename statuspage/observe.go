package statuspage

import (
	"sync"
	"time"
)

type observation struct {
	at     time.Time
	failed bool
	ms     int
}

var (
	obsMu        sync.Mutex
	observations []observation
)

// Observe records a finished HTTP response for the API error budget.
// Health, readiness and status polls are excluded so a database outage is not
// also counted as an API failure, and so this page cannot mark itself down.
func Observe(path string, status int, latency time.Duration) {
	switch path {
	case "/health", "/api/v1/health", "/ready", "/api/v1/ready", "/status", "/api/v1/status":
		return
	}
	if status < 100 {
		return
	}
	ms := int(latency / time.Millisecond)
	if ms < 0 {
		ms = 0
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	now := time.Now()
	observations = append(observations, observation{at: now, failed: status >= 500, ms: ms})
	cutoff := now.Add(-5 * time.Minute)
	kept := observations[:0]
	for _, item := range observations {
		if item.at.After(cutoff) {
			kept = append(kept, item)
		}
	}
	if len(kept) > 4000 {
		kept = append([]observation(nil), kept[len(kept)-4000:]...)
	}
	observations = kept
}

func requestWindow(within time.Duration) (total, failed int, avg time.Duration) {
	obsMu.Lock()
	defer obsMu.Unlock()
	cutoff := time.Now().Add(-within)
	sum := 0
	for _, item := range observations {
		if !item.at.After(cutoff) {
			continue
		}
		total++
		sum += item.ms
		if item.failed {
			failed++
		}
	}
	if total == 0 {
		return 0, 0, 0
	}
	return total, failed, time.Duration(sum/total) * time.Millisecond
}
