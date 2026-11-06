package store

import (
	"github.com/google/uuid"
	"gorm.io/gorm/clause"
	"ride-backend/models"
)

func (s *Store) requireSharedParticipant(id, actor string, active bool) error {
	if _, err := uuid.Parse(id); err != nil {
		return OpError(404, "Trip not found")
	}
	v, err := s.SharedDetail(id, actor, "")
	if err != nil {
		return err
	}
	if v.DriverID != actor && v.MySeat == nil {
		return OpError(404, "Trip not found")
	}
	if active && (v.Status != "waiting" && v.Status != "in_progress" || v.DriverID != actor && v.MySeat.Status != "held" && v.MySeat.Status != "confirmed") {
		return TransportError(409, "NO_ACTIVE_TRIP", "An active shared booking is required")
	}
	return nil
}
func (s *Store) SharedStaffDetail(id string) (models.SharedView, error) {
	var t models.SharedTrip
	if err := s.db.First(&t, "id=?", id).Error; err != nil {
		return models.SharedView{}, err
	}
	return s.sharedView(t, "", "", true, s.now().Unix())
}
func (s *Store) TriggerSharedSOS(actor, id string, in SOSInput) (*models.SOSAlert, error) {
	if !ValidLocation(in.Lat, in.Lng, in.Accuracy) {
		return nil, FieldError("location", "invalid coordinates or accuracy")
	}
	created := false
	var result models.SOSAlert
	err := s.Transaction(func(w *Store) error {
		if _, err := lockRideActor(w.db, actor); err != nil {
			return err
		}
		if err := w.requireSharedParticipant(id, actor, true); err != nil {
			return err
		}
		var n int64
		if err := w.db.Model(&models.SOSAlert{}).Where("triggered_by=? AND resolved_at IS NULL", actor).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return w.db.First(&result, "triggered_by=? AND resolved_at IS NULL", actor).Error
		}
		now := w.now()
		role := "passenger"
		var trip models.SharedTrip
		if err := w.db.First(&trip, "id=?", id).Error; err != nil {
			return err
		}
		if trip.DriverID == actor {
			role = "driver"
		}
		result = models.SOSAlert{ID: newID(), RideID: models.UnassignedRideDriverID, SharedTripID: &id, TriggeredBy: actor, Role: role, Status: "triggered", Lat: in.Lat, Lng: in.Lng, Accuracy: in.Accuracy, LastLat: in.Lat, LastLng: in.Lng, LastLocationAt: &now, Silent: in.Silent, CreatedAt: now}
		if err := w.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&result).Error; err != nil {
			return err
		}
		created = true
		if err := w.sosEvent(result.ID, "triggered", actor, "shared trip"); err != nil {
			return err
		}
		if err := w.enqueue("staff:support", "sos_created", "", ""); err != nil {
			return err
		}
		var contacts []models.EmergencyContact
		if err := w.db.Where("user_id=? AND notify_on_sos=true", actor).Limit(5).Find(&contacts).Error; err != nil {
			return err
		}
		for _, c := range contacts {
			if err := w.enqueueSOSContact(result.ID, actor, c.Phone, ""); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil && created {
		_ = s.Events.Publish("sos.created", map[string]string{"id": result.ID, "mode": "shared"})
	}
	return &result, err
}

type SharedRatingInput struct {
	Score   int    `json:"score"`
	Comment string `json:"comment"`
	SeatID  string `json:"seat_id"`
}

func (s *Store) RateShared(actor, id string, in SharedRatingInput) (*models.Rating, error) {
	if in.Score < 1 || in.Score > 5 || len([]rune(in.Comment)) > 2000 {
		return nil, FieldError("rating", "Choose 1–5 stars and at most 2000 comment characters")
	}
	var result models.Rating
	err := s.Transaction(func(w *Store) error {
		if err := lockTransportCursor(w.db); err != nil {
			return err
		}
		user, err := lockRideActor(w.db, actor)
		if err != nil {
			return err
		}
		var trip models.SharedTrip
		if err := w.db.First(&trip, "id=?", id).Error; err != nil {
			return err
		}
		if trip.Status != "completed" {
			return TransportError(409, "TRIP_NOT_COMPLETED", "Complete the shared trip before rating")
		}
		var seat models.SharedSeat
		q := w.db.Where("trip_id=? AND status='completed'", id)
		target := trip.DriverID
		if user.Role == models.RoleDriver && actor == trip.DriverID {
			q = q.Where("id=?", in.SeatID)
		} else if user.Role == models.RolePassenger {
			q = q.Where("passenger_id=?", actor)
		} else {
			return OpError(404, "Trip not found")
		}
		if err = q.First(&seat).Error; err != nil {
			return OpError(404, "Completed shared booking not found")
		}
		if actor == trip.DriverID {
			target = seat.PassengerID
		}
		if target == models.UnassignedRideDriverID {
			return OpError(409, "Account no longer available")
		}
		result = models.Rating{ID: newID(), SharedTripID: &id, FromUserID: actor, ToUserID: target, Score: in.Score, Comment: in.Comment, CreatedAt: w.now()}
		r := w.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&result)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			var previous models.Rating
			if err = w.db.First(&previous, "shared_trip_id=? AND from_user_id=? AND to_user_id=?", id, actor, target).Error; err != nil {
				return err
			}
			if previous.Score != in.Score || previous.Comment != in.Comment {
				return OpError(409, "This shared trip participant was already rated")
			}
			result = previous
		}
		return nil
	})
	if err == nil {
		s.recompute(result.ToUserID)
	}
	return &result, err
}
