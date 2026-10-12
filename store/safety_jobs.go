package store

import (
	"context"
	"gorm.io/gorm/clause"
	"ride-backend/config"
	"ride-backend/models"
	"ride-backend/notifications"
	"strings"
	"time"
)

func (s *Store) ProcessSafety(cfg *config.Config) error {
	sla := cfg.SOSAckSLASeconds
	if sla < 1 {
		sla = 60
	}
	cutoff := s.now().Add(-time.Duration(sla) * time.Second)
	rows := []models.SOSAlert{}
	if err := s.db.Where("resolved_at IS NULL AND acknowledged_at IS NULL AND created_at <= ? AND (last_escalated_at IS NULL OR last_escalated_at <= ?) AND escalation_level < 3", cutoff, cutoff).Order("created_at ASC, id ASC").Limit(100).Find(&rows).Error; err != nil {
		return err
	}
	for _, a := range rows {
		claimed := false
		err := s.Transaction(func(tx *Store) error {
			result := tx.db.Model(&models.SOSAlert{}).Where("id = ? AND escalation_level = ? AND acknowledged_at IS NULL AND resolved_at IS NULL AND (last_escalated_at IS NULL OR last_escalated_at <= ?)", a.ID, a.EscalationLevel, cutoff).Updates(map[string]any{"escalation_level": a.EscalationLevel + 1, "last_escalated_at": tx.now()})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return nil
			}
			claimed = true
			if err := tx.enqueue("staff:support", "sos_escalated", "", ""); err != nil {
				return err
			}
			if a.EscalationLevel >= 1 {
				if err := tx.enqueue("staff:admin", "sos_escalated", "", ""); err != nil {
					return err
				}
			}
			for _, phone := range strings.Split(cfg.SOSOncallPhones, ",") {
				phone = strings.TrimSpace(phone)
				if phone != "" {
					if !e164.MatchString(phone) {
						return FieldError("SOS_ONCALL_PHONES", "oncall numbers must use E.164")
					}
					if err := tx.enqueue("oncall", "sos_escalated", phone, ""); err != nil {
						return err
					}
				}
			}
			return tx.sosEvent(a.ID, "escalated", "system", "")
		})
		if err != nil {
			return err
		}
		if claimed {
			if err := s.Events.Publish("sos.escalated", map[string]any{"id": a.ID}); err != nil {
				return err
			}
		}
	}
	days := cfg.SOSLocationRetentionDays
	if days < 1 {
		days = 30
	}
	if err := s.db.Where("at < ?", s.now().AddDate(0, 0, -days)).Delete(&models.SOSLocationPing{}).Error; err != nil {
		return err
	}
	if err := s.db.Model(&models.SOSAlert{}).Where("resolved_at IS NOT NULL AND created_at < ?", s.now().AddDate(0, 0, -days)).Updates(map[string]any{"lat": 0, "lng": 0, "last_lat": 0, "last_lng": 0, "accuracy": 0, "last_location_at": nil}).Error; err != nil {
		return err
	}
	return s.ProcessNotifications()
}
func (s *Store) ProcessNotifications() error {
	if err := s.db.Model(&models.NotificationDelivery{}).Where("status = ? AND claimed_at < ?", "processing", s.now().Add(-time.Minute)).Update("status", "pending").Error; err != nil {
		return err
	}
	rows := []models.NotificationDelivery{}
	if err := s.db.Where("status = ?", "pending").Order("created_at ASC, id ASC").Limit(100).Find(&rows).Error; err != nil {
		return err
	}
	for _, n := range rows {
		r := s.db.Model(&models.NotificationDelivery{}).Where("id = ? AND status = ?", n.ID, "pending").Updates(map[string]any{"status": "processing", "claimed_at": s.now(), "attempts": n.Attempts + 1})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			continue
		}
		channel := "push"
		if n.Phone != "" {
			channel = "sms"
		}
		if strings.HasPrefix(n.Template, "sos_") {
			channel = "critical"
		}
		err := s.Delivery.Send(context.Background(), notifications.Message{ID: n.ID, Recipient: n.UserID, Phone: n.Phone, Template: n.Template, Language: n.Language, Link: n.Link, Channel: channel})
		status := "delivered"
		if err != nil {
			status = "pending"
			if n.Attempts >= 9 {
				status = "failed"
			}
		}
		if updateErr := s.Transaction(func(tx *Store) error {
			result := tx.db.Model(&n).Where("status = ?", "processing").Update("status", status)
			if result.Error != nil {
				return result.Error
			}
			if status == "delivered" && result.RowsAffected == 1 && n.SOSAlertID != nil {
				return tx.sosEvent(*n.SOSAlertID, "contacts_notified", "system", "")
			}
			return nil
		}); updateErr != nil {
			return updateErr
		}
		if err != nil {
			return OpError(502, "notification provider failed; delivery retained for retry")
		}
	}
	return nil
}
func init() {
	RegisterDeletionBlocker("active_rides", func(s *Store, id string) ([]string, error) {
		var n int64
		if err := s.db.Model(&models.Ride{}).Where("(passenger_id = ? OR driver_id = ?) AND status IN ?", id, id, []string{"assigned", "in_progress"}).Count(&n).Error; err != nil {
			return nil, err
		}
		if n > 0 {
			return []string{"ongoing ride"}, nil
		}
		return nil, nil
	})
}
func (s *Store) CreateIncident(user string, r *models.IncidentReport) error {
	if !map[string]bool{"accident": true, "harassment": true, "assault": true, "theft": true, "dangerous_driving": true, "vehicle_issue": true, "other": true}[r.Category] {
		return FieldError("category", "invalid incident category")
	}
	if err := cleanNote(r.Description, 4000); err != nil {
		return FieldError("description", "description is required, max 4000 characters")
	}
	if len(r.Location) > 500 {
		return FieldError("location", "location too long")
	}
	if r.OccurredAt.IsZero() || r.OccurredAt.After(s.now()) {
		return FieldError("occurred_at", "occurred_at must not be in the future")
	}
	return s.Transaction(func(tx *Store) error {
		ride, err := tx.RideForUser(r.RideID, user)
		if err != nil {
			return err
		}
		r.ID = newID()
		r.ReporterID = user
		r.Status = "new"
		r.CreatedAt = tx.now()
		r.Severity = "normal"
		if r.Category == "assault" || r.Category == "accident" {
			r.Severity = "high"
		}
		r.AssignedTo = nil
		r.ResolutionNote = ""
		if err := tx.db.Create(r).Error; err != nil {
			return err
		}
		if r.Severity == "high" {
			other := ride.DriverID
			if user == other {
				other = ride.PassengerID
			}
			report := models.Report{ID: newID(), ReporterID: user, ReportedID: other, RideID: ride.ID, Category: "other", Description: r.Description, Status: "open", Priority: "high", CreatedAt: tx.now()}
			if err := tx.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&report).Error; err != nil {
				return err
			}
			return tx.enqueue("staff:support", "account_flag", "", "")
		}
		return nil
	})
}
func (s *Store) WorkIncident(id, action string, actor *models.StaffUser, note string) error {
	return s.Transaction(func(tx *Store) error {
		var r models.IncidentReport
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&r, "id = ?", id).Error; err != nil {
			return err
		}
		if r.AssignedTo != nil && *r.AssignedTo != actor.ID && actor.Role != models.StaffRoleAdmin {
			return OpError(409, "incident held by another reviewer")
		}
		switch action {
		case "assign":
			if err := tx.db.Model(&r).Updates(map[string]any{"assigned_to": actor.ID, "status": "in_review"}).Error; err != nil {
				return err
			}
		case "note":
			if err := cleanNote(note, 4000); err != nil {
				return err
			}
			if err := tx.db.Create(&models.IncidentNote{ID: newID(), IncidentID: id, StaffID: actor.ID, Note: note, CreatedAt: tx.now()}).Error; err != nil {
				return err
			}
		case "resolve":
			if err := cleanNote(note, 2000); err != nil {
				return err
			}
			if err := tx.db.Model(&r).Updates(map[string]any{"status": "resolved", "resolution_note": note}).Error; err != nil {
				return err
			}
		default:
			return OpError(404, "unknown incident action")
		}
		return tx.CreateAuditLog(actor.ID, "incident_"+action, "incident", id, "", nil)
	})
}
