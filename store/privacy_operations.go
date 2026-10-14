package store

import (
	"time"

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
	return []string{"rides", "ride_events", "ride_idempotency_keys", "user_blocks", "reports", "report_notes", "support_tickets", "support_ticket_notes", "support_ticket_messages", "support_ticket_attachments", "account_flags", "notification_deliveries"}
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
	events := []models.RideEvent{}
	if err := e.s.db.Where("ride_id IN ?", rideIDs(rides)).Order("at ASC, id ASC").Find(&events).Error; err != nil {
		return nil, nil, err
	}
	eventOut := make([]map[string]any, 0, len(events))
	for _, event := range events {
		eventOut = append(eventOut, map[string]any{"ride_id": event.RideID, "type": event.Type, "at": event.At})
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
	tickets := []struct {
		ID        string    `json:"id"`
		Subject   string    `json:"subject"`
		Status    string    `json:"status"`
		CreatedAt time.Time `json:"created_at"`
	}{}
	if err := e.s.db.Model(&models.SupportTicket{}).Select("id, subject, status, created_at").Where("user_id = ?", id).Find(&tickets).Error; err != nil {
		return nil, nil, err
	}
	messageRows := []models.SupportTicketMessage{}
	if err := e.s.db.Where("ticket_id IN (?)", e.s.db.Model(&models.SupportTicket{}).Select("id").Where("user_id = ?", id)).Find(&messageRows).Error; err != nil {
		return nil, nil, err
	}
	attachmentRows := []models.SupportTicketAttachment{}
	if err := e.s.db.Where("ticket_id IN (?)", e.s.db.Model(&models.SupportTicket{}).Select("id").Where("user_id = ?", id)).Find(&attachmentRows).Error; err != nil {
		return nil, nil, err
	}
	return map[string]any{"rides": rideOut, "ride_events": eventOut, "reports_made": reports, "support_tickets": tickets, "ticket_messages": messageRows, "ticket_attachments": attachmentRows}, nil, nil
}

func rideIDs(rides []models.Ride) []string {
	ids := make([]string, 0, len(rides))
	for _, ride := range rides {
		ids = append(ids, ride.ID)
	}
	return ids
}
func (operationsPrivacy) Anonymize(tx *gorm.DB, id string) error {
	var rides []models.Ride
	if err := tx.Where("passenger_id = ? OR driver_id = ?", id, id).Find(&rides).Error; err != nil {
		return err
	}
	ids := rideIDs(rides)
	if err := tx.Model(&models.Ride{}).Where("passenger_id = ? OR driver_id = ?", id, id).Updates(map[string]any{"pickup": "", "dropoff": "", "route_geometry": "", "pickup_lat": 0, "pickup_lng": 0, "dropoff_lat": 0, "dropoff_lng": 0, "last_lat": 0, "last_lng": 0, "cancel_reason": ""}).Error; err != nil {
		return err
	}
	if len(ids) > 0 {
		if err := tx.Model(&models.RideEvent{}).Where("ride_id IN ?", ids).Updates(map[string]any{"actor_id": nil, "meta": ""}).Error; err != nil {
			return err
		}
	}
	if err := tx.Where("actor_id = ?", id).Delete(&models.RideIdempotencyKey{}).Error; err != nil {
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
	if err := tx.Model(&models.SupportTicket{}).Where("user_id = ?", id).Updates(map[string]any{"subject": "", "description": "", "resolution_note": ""}).Error; err != nil {
		return err
	}
	if err := tx.Where("ticket_id IN (?)", tx.Model(&models.SupportTicket{}).Select("id").Where("user_id = ?", id)).Delete(&models.SupportTicketNote{}).Error; err != nil {
		return err
	}
	if err := tx.Where("ticket_id IN (?)", tx.Model(&models.SupportTicket{}).Select("id").Where("user_id = ?", id)).Delete(&models.SupportTicketMessage{}).Error; err != nil {
		return err
	}
	if err := tx.Where("ticket_id IN (?)", tx.Model(&models.SupportTicket{}).Select("id").Where("user_id = ?", id)).Delete(&models.SupportTicketAttachment{}).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.AccountFlag{}).Where("user_id = ?", id).Update("reason", "").Error; err != nil {
		return err
	}
	return tx.Where("user_id = ?", id).Delete(&models.NotificationDelivery{}).Error
}
