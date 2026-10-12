package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"gorm.io/gorm/clause"
	"math"
	"regexp"
	"ride-backend/models"
	"strings"
	"time"
)

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

func ValidLocation(lat, lng, accuracy float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lng) && !math.IsNaN(accuracy) && !math.IsInf(lat, 0) && !math.IsInf(lng, 0) && !math.IsInf(accuracy, 0) && lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180 && accuracy >= 0 && accuracy <= 100000
}
func (s *Store) SaveEmergencyContact(user string, c *models.EmergencyContact) error {
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 100 {
		return FieldError("name", "name is required (max 100)")
	}
	if !e164.MatchString(c.Phone) {
		return FieldError("phone", "use E.164 phone format")
	}
	if len(c.Relationship) > 100 {
		return FieldError("relationship", "relationship too long")
	}
	return s.Transaction(func(tx *Store) error {
		var u models.User
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", user).Error; err != nil {
			return err
		}
		c.UserID = user
		if c.ID == "" {
			var n int64
			if err := tx.db.Model(c).Where("user_id = ?", user).Count(&n).Error; err != nil {
				return err
			}
			if n >= 5 {
				return OpError(409, "maximum five emergency contacts")
			}
			c.ID = newID()
			c.CreatedAt = tx.now()
			return tx.db.Create(c).Error
		}
		result := tx.db.Model(&models.EmergencyContact{}).Where("id = ? AND user_id = ?", c.ID, user).Updates(map[string]any{"name": c.Name, "phone": c.Phone, "relationship": c.Relationship, "notify_on_sos": c.NotifyOnSOS, "notify_on_trip_share": c.NotifyOnTripShare})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return OpError(404, "contact not found")
		}
		return nil
	})
}
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (s *Store) createShare(ride, user string) (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes[:])
	err := s.db.Create(&models.ShareToken{ID: newID(), RideID: ride, CreatedBy: user, TokenHash: tokenHash(token), ExpiresAt: s.now().Add(12 * time.Hour), CreatedAt: s.now()}).Error
	return token, err
}
func (s *Store) ShareRide(ride, user, baseURL string, contacts []string) (string, error) {
	var token string
	err := s.Transaction(func(tx *Store) error {
		r, err := tx.RideForUser(ride, user)
		if err != nil {
			return err
		}
		if r.Status != "assigned" && r.Status != "in_progress" {
			return OpError(409, "only active rides can be shared")
		}
		token, err = tx.createShare(ride, user)
		if err != nil {
			return err
		}
		if len(contacts) > 5 {
			return FieldError("contact_ids", "maximum five contacts")
		}
		for _, id := range contacts {
			var c models.EmergencyContact
			if err := tx.db.First(&c, "id = ? AND user_id = ? AND notify_on_trip_share = ?", id, user, true).Error; err != nil {
				return err
			}
			if err := tx.enqueue(user, "trip_shared", c.Phone, strings.TrimRight(baseURL, "/")+"/api/v1/share/"+token); err != nil {
				return err
			}
		}
		return nil
	})
	return token, err
}
func (s *Store) StopSharing(ride, user string) error {
	if _, err := s.RideForUser(ride, user); err != nil {
		return err
	}
	return s.db.Where("ride_id = ? AND created_by = ?", ride, user).Delete(&models.ShareToken{}).Error
}
func (s *Store) PublicShare(token string) (map[string]any, error) {
	var st models.ShareToken
	missing := OpError(404, "trip not found")
	if len(token) != 64 {
		return nil, missing
	}
	if err := s.db.First(&st, "token_hash = ? AND expires_at > ?", tokenHash(token), s.now()).Error; err != nil {
		if notFound(err) {
			return nil, missing
		}
		return nil, err
	}
	var r models.Ride
	if err := s.db.First(&r, "id = ?", st.RideID).Error; err != nil {
		if notFound(err) {
			return nil, missing
		}
		return nil, err
	}
	if r.Status == "cancelled" || r.Status == "completed" {
		if r.CompletedAt == nil || !s.now().Before(r.CompletedAt.Add(30*time.Minute)) {
			return nil, missing
		}
	}
	var u models.User
	var p models.DriverProfile
	if err := s.db.First(&u, "id = ?", r.DriverID).Error; err != nil {
		return nil, err
	}
	if err := s.db.First(&p, "user_id = ?", r.DriverID).Error; err != nil {
		return nil, err
	}
	name := ""
	if parts := strings.Fields(u.Name); len(parts) > 0 {
		name = parts[0]
	}
	out := map[string]any{"driver_first_name": name, "vehicle_type": r.VehicleType, "plate": p.PlateNumber, "pickup": r.Pickup, "dropoff": r.Dropoff, "status": r.Status}
	if r.LastLocationAt != nil {
		out["last_driver_location"] = map[string]any{"lat": r.LastLat, "lng": r.LastLng, "age_seconds": int64(s.now().Sub(*r.LastLocationAt).Seconds())}
	}
	return out, nil
}

