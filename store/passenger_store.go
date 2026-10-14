package store

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/config"
	"ride-backend/models"
)

func validRidePoint(p models.RidePoint) bool {
	return !math.IsNaN(p.Lat) && !math.IsNaN(p.Lng) && !math.IsInf(p.Lat, 0) && !math.IsInf(p.Lng, 0) && p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180
}

func ValidateRideInput(in models.CreateRideInput) (models.CreateRideInput, float64, error) {
	in.Pickup.Address = strings.TrimSpace(in.Pickup.Address)
	in.Dropoff.Address = strings.TrimSpace(in.Dropoff.Address)
	in.VehicleType = strings.TrimSpace(in.VehicleType)
	if n := len([]rune(in.Pickup.Address)); n < 1 || n > 250 {
		return in, 0, FieldError("pickup", "pickup address must contain 1-250 characters")
	}
	if n := len([]rune(in.Dropoff.Address)); n < 1 || n > 250 {
		return in, 0, FieldError("dropoff", "dropoff address must contain 1-250 characters")
	}
	if !validRidePoint(in.Pickup) || !validRidePoint(in.Dropoff) {
		return in, 0, FieldError("coordinates", "latitude or longitude is outside the supported range")
	}
	if len([]rune(in.VehicleType)) > 50 {
		return in, 0, FieldError("vehicle_type", "vehicle type must be at most 50 characters")
	}
	distance := RideDistanceMeters(in.Pickup.Lat, in.Pickup.Lng, in.Dropoff.Lat, in.Dropoff.Lng)
	if distance < 1 {
		return in, 0, FieldError("dropoff", "pickup and dropoff must be different locations")
	}
	return in, distance, nil
}

func validatePlace(in models.SavedPlaceInput) (models.SavedPlaceInput, error) {
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	in.Label = strings.TrimSpace(in.Label)
	in.Address = strings.TrimSpace(in.Address)
	if in.Kind != "home" && in.Kind != "work" && in.Kind != "custom" {
		return in, FieldError("kind", "kind must be home, work or custom")
	}
	if len([]rune(in.Label)) < 1 || len([]rune(in.Label)) > 80 {
		return in, FieldError("label", "label must contain 1-80 characters")
	}
	if len([]rune(in.Address)) < 1 || len([]rune(in.Address)) > 250 {
		return in, FieldError("address", "address must contain 1-250 characters")
	}
	if !validRidePoint(models.RidePoint{Lat: in.Lat, Lng: in.Lng}) {
		return in, FieldError("coordinates", "latitude or longitude is outside the supported range")
	}
	return in, nil
}

func (s *Store) ListSavedPlaces(ctx context.Context, userID string) ([]models.SavedPlace, error) {
	rows := []models.SavedPlace{}
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at ASC, id ASC").Find(&rows).Error
	if err != nil {
		return nil, OpError(503, "Saved places unavailable")
	}
	return rows, nil
}

func (s *Store) SavePlace(ctx context.Context, userID, id string, input models.SavedPlaceInput) (*models.SavedPlace, error) {
	in, err := validatePlace(input)
	if err != nil {
		return nil, err
	}
	if id != "" {
		if _, err := uuid.Parse(id); err != nil {
			return nil, FieldError("id", "valid place ID required")
		}
	}
	var place models.SavedPlace
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := lockRideActor(tx, userID)
		if err != nil {
			return err
		}
		if user.Role != models.RolePassenger {
			return OpError(403, "Passenger account required")
		}
		if id != "" {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", id, userID).First(&place).Error; err != nil {
				return OpError(404, "Saved place not found")
			}
		} else {
			var count int64
			if err := tx.Model(&models.SavedPlace{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
				return err
			}
			if count >= 20 {
				return OpError(409, "Maximum 20 saved places")
			}
			place = models.SavedPlace{ID: newID(), UserID: userID, CreatedAt: s.now()}
		}
		if in.Kind != "custom" {
			var count int64
			if err := tx.Model(&models.SavedPlace{}).Where("user_id = ? AND kind = ? AND id <> ?", userID, in.Kind, place.ID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return OpError(409, in.Kind+" place already exists")
			}
		}
		place.Kind, place.Label, place.Address = in.Kind, in.Label, in.Address
		place.Lat, place.Lng, place.UpdatedAt = in.Lat, in.Lng, s.now()
		if id == "" {
			return tx.Create(&place).Error
		}
		return tx.Save(&place).Error
	})
	if err != nil {
		var op *OperationError
		if errors.As(err, &op) {
			return nil, err
		}
		return nil, OpError(503, "Saved place unavailable")
	}
	return &place, nil
}

