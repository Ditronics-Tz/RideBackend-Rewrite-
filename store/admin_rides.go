package store

import (
	"strings"
	"time"

	"gorm.io/gorm/clause"
	"ride-backend/models"
)

type AdminRideFilter struct{ Status, Passenger, Driver, Q, From, To string }

func (s *Store) ListAdminRides(filter AdminRideFilter, page Page) ([]models.Ride, int64, error) {
	if page.Limit == 0 {
		page.Limit = 50
	}
	if page.Limit < 1 || page.Limit > 200 || page.Offset < 0 {
		return nil, 0, FieldError("pagination", "limit must be 1-200 and offset non-negative")
	}
	q := s.db.Model(&models.Ride{})
	for column, value := range map[string]string{"status": filter.Status, "passenger_id": filter.Passenger, "driver_id": filter.Driver} {
		if value != "" {
			q = q.Where(column+" = ?", value)
		}
	}
	if filter.Q != "" {
		needle := "%" + strings.ToLower(strings.TrimSpace(filter.Q)) + "%"
		q = q.Where("(LOWER(pickup) LIKE ? OR LOWER(dropoff) LIKE ? OR id IN (SELECT id FROM rides WHERE passenger_id IN (SELECT id FROM users WHERE LOWER(name) LIKE ? OR LOWER(email) LIKE ?) OR driver_id IN (SELECT id FROM users WHERE LOWER(name) LIKE ? OR LOWER(email) LIKE ?)))", needle, needle, needle, needle, needle, needle)
	}
	if filter.From != "" {
		from, err := time.Parse(time.RFC3339, filter.From)
		if err != nil {
			return nil, 0, FieldError("from", "from must be RFC3339")
		}
		q = q.Where("created_at >= ?", from)
	}
	if filter.To != "" {
		to, err := time.Parse(time.RFC3339, filter.To)
		if err != nil {
			return nil, 0, FieldError("to", "to must be RFC3339")
		}
		q = q.Where("created_at < ?", to)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, OpError(503, "Ride query unavailable")
	}
	rows := []models.Ride{}
	if err := q.Order("created_at DESC, id DESC").Limit(page.Limit).Offset(page.Offset).Find(&rows).Error; err != nil {
		return nil, 0, OpError(503, "Ride query unavailable")
	}
	return rows, total, nil
}

type AdminRideCancelInput struct {
	Reason string `json:"reason"`
}

func (s *Store) AdminCancelRide(rideID string, actor *models.StaffUser, reason string) error {
	reason = strings.TrimSpace(reason)
	if actor == nil || actor.Role != models.StaffRoleAdmin && actor.Role != models.StaffRoleSupport {
		return OpError(403, "Support or admin role required")
	}
	if reason == "" || len([]rune(reason)) > 250 {
		return FieldError("reason", "reason must contain 1-250 characters")
	}
	now := s.now()
	return s.Transaction(func(tx *Store) error {
		var ride models.Ride
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&ride, "id = ?", rideID).Error; err != nil {
			return OpError(404, "Ride not found")
		}
		if !containsStatus(ride.Status, rideActiveStatuses) {
			return OpError(409, "Only an active ride can be cancelled")
		}
		actorID := actor.ID
		ride.Status, ride.CancelReason, ride.CancelledBy, ride.CancelledAt = "cancelled_by_admin", reason, &actorID, &now
		ride.OfferExpiresAt, ride.UpdatedAt = nil, now
		if err := tx.db.Save(&ride).Error; err != nil {
			return err
		}
		if err := appendRideEvent(tx.db, ride.ID, "cancelled_by_staff", &actorID, string(actor.Role), now, map[string]any{"reason": reason}); err != nil {
			return err
		}
		return tx.CreateAuditLog(actor.ID, "ride_cancelled_by_staff", "ride", ride.ID, "", map[string]string{"reason": reason, "actor_role": string(actor.Role)})
	})
}

func (s *Store) AdminReassignRide(rideID, driverID string, actor *models.StaffUser, reason string) error {
	reason = strings.TrimSpace(reason)
	if actor == nil || actor.Role != models.StaffRoleAdmin && actor.Role != models.StaffRoleSupport {
		return OpError(403, "Support or admin role required")
	}
	if reason == "" || len([]rune(reason)) > 250 {
		return FieldError("reason", "reason must contain 1-250 characters")
	}
	now := s.now()
	return s.Transaction(func(tx *Store) error {
		var ride models.Ride
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&ride, "id = ?", rideID).Error; err != nil {
			return OpError(404, "Ride not found")
		}
		if !containsStatus(ride.Status, rideActiveStatuses) || ride.Status == "in_progress" {
			return OpError(409, "Only a ride awaiting or performing pickup can be reassigned")
		}
		scoped := *tx
		var driver models.DriverProfile
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&driver, "user_id = ?", driverID).Error; err != nil || !driver.IsOnline {
			return OpError(409, "Replacement driver must be online")
		}
		if ride.VehicleType != "" && driver.VehicleType != ride.VehicleType {
			return OpError(409, "Replacement driver has the wrong vehicle type")
		}
		if err := scoped.CheckDispatchEligibility(ride.PassengerID, driverID); err != nil {
			return OpError(409, "Replacement driver is not eligible")
		}
		var active int64
		if err := tx.db.Model(&models.Ride{}).Where("driver_id = ? AND status IN ? AND id <> ?", driverID, rideActiveStatuses, ride.ID).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return OpError(409, "Replacement driver already has an active ride")
		}
		previous := ride.DriverID
		actorID := actor.ID
		ride.DriverID, ride.Status, ride.AcceptedAt, ride.OfferExpiresAt, ride.UpdatedAt = driverID, "assigned", &now, nil, now
		if err := tx.db.Save(&ride).Error; err != nil {
			return err
		}
		if err := tx.db.Model(&models.DriverProfile{}).Where("user_id = ?", driverID).Update("is_online", false).Error; err != nil {
			return err
		}
		if err := appendRideEvent(tx.db, ride.ID, "staff_reassigned", &actorID, string(actor.Role), now, map[string]any{"reason": reason, "previous_driver_id": previous, "driver_id": driverID}); err != nil {
			return err
		}
		return tx.CreateAuditLog(actor.ID, "ride_reassigned", "ride", ride.ID, "", map[string]string{"reason": reason, "driver_id": driverID, "actor_role": string(actor.Role)})
	})
}

func containsStatus(status string, values []string) bool {
	for _, value := range values {
		if status == value {
			return true
		}
	}
	return false
}

func (s *Store) AdminOperationalCounts() (int64, int64, int64, error) {
	var rides, tickets, complaints int64
	if err := s.db.Model(&models.Ride{}).Where("status IN ?", rideActiveStatuses).Count(&rides).Error; err != nil {
		return 0, 0, 0, OpError(503, "Operational counts unavailable")
	}
	if err := s.db.Model(&models.SupportTicket{}).Where("status IN ?", []string{"open", "in_review"}).Count(&tickets).Error; err != nil {
		return 0, 0, 0, OpError(503, "Operational counts unavailable")
	}
	if err := s.db.Model(&models.Report{}).Where("status IN ?", []string{"open", "in_review"}).Count(&complaints).Error; err != nil {
		return 0, 0, 0, OpError(503, "Operational counts unavailable")
	}
	return rides, tickets, complaints, nil
}