type SOSInput struct {
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	Accuracy float64 `json:"accuracy"`
	Silent   bool    `json:"silent"`
}

func (s *Store) TriggerSOS(user, ride, baseURL string, in SOSInput) (*models.SOSAlert, error) {
	if !ValidLocation(in.Lat, in.Lng, in.Accuracy) {
		return nil, FieldError("location", "invalid coordinates or accuracy")
	}
	var a models.SOSAlert
	created := false
	err := s.Transaction(func(tx *Store) error {
		r, err := tx.RideForUser(ride, user)
		if err != nil {
			return err
		}
		if r.Status != "assigned" && r.Status != "in_progress" {
			return OpError(409, "SOS requires an assigned or in-progress ride")
		}
		role := "passenger"
		if r.DriverID == user {
			role = "driver"
		}
		now := tx.now()
		a = models.SOSAlert{ID: newID(), RideID: ride, TriggeredBy: user, Role: role, Status: "triggered", Lat: in.Lat, Lng: in.Lng, Accuracy: in.Accuracy, LastLat: in.Lat, LastLng: in.Lng, LastLocationAt: &now, Silent: in.Silent, CreatedAt: now}
		result := tx.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&a)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			a = models.SOSAlert{}
			return tx.db.First(&a, "ride_id = ? AND triggered_by = ? AND resolved_at IS NULL", ride, user).Error
		}
		created = true
		if err := tx.sosEvent(a.ID, "triggered", user, ""); err != nil {
			return err
		}
		if err := tx.enqueue("staff:support", "sos_created", "", ""); err != nil {
			return err
		}
		contacts := []models.EmergencyContact{}
		if err := tx.db.Where("user_id = ? AND notify_on_sos = ?", user, true).Find(&contacts).Error; err != nil {
			return err
		}
		if len(contacts) > 0 {
			token, err := tx.createShare(ride, user)
			if err != nil {
				return err
			}
			for _, c := range contacts {
				if err := tx.enqueueSOSContact(a.ID, user, c.Phone, strings.TrimRight(baseURL, "/")+"/api/v1/share/"+token); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if created {
		if err := s.Events.Publish("sos.created", map[string]any{"id": a.ID}); err != nil {
			return nil, err
		}
	}
	return &a, nil
}
func (s *Store) sosEvent(id, kind, by, note string) error {
	return s.db.Create(&models.SOSEvent{ID: newID(), AlertID: id, Type: kind, By: by, Note: note, CreatedAt: s.now()}).Error
}
func (s *Store) SOSPing(user, id string, lat, lng, accuracy, speed float64) error {
	if !ValidLocation(lat, lng, accuracy) || speed < 0 || math.IsNaN(speed) || math.IsInf(speed, 0) {
		return FieldError("location", "invalid coordinates")
	}
	err := s.Transaction(func(tx *Store) error {
		var a models.SOSAlert
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&a, "id = ? AND triggered_by = ? AND resolved_at IS NULL", id, user).Error; err != nil {
			return err
		}
		var n int64
		if err := tx.db.Model(&models.SOSLocationPing{}).Where("alert_id = ? AND at > ?", id, tx.now().Add(-time.Minute)).Count(&n).Error; err != nil {
			return err
		}
		if n >= 30 {
			return &OperationError{Status: 429, Message: "location limit reached", RetryAfter: 2}
		}
		if err := tx.db.Create(&models.SOSLocationPing{ID: newID(), AlertID: id, Lat: lat, Lng: lng, Accuracy: accuracy, Speed: speed, At: tx.now(), CreatedAt: tx.now()}).Error; err != nil {
			return err
		}
		return tx.db.Model(&a).Updates(map[string]any{"last_lat": lat, "last_lng": lng, "last_location_at": tx.now()}).Error
	})
	if err != nil {
		return err
	}
	return s.Events.Publish("sos.location", map[string]any{"id": id})
}
func (s *Store) ReportSafe(user, id string, threshold int) error {
	err := s.Transaction(func(tx *Store) error {
		var a models.SOSAlert
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&a, "id = ? AND triggered_by = ?", id, user).Error; err != nil {
			return err
		}
		if a.ResolvedAt != nil {
			return nil
		}
		values := map[string]any{"user_reported_safe": true}
		if tx.now().Sub(a.CreatedAt) < time.Minute {
			values["status"] = "false_alarm"
			values["resolved_at"] = tx.now()
			values["outcome"] = "false_alarm"
		}
		if err := tx.db.Model(&a).Updates(values).Error; err != nil {
			return err
		}
		if err := tx.sosEvent(id, "user_reported_safe", user, ""); err != nil {
			return err
		}
		return tx.flagFalseAlarms(user, threshold)
	})
	if err != nil {
		return err
	}
	return s.Events.Publish("sos.updated", map[string]any{"id": id})
}
func (s *Store) flagFalseAlarms(user string, threshold int) error {
	if threshold < 1 {
		threshold = 3
	}
	var n int64
	if err := s.db.Model(&models.SOSAlert{}).Where("triggered_by = ? AND outcome = ?", user, "false_alarm").Count(&n).Error; err != nil {
		return err
	}
	if n < int64(threshold) {
		return nil
	}
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.AccountFlag{ID: newID(), UserID: user, Reason: "repeated_false_alarms", Source: "sos", Status: "open", CreatedAt: s.now()}).Error
}

