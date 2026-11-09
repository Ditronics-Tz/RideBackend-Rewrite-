package statuspage

import (
	"strings"
	"sync"
	"time"
)

// Worker names reported by the process loops.
const (
	WorkerDispatch   = "ride-dispatch"
	WorkerDocuments  = "document-expiry"
	WorkerPush       = "push-deliveries"
	WorkerOperations = "operations"
	WorkerPrivacy    = "data-requests"
)

type note struct {
	at      time.Time
	state   State
	summary string
}

var (
	beats sync.Map
	notes sync.Map
)

// Succeed records a finished worker tick.
func Succeed(name string) {
	stamp(name, Operational, "")
}

// Fail records a finished worker tick that could not complete its work.
// summary is a fixed phrase. Callers must not pass driver or request errors.
func Fail(name, summary string) {
	stamp(name, Degraded, summary)
}

func stamp(name string, state State, summary string) {
	now := time.Now()
	beats.Store(name, now)
	notes.Store(name, note{at: now, state: state, summary: clean(summary)})
}

func lastBeat(name string) (time.Time, bool) {
	value, ok := beats.Load(name)
	if !ok {
		return time.Time{}, false
	}
	beat, ok := value.(time.Time)
	return beat, ok
}

func lastNote(name string) (note, bool) {
	value, ok := notes.Load(name)
	if !ok {
		return note{}, false
	}
	item, ok := value.(note)
	return item, ok
}

func clean(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 180 {
		value = value[:180]
	}
	return value
}
