package store

import (
	"gorm.io/gorm"
	"ride-backend/models"
)

func init() {
	RegisterDataExporter(func(s *Store) DataExporter { return operationsPrivacy{s} })
	RegisterAnonymizer(operationsPrivacy{})
}

type operationsPrivacy struct{ s *Store }

func (operationsPrivacy) Name() string { return "reports_and_rides" }
func (operationsPrivacy) Tables() []string {
	return []string{"rides", "user_blocks", "reports", "report_notes", "account_flags", "notification_deliveries"}
}
func (operationsPrivacy) Description() string {
	return "retains anonymous ride/report IDs and counts; clears routes, locations, report free text, staff notes, blocks and notifications"
}
func (e operationsPrivacy) Export(id string) (any, []string, error) {
	rides := []models.Ride{}
	if err := e.s.db.Where("passenger_id = ? OR driver_id = ?", id, id).Find(&rides).Error; err != nil {
		return nil, nil, err
	}
	// Other party IDs and precise real-time driver locations are not an export of this user.
	rideOut := []map[string]any{}
	for _, r := range rides {
		rideOut = append(rideOut, map[string]any{"id": r.ID, "status": r.Status, "fare": r.Fare, "pickup": r.Pickup, "dropoff": r.Dropoff, "created_at": r.CreatedAt})
	}
	reports := []struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		Category    string `json:"category"`
		Description string `json:"description"`
	}{}
	if err := e.s.db.Model(&models.Report{}).Select("id, status, category, description").Where("reporter_id = ?", id).Find(&reports).Error; err != nil {
		return nil, nil, err
	}
	return map[string]any{"rides": rideOut, "reports_made": reports}, nil, nil
}
func (operationsPrivacy) Anonymize(tx *gorm.DB, id string) error {
	if err := tx.Model(&models.Ride{}).Where("passenger_id = ? OR driver_id = ?", id, id).Updates(map[string]any{"pickup": "", "dropoff": "", "route_geometry": "", "pickup_lat": 0, "pickup_lng": 0, "dropoff_lat": 0, "dropoff_lng": 0, "last_lat": 0, "last_lng": 0}).Error; err != nil {
		return err
	}
	if err := tx.Where("blocker_id = ? OR blocked_id = ?", id, id).Delete(&models.UserBlock{}).Error; err != nil {
		return err
	}
	if err := tx.Where("report_id IN (?)", tx.Model(&models.Report{}).Select("id").Where("reporter_id = ? OR reported_id = ?", id, id)).Delete(&models.ReportNote{}).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.Report{}).Where("reporter_id = ? OR reported_id = ?", id, id).Updates(map[string]any{"description": "", "resolution_note": ""}).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.AccountFlag{}).Where("user_id = ?", id).Update("reason", "").Error; err != nil {
		return err
	}
	return tx.Where("user_id = ?", id).Delete(&models.NotificationDelivery{}).Error
}
