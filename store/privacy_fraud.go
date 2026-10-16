package store

import (
	"gorm.io/gorm"
	"ride-backend/models"
)

func init() {
	RegisterDataExporter(func(s *Store) DataExporter { return fraudPrivacy{s} })
	RegisterAnonymizer(fraudPrivacy{})
}

type fraudPrivacy struct{ s *Store }

func (fraudPrivacy) Name() string { return "devices_and_location" }
func (fraudPrivacy) Tables() []string {
	return []string{"user_devices", "push_devices", "driver_locations", "ride_location_points", "fraud_flags", "fraud_rules", "fraud_actions", "fraud_notes", "fraud_mitigations", "fraud_events"}
}
func (fraudPrivacy) Description() string {
	return "removes raw IP/device hashes and location points; clears internal fraud notes; retains anonymous rule decisions, action history and financial-hold IDs; rules are global settings"
}
func (e fraudPrivacy) Export(id string) (any, []string, error) {
	devices := []struct {
		Platform  string `json:"platform"`
		FirstSeen string `json:"first_seen"`
		LastSeen  string `json:"last_seen"`
	}{}
	if err := e.s.db.Model(&models.UserDevice{}).Where("user_id = ?", id).Select("platform,first_seen,last_seen").Scan(&devices).Error; err != nil {
		return nil, nil, err
	}
	points := []models.RideLocationPoint{}
	if err := e.s.db.Where("user_id = ?", id).Find(&points).Error; err != nil {
		return nil, nil, err
	}
	return map[string]any{"devices": devices, "location_points": points}, nil, nil
}
func (fraudPrivacy) Anonymize(tx *gorm.DB, id string) error {
	if err := tx.Where("driver_id = ?", id).Delete(&models.DriverLocation{}).Error; err != nil {
		return err
	}
	for _, model := range []any{&models.UserDevice{}, &models.PushDevice{}, &models.RideLocationPoint{}} {
		if err := tx.Where("user_id = ?", id).Delete(model).Error; err != nil {
			return err
		}
	}
	if err := tx.Where("flag_id IN (?)", tx.Model(&models.FraudFlag{}).Select("id").Where("user_id = ?", id)).Delete(&models.FraudNote{}).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.DriverPayout{}).Where("driver_id = ?", id).Update("destination_hash", "").Error; err != nil {
		return err
	}
	return tx.Model(&models.FraudFlag{}).Where("user_id = ?", id).Update("resolution_note", "").Error
}