func (s *Store) DeleteSavedPlace(ctx context.Context, userID, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return FieldError("id", "valid place ID required")
	}
	result := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).Delete(&models.SavedPlace{})
	if result.Error != nil {
		return OpError(503, "Saved place unavailable")
	}
	if result.RowsAffected == 0 {
		return OpError(404, "Saved place not found")
	}
	return nil
}

func defaultPassengerPreferences(userID string) models.PassengerPreferences {
	return models.PassengerPreferences{UserID: userID, PreciseLocation: true, RideUpdates: true, TripMessages: true, PushEnabled: true, Language: "en"}
}

func (s *Store) PassengerPreferences(ctx context.Context, userID string) (*models.PassengerPreferences, error) {
	var prefs models.PassengerPreferences
	err := s.db.WithContext(ctx).First(&prefs, "user_id = ?", userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		value := defaultPassengerPreferences(userID)
		return &value, nil
	}
	if err != nil {
		return nil, OpError(503, "Preferences unavailable")
	}
	return &prefs, nil
}

// PatchPassengerPreferences accepts only the documented keys. The HTTP handler
// builds updates from pointer fields so explicit false values are retained.
func (s *Store) PatchPassengerPreferences(ctx context.Context, userID string, updates map[string]any) (*models.PassengerPreferences, error) {
	if len(updates) == 0 {
		return nil, FieldError("body", "at least one preference is required")
	}
	if language, ok := updates["language"].(string); ok && language != "en" && language != "sw" {
		return nil, FieldError("language", "language must be en or sw")
	}
	var prefs models.PassengerPreferences
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := lockRideActor(tx, userID)
		if err != nil {
			return err
		}
		if user.Role != models.RolePassenger {
			return OpError(403, "Passenger account required")
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&prefs, "user_id = ?", userID).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			prefs = defaultPassengerPreferences(userID)
			prefs.UpdatedAt = s.now()
			if err := tx.Create(&prefs).Error; err != nil {
				return err
			}
		}
		updates["updated_at"] = s.now()
		if err := tx.Model(&prefs).Updates(updates).Error; err != nil {
			return err
		}
		return tx.First(&prefs, "user_id = ?", userID).Error
	})
	if err != nil {
		return nil, OpError(503, "Preferences unavailable")
	}
	return &prefs, nil
}