type SOSAction struct {
	Note     string `json:"note"`
	Outcome  string `json:"outcome"`
	Override bool   `json:"override"`
	Who      string `json:"who"`
	Method   string `json:"method"`
	Result   string `json:"result"`
}

func (s *Store) WorkSOS(id, action string, actor *models.StaffUser, in SOSAction, threshold int) error {
	err := s.Transaction(func(tx *Store) error {
		var a models.SOSAlert
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&a, "id = ?", id).Error; err != nil {
			return err
		}
		if a.ResolvedAt != nil {
			return OpError(409, "alert already resolved")
		}
		if a.AssignedTo != nil && *a.AssignedTo != actor.ID && (actor.Role != models.StaffRoleAdmin || !in.Override) {
			return OpError(409, "alert is held by another staff member")
		}
		values := map[string]any{}
		note := in.Note
		switch action {
		case "acknowledge":
			values["assigned_to"] = actor.ID
			values["acknowledged_at"] = tx.now()
			values["status"] = "acknowledged"
		case "note":
			if err := cleanNote(in.Note, 4000); err != nil {
				return err
			}
			values["status"] = "responding"
		case "call-attempt":
			if (in.Who != "driver" && in.Who != "passenger" && in.Who != "emergency_contact") || (in.Method != "phone" && in.Method != "sms") || !map[string]bool{"answered": true, "no_answer": true, "failed": true}[in.Result] {
				return FieldError("call", "who, method and result must use documented values")
			}
			note = in.Who + ":" + in.Method + ":" + in.Result
			values["status"] = "responding"
		case "escalate":
			values["escalation_level"] = a.EscalationLevel + 1
			values["last_escalated_at"] = tx.now()
			if err := tx.enqueue("staff:admin", "sos_escalated", "", ""); err != nil {
				return err
			}
		case "resolve":
			if err := cleanNote(in.Note, 2000); err != nil {
				return err
			}
			if !map[string]bool{"safe": true, "police_contacted": true, "medical": true, "false_alarm": true, "other": true}[in.Outcome] {
				return FieldError("outcome", "invalid SOS outcome")
			}
			values["resolved_at"] = tx.now()
			values["status"] = "resolved"
			if in.Outcome == "false_alarm" {
				values["status"] = "false_alarm"
			}
			values["outcome"] = in.Outcome
			values["resolution_note"] = in.Note
		default:
			return OpError(404, "unknown SOS action")
		}
		if err := tx.db.Model(&a).Updates(values).Error; err != nil {
			return err
		}
		if err := tx.sosEvent(id, action, actor.ID, note); err != nil {
			return err
		}
		if action == "resolve" {
			if err := tx.flagFalseAlarms(a.TriggeredBy, threshold); err != nil {
				return err
			}
		}
		return tx.CreateAuditLog(actor.ID, "sos_"+action, "sos", id, "", map[string]any{"outcome": in.Outcome})
	})
	if err != nil {
		return err
	}
	return s.Events.Publish("sos.updated", map[string]any{"id": id})
}

func (s *Store) enqueueSOSContact(alert, user, phone, link string) error {
	return s.db.Create(&models.NotificationDelivery{ID: newID(), SOSAlertID: &alert, UserID: user, Template: "sos_contact", Language: "sw", Phone: phone, Link: link, Status: "pending", CreatedAt: s.now()}).Error
}
