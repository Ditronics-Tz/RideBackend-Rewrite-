package store

import (
	"encoding/json"
	"sync"
)

type StreamEvent struct {
	Type string
	Data []byte
}
type EventHub struct {
	mu      sync.Mutex
	clients map[chan StreamEvent]struct{}
}

func NewEventHub() *EventHub { return &EventHub{clients: map[chan StreamEvent]struct{}{}} }
func (h *EventHub) Subscribe() (chan StreamEvent, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := make(chan StreamEvent, 32)
	h.clients[c] = struct{}{}
	return c, func() { h.mu.Lock(); delete(h.clients, c); h.mu.Unlock() }
}
func (h *EventHub) Publish(kind string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c <- StreamEvent{kind, raw}:
		default:
		}
	}
	return nil
}