func (s *Store) CreateScheduledRide(ctx context.Context, userID, key string, in models.ScheduledRideInput) (*models.ScheduledRide, error) {
	if len(key) < 8 || len(key) > 100 {
		return nil, FieldError("Idempotency-Key", "Idempotency-Key must contain 8-100 characters")
	}
	request, _, err := ValidateRideInput(models.CreateRideInput{Pickup: in.Pickup, Dropoff: in.Dropoff, VehicleType: in.VehicleType})
	if err != nil {
		return nil, err
	}
	now := s.now()
	when := in.ScheduledFor.UTC()
	if when.Before(now.Add(5*time.Minute)) || when.After(now.Add(30*24*time.Hour)) {
		return nil, FieldError("scheduled_for", "scheduled_for must be 5 minutes to 30 days in the future")
	}
	hash, err := rideRequestHash(models.ScheduledRideInput{Pickup: request.Pickup, Dropoff: request.Dropoff, VehicleType: request.VehicleType, ScheduledFor: when})
	if err != nil {
		return nil, err
	}
	var row models.ScheduledRide
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := lockRideActor(tx, userID)
		if err != nil {
			return err
		}
		if user.Role != models.RolePassenger {
			return OpError(403, "Passenger account required")
		}
		if err := tx.Where("passenger_id = ? AND request_key = ?", userID, key).First(&row).Error; err == nil {
			if row.RequestHash != hash {
				return OpError(409, "Idempotency-Key was already used for another schedule")
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var count int64
		if err := tx.Model(&models.ScheduledRide{}).Where("passenger_id = ? AND status IN ?", userID, []string{"scheduled", "dispatching"}).Count(&count).Error; err != nil {
			return err
		}
		if count >= 10 {
			return OpError(409, "Maximum 10 upcoming rides")
		}
		row = models.ScheduledRide{ID: newID(), PassengerID: userID, PickupAddress: request.Pickup.Address, PickupLat: request.Pickup.Lat, PickupLng: request.Pickup.Lng, DropoffAddress: request.Dropoff.Address, DropoffLat: request.Dropoff.Lat, DropoffLng: request.Dropoff.Lng, VehicleType: request.VehicleType, ScheduledFor: when, Status: "scheduled", FailureCode: "", RequestKey: key, RequestHash: hash, CreatedAt: now, UpdatedAt: now}
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "passenger_id"}, {Name: "request_key"}}, DoNothing: true}).Create(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if err := tx.Where("passenger_id = ? AND request_key = ?", userID, key).First(&row).Error; err != nil {
				return err
			}
			if row.RequestHash != hash {
				return OpError(409, "Idempotency-Key was already used for another schedule")
			}
		}
		return nil
	})
	if err != nil {
		var op *OperationError
		if errors.As(err, &op) {
			return nil, err
		}
		return nil, OpError(503, "Scheduled rides unavailable")
	}
	return &row, nil
}

func (s *Store) ListScheduledRides(ctx context.Context, userID, status string, page Page) ([]models.ScheduledRide, int64, error) {
	if page.Limit == 0 {
		page.Limit = 50
	}
	if page.Limit < 1 || page.Limit > 200 || page.Offset < 0 {
		return nil, 0, FieldError("pagination", "limit must be 1-200 and offset non-negative")
	}
	query := s.db.WithContext(ctx).Model(&models.ScheduledRide{}).Where("passenger_id = ?", userID)
	order := "scheduled_for DESC, id DESC"
	if status == "upcoming" {
		query = query.Where("status IN ?", []string{"scheduled", "dispatching"})
		order = "scheduled_for ASC, id ASC"
	} else if status != "" {
		if !map[string]bool{"scheduled": true, "dispatching": true, "dispatched": true, "failed": true, "cancelled": true}[status] {
			return nil, 0, FieldError("status", "invalid schedule status")
		}
		query = query.Where("status = ?", status)
		if status == "scheduled" || status == "dispatching" {
			order = "scheduled_for ASC, id ASC"
		}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, OpError(503, "Scheduled rides unavailable")
	}
	rows := []models.ScheduledRide{}
	if err := query.Order(order).Limit(page.Limit).Offset(page.Offset).Find(&rows).Error; err != nil {
		return nil, 0, OpError(503, "Scheduled rides unavailable")
	}
	return rows, total, nil
}

func (s *Store) ScheduledRide(ctx context.Context, userID, id string) (*models.ScheduledRide, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, FieldError("id", "valid schedule ID required")
	}
	var row models.ScheduledRide
	if err := s.db.WithContext(ctx).Where("id = ? AND passenger_id = ?", id, userID).First(&row).Error; err != nil {
		return nil, OpError(404, "Scheduled ride not found")
	}
	return &row, nil
}

