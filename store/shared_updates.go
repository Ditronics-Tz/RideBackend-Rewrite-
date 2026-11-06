package store

import (
	"context"
	"encoding/json"
	"ride-backend/models"
	"time"
)

// The notification is a hint to refetch authorized state. Cross-instance updates
// and lost notifications are recovered by the two-second snapshot fallback.
type sharedNotice struct {
	ID       string
	Lat, Lng float64
}

func (s *Store) NotifyShared(v models.SharedView) {
	if s.sharedEvents != nil {
		_ = s.sharedEvents.Publish("shared", sharedNotice{v.ID, v.Pickup.Lat, v.Pickup.Lng})
	}
}
func (s *Store) WaitForShared(ctx context.Context, id string, lat, lng float64) {
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	if s.sharedEvents == nil {
		select {
		case <-ctx.Done():
		case <-timeout.C:
		}
		return
	}
	events, detach := s.sharedEvents.Subscribe()
	defer detach()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timeout.C:
			return
		case event := <-events:
			var v sharedNotice
			if json.Unmarshal(event.Data, &v) == nil && (id != "" && v.ID == id || id == "" && RideDistanceMeters(lat, lng, v.Lat, v.Lng) <= 10000) {
				return
			}
		}
	}
}
