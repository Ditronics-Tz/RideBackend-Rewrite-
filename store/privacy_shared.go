package store

import (
	"gorm.io/gorm"
	"ride-backend/models"
)

type sharedPrivacy struct{ s *Store }

func init() {
	RegisterDataExporter(func(s *Store) DataExporter { return sharedPrivacy{s} })
	RegisterAnonymizer(sharedPrivacy{})
}
func (sharedPrivacy) Name() string { return "shared_rides" }
func (sharedPrivacy) Tables() []string {
	return []string{"shared_trips", "shared_seats", "shared_requests"}
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
	return map[string]any{"trips": trips, "seats": seats}, nil, err
}
func (sharedPrivacy) Anonymize(tx *gorm.DB, id string) error {
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