func (s *Store) CancelScheduledRide(ctx context.Context, userID, id string) (*models.ScheduledRide, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, FieldError("id", "valid schedule ID required")
	}
	var row models.ScheduledRide
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND passenger_id = ?", id, userID).First(&row).Error; err != nil {
			return OpError(404, "Scheduled ride not found")
		}
		if row.Status == "cancelled" {
			return nil
		}
		if row.Status != "scheduled" {
			return OpError(409, "Scheduled ride is already being dispatched")
		}
		row.Status, row.UpdatedAt = "cancelled", s.now()
		return tx.Save(&row).Error
	})
	if err != nil {
		var op *OperationError
		if errors.As(err, &op) {
			return nil, err
		}
		return nil, OpError(503, "Scheduled rides unavailable")
	}
	return &row, nil
}

// DispatchDueScheduledRides claims due schedules, then uses the ordinary ride
// creation path with a stable key. A retry cannot create a second ride.
func (s *Store) DispatchDueScheduledRides(ctx context.Context, cfg *config.Config) error {
	now := s.now()
	rows := []models.ScheduledRide{}
	if err := s.db.WithContext(ctx).Where("(status = ? AND scheduled_for <= ?) OR (status = ? AND claimed_until <= ?)", "scheduled", now, "dispatching", now).Order("scheduled_for ASC, id ASC").Limit(20).Find(&rows).Error; err != nil {
		return OpError(503, "Scheduled dispatch unavailable")
	}
	for _, candidate := range rows {
		claimedUntil := s.now().Add(2 * time.Minute)
		claim := s.db.WithContext(ctx).Model(&models.ScheduledRide{}).Where("id = ? AND ((status = ? AND scheduled_for <= ?) OR (status = ? AND claimed_until <= ?))", candidate.ID, "scheduled", now, "dispatching", now).Updates(map[string]any{"status": "dispatching", "claimed_until": claimedUntil, "attempt_count": gorm.Expr("attempt_count + 1"), "updated_at": s.now()})
		if claim.Error != nil {
			return OpError(503, "Scheduled dispatch unavailable")
		}
		if claim.RowsAffected == 0 {
			continue
		}
		input := models.CreateRideInput{Pickup: models.RidePoint{Lat: candidate.PickupLat, Lng: candidate.PickupLng, Address: candidate.PickupAddress}, Dropoff: models.RidePoint{Lat: candidate.DropoffLat, Lng: candidate.DropoffLng, Address: candidate.DropoffAddress}, VehicleType: candidate.VehicleType}
		distance := RideDistanceMeters(input.Pickup.Lat, input.Pickup.Lng, input.Dropoff.Lat, input.Dropoff.Lng)
		fare, fareErr := EstimatedRideFare(cfg, distance)
		var ride *models.Ride
		var createErr error
		if fareErr == nil {
			ride, createErr = s.CreateRideRequest(ctx, candidate.PassengerID, "schedule:"+candidate.ID, input, distance, fare, cfg.RideSearchRadiusKM, time.Duration(cfg.RideOfferTimeoutSeconds)*time.Second)
		} else {
			createErr = fareErr
		}
		updates := map[string]any{"updated_at": s.now(), "claimed_until": nil}
		if createErr == nil {
			updates["ride_id"] = ride.ID
			if ride.Status == "no_driver_found" {
				updates["status"], updates["failure_code"] = "failed", "no_driver_found"
			} else {
				updates["status"], updates["failure_code"] = "dispatched", ""
			}
		} else {
			var op *OperationError
			permanent := errors.As(createErr, &op) && (op.Status == 409 || op.Status == 400 || op.Status == 403)
			if candidate.AttemptCount+1 >= 3 || permanent {
				updates["status"], updates["failure_code"] = "failed", "dispatch_unavailable"
			} else {
				updates["status"], updates["claimed_until"] = "dispatching", s.now().Add(time.Minute)
			}
		}
		if err := s.db.WithContext(ctx).Model(&models.ScheduledRide{}).Where("id = ? AND status = ?", candidate.ID, "dispatching").Updates(updates).Error; err != nil {
			return OpError(503, "Scheduled dispatch unavailable")
		}
	}
	return nil
}
