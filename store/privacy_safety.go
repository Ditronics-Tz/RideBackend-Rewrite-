package store

import (
	"gorm.io/gorm"
	"ride-backend/models"
)

func init() {
	RegisterDataExporter(func(s *Store) DataExporter { return safetyPrivacy{s} })
	RegisterAnonymizer(safetyPrivacy{})
}

type safetyPrivacy struct{ s *Store }

func (safetyPrivacy) Name() string { return "safety" }
func (safetyPrivacy) Tables() []string {
	return []string{"emergency_contacts", "sos_alerts", "sos_location_pings", "sos_events", "share_tokens", "incident_reports", "incident_attachments", "incident_notes"}
}
func (safetyPrivacy) Description() string {
	return "deletes contacts/share tokens/owned files; clears emergency and incident precise location and free text; retains anonymised incident IDs/status; staff notes are never exported"
}
func (e safetyPrivacy) Export(id string) (any, []string, error) {
	contacts := []models.EmergencyContact{}
	if err := e.s.db.Where("user_id = ?", id).Find(&contacts).Error; err != nil {
		return nil, nil, err
	}
	alerts := []models.SOSAlert{}
	if err := e.s.db.Where("triggered_by = ?", id).Find(&alerts).Error; err != nil {
		return nil, nil, err
	}
	for i := range alerts {
		alerts[i].ResolutionNote = ""
		alerts[i].AssignedTo = nil
	}
	incidents := []models.IncidentReport{}
	if err := e.s.db.Where("reporter_id = ?", id).Find(&incidents).Error; err != nil {
		return nil, nil, err
	}
	for i := range incidents {
		incidents[i].ResolutionNote = ""
		incidents[i].AssignedTo = nil
	}
	files := []models.IncidentAttachment{}
	if err := e.s.db.Where("user_id = ?", id).Find(&files).Error; err != nil {
		return nil, nil, err
	}
	paths := []string{}
	for _, f := range files {
		if f.FilePath != "" {
			paths = append(paths, f.FilePath)
		}
	}
	return map[string]any{"contacts": contacts, "alerts": alerts, "incidents": incidents}, paths, nil
}
func (safetyPrivacy) Anonymize(tx *gorm.DB, id string) error {
	for _, m := range []any{&models.EmergencyContact{}, &models.ShareToken{}, &models.IncidentAttachment{}} {
		column := "user_id"
		switch m.(type) {
		case *models.ShareToken:
			column = "created_by"
		}
		if err := tx.Where(column+" = ?", id).Delete(m).Error; err != nil {
			return err
		}
	}
	alertIDs := tx.Model(&models.SOSAlert{}).Select("id").Where("triggered_by = ?", id)
	if err := tx.Where("alert_id IN (?)", alertIDs).Delete(&models.SOSLocationPing{}).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.SOSEvent{}).Where("alert_id IN (?)", alertIDs).Update("note", "").Error; err != nil {
		return err
	}
	if err := tx.Model(&models.SOSAlert{}).Where("triggered_by = ?", id).Updates(map[string]any{"lat": 0, "lng": 0, "last_lat": 0, "last_lng": 0, "accuracy": 0, "resolution_note": ""}).Error; err != nil {
		return err
	}
	if err := tx.Where("incident_id IN (?)", tx.Model(&models.IncidentReport{}).Select("id").Where("reporter_id = ?", id)).Delete(&models.IncidentNote{}).Error; err != nil {
		return err
	}
	return tx.Model(&models.IncidentReport{}).Where("reporter_id = ?", id).Updates(map[string]any{"description": "", "location": "", "resolution_note": ""}).Error
}
