package store

import (
	"context"

	"gorm.io/gorm"
	"ride-backend/models"
)

func init() {
	RegisterDataExporter(func(s *Store) DataExporter { return passengerDataPrivacy{s} })
	RegisterAnonymizer(passengerDataPrivacy{})
}

type passengerDataPrivacy struct{ s *Store }

func (passengerDataPrivacy) Name() string { return "passenger_mobile_data" }
func (passengerDataPrivacy) Tables() []string {
	return []string{"saved_places", "passenger_preferences", "scheduled_rides"}
}
func (passengerDataPrivacy) Description() string {
	return "deletes saved coordinates, settings and schedule records when account deletion completes"
}
func (e passengerDataPrivacy) Export(id string) (any, []string, error) {
	places := []models.SavedPlace{}
	if err := e.s.db.Where("user_id = ?", id).Find(&places).Error; err != nil {
		return nil, nil, err
	}
	prefs, err := e.s.PassengerPreferences(context.Background(), id)
	if err != nil {
		return nil, nil, err
	}
	rows := []models.ScheduledRide{}
	if err := e.s.db.Where("passenger_id = ?", id).Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	schedules := make([]models.ScheduledRideResponse, 0, len(rows))
	for _, row := range rows {
		schedules = append(schedules, row.Response())
	}
	return map[string]any{"saved_places": places, "preferences": prefs, "scheduled_rides": schedules}, nil, nil
}
func (passengerDataPrivacy) Anonymize(tx *gorm.DB, id string) error {
	if err := tx.Where("user_id = ?", id).Delete(&models.SavedPlace{}).Error; err != nil {
		return err
	}
	if err := tx.Where("user_id = ?", id).Delete(&models.PassengerPreferences{}).Error; err != nil {
		return err
	}
	return tx.Where("passenger_id = ?", id).Delete(&models.ScheduledRide{}).Error
}
