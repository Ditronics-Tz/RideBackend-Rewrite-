package store

import (
	"gorm.io/gorm"
	"ride-backend/models"
)

type sharedPrivacy struct{ s *Store }

func init() {
	RegisterDataExporter(func(s *Store) DataExporter { return sharedPrivacy{s} })
	RegisterAnonymizer(sharedPrivacy{})
	RegisterDeletionBlocker("shared_transport_commitments", func(s *Store, id string) ([]string, error) {
		driver, err := s.activeSharedDriver(id, s.now().Unix())
		if err != nil {
			return nil, err
		}
		passenger, err := s.activeSharedPassenger(id, s.now().Unix())
		if err != nil {
			return nil, err
		}
		var pending int64
		if err = s.db.Model(&models.TransportAssessment{}).Where("driver_id=? AND status='receivable'", id).Count(&pending).Error; err != nil {
			return nil, err
		}
		reasons := []string{}
		if driver || passenger {
			reasons = append(reasons, "active_shared_transport")
		}
		if pending > 0 {
			reasons = append(reasons, "unsettled_transport_fee")
		}
		return reasons, nil
	})
}
func (sharedPrivacy) Name() string { return "shared_rides" }
func (sharedPrivacy) Tables() []string {
	return []string{"shared_trips", "shared_seats", "shared_requests", "transport_events", "transport_assessments"}
}
func (sharedPrivacy) Description() string {
	return "Removes seat and retry identity and redacts driver route coordinates while retaining settlement references."
}
func (e sharedPrivacy) Export(id string) (any, []string, error) {
	var trips []models.SharedTrip
	var seats []models.SharedSeat
	err := e.s.db.Where("driver_id = ? OR id IN (?)", id, e.s.db.Model(&models.SharedSeat{}).Select("trip_id").Where("passenger_id = ?", id)).Find(&trips).Error
	if err != nil {
		return nil, nil, err
	}
	err = e.s.db.Where("passenger_id = ?", id).Find(&seats).Error
	var events []models.TransportEvent
	if err == nil {
		err = e.s.db.Where("actor_id=?", id).Find(&events).Error
	}
	var assessments []models.TransportAssessment
	if err == nil {
		err = e.s.db.Where("driver_id=?", id).Find(&assessments).Error
	}
	return map[string]any{"trips": trips, "seats": seats, "assessments": assessments, "events": events}, nil, err
}
func (sharedPrivacy) Anonymize(tx *gorm.DB, id string) error {
	var active int64
	if err := tx.Model(&models.SharedTrip{}).Where("status='in_progress' AND (driver_id=? OR id IN (SELECT trip_id FROM shared_seats WHERE passenger_id=? AND status='confirmed'))", id, id).Count(&active).Error; err != nil {
		return err
	}
	if active > 0 {
		return TransportError(409, "COMMITMENT_CONFLICT", "Finish the active shared trip before account deletion")
	}

	if err := tx.Model(&models.TransportEvent{}).Where("actor_id=?", id).Updates(map[string]any{"actor_id": models.UnassignedRideDriverID, "reason": ""}).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.TransportAssessment{}).Where("driver_id=?", id).Update("driver_id", models.UnassignedRideDriverID).Error; err != nil {
		return err
	}
	if err := tx.Where("actor = ?", id).Delete(&models.SharedRequest{}).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.SharedSeat{}).Where("passenger_id = ? AND status='held'", id).Update("status", "released").Error; err != nil {
		return err
	}
	if err := tx.Model(&models.SharedSeat{}).Where("passenger_id = ?", id).Update("passenger_id", models.UnassignedRideDriverID).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.SharedTrip{}).Where("driver_id = ? AND status IN ?", id, []string{"waiting", "in_progress"}).Update("status", "cancelled").Error; err != nil {
		return err
	}
	return tx.Model(&models.SharedTrip{}).Where("driver_id = ?", id).Updates(map[string]any{"driver_id": models.UnassignedRideDriverID, "pickup_lat": 0, "pickup_lng": 0, "dropoff_lat": 0, "dropoff_lng": 0, "pickup_address": "Removed", "dropoff_address": "Removed"}).Error
}
