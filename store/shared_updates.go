package store

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/models"
	"time"
)

func lockTransportCursor(tx *gorm.DB) error {
	seed := models.TransportCursor{ID: 1}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return err
	}
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&seed, "id=1").Error
}

// Caller holds the cursor and trip lock, and commits this outbox row together
// with the state change. Consumers read the durable log directly; no publish
// acknowledgment can lose an event between commit and delivery.
func (s *Store) appendTransportEvent(trip *models.SharedTrip, actor, kind string, reason ...string) error {
	var cursor models.TransportCursor
	if err := s.db.First(&cursor, "id=1").Error; err != nil {
		return err
	}
	cursor.Revision++
	if err := s.db.Save(&cursor).Error; err != nil {
		return err
	}
	trip.Version++
	if err := s.db.Model(trip).Update("version", trip.Version).Error; err != nil {
		return err
	}
	if actor == "" {
		actor = models.UnassignedRideDriverID
	}
	note := ""
	if len(reason) > 0 {
		note = reason[0]
		if len([]rune(note)) > 250 {
			return FieldError("reason", "Use at most 250 characters")
		}
	}
	return s.db.Create(&models.TransportEvent{Reason: note, ID: cursor.Revision, TripID: trip.ID, Version: trip.Version, Type: kind, ActorID: actor, At: s.now().Unix()}).Error
}

// Notifications are only an optimization; durable polling does not depend on a
// per-process hub and works after reconnect and on multiple API instances.
func (s *Store) NotifyShared(v models.SharedView) {}

type TransportChanges struct {
	Cursor     int64                   `json:"cursor"`
	Events     []models.TransportEvent `json:"events"`
	Reset      bool                    `json:"reset"`
	ServerTime int64                   `json:"server_time"`
}

func (s *Store) SharedChanges(ctx context.Context, actor, id string, lat, lng, radius float64, after int64) (TransportChanges, error) {
	result := TransportChanges{Cursor: after, Events: []models.TransportEvent{}, ServerTime: s.now().Unix()}
	var cursor models.TransportCursor
	if err := s.db.WithContext(ctx).First(&cursor, "id=1").Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	if after < 0 || after > cursor.Revision {
		result.Reset = true
		result.Cursor = cursor.Revision
		return result, nil
	}
	// Bound replay scans, including non-matching geographic events. Never advance
	// beyond rows scanned, which would skip matching events on a following page.
	var events []models.TransportEvent
	if err := s.db.WithContext(ctx).Where("id>?", after).Order("id").Limit(200).Find(&events).Error; err != nil {
		return result, err
	}
	for _, event := range events {
		result.Cursor = event.ID
		if id != "" && event.TripID != id {
			continue
		}
		var t models.SharedTrip
		if err := s.db.WithContext(ctx).First(&t, "id=?", event.TripID).Error; err != nil {
			return result, err
		}
		if id == "" && RideDistanceMeters(lat, lng, t.Pickup.Lat, t.Pickup.Lng) > radius*1000 {
			continue
		}
		if id == "" {
			var blocked int64
			if err := s.db.Model(&models.UserBlock{}).Where("(blocker_id=? AND blocked_id=?) OR (blocker_id=? AND blocked_id=?)", actor, t.DriverID, t.DriverID, actor).Count(&blocked).Error; err != nil {
				return result, err
			}
			if blocked > 0 {
				continue
			}
		}
		result.Events = append(result.Events, event)
	}
	return result, nil
}

func (s *Store) WaitSharedChanges(ctx context.Context, actor, id string, lat, lng, radius float64, after int64) (TransportChanges, error) {
	deadline := time.NewTimer(1500 * time.Millisecond)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := s.SharedChanges(ctx, actor, id, lat, lng, radius, after)
		if err != nil || result.Reset || len(result.Events) > 0 || result.Cursor > after {
			return result, err
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-deadline.C:
			return result, nil
		case <-ticker.C:
		}
	}
}

func (s *Store) WaitForShared(ctx context.Context, id string, lat, lng float64) {
	_, _ = s.WaitSharedChanges(ctx, "", id, lat, lng, 10, 0)
}

// A worker materializes expiry, but sharedView and allocation always apply time
// semantics independently, even if this worker has stopped.
func (s *Store) ExpireShared() error {
	now := s.now().Unix()
	var ids []string
	if err := s.db.Model(&models.SharedTrip{}).Where("status='waiting' AND ((deadline<=? AND NOT EXISTS (SELECT 1 FROM shared_seats c WHERE c.trip_id=shared_trips.id AND c.status='confirmed')) OR EXISTS (SELECT 1 FROM shared_seats b WHERE b.trip_id=shared_trips.id AND b.status='held' AND b.expires<=?))", now, now).Order("deadline,id").Limit(100).Pluck("id", &ids).Error; err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.Transaction(func(w *Store) error {
			if err := lockTransportCursor(w.db); err != nil {
				return err
			}
			var t models.SharedTrip
			if err := w.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&t, "id=?", id).Error; err != nil {
				return err
			}
			if t.Status != "waiting" {
				return nil
			}
			var expired []models.SharedSeat
			if err := w.db.Where("trip_id=? AND status='held' AND expires<=?", id, now).Find(&expired).Error; err != nil {
				return err
			}
			for _, seat := range expired {
				if err := w.db.Model(&seat).Update("status", "expired").Error; err != nil {
					return err
				}
				if err := w.queueAppNotification(w.db.Statement.Context, seat.PassengerID, "ride", "Seat hold expired", "Your five-minute seat hold expired.", map[string]string{"shared_trip_id": id, "status": "expired"}); err != nil {
					return err
				}
			}
			v, err := w.sharedView(t, "", "", true, now)
			if err != nil {
				return err
			}
			if v.Status == "expired" {
				t.Status = "expired"
				if err := w.db.Save(&t).Error; err != nil {
					return err
				}
			}
			if len(expired) > 0 || t.Status == "expired" {
				return w.appendTransportEvent(&t, "", "shared_trip.expiry")
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
