package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/models"
)

var rideActiveStatuses = []string{"requested", "searching", "offered", "assigned", "driver_arrived", "in_progress"}

func RideDistanceMeters(aLat, aLng, bLat, bLng float64) float64 {
	const earthRadius = 6371000.0
	lat1, lat2 := aLat*math.Pi/180, bLat*math.Pi/180
	dLat, dLng := (bLat-aLat)*math.Pi/180, (bLng-aLng)*math.Pi/180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	if h > 1 {
		h = 1
	}
	return earthRadius * 2 * math.Atan2(math.Sqrt(h), math.Sqrt(1-h))
}

func rideRequestHash(value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", FieldError("request", "invalid ride request")
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func checkRideIdempotency(tx *gorm.DB, actor, key, operation, hash string) (*models.Ride, bool, error) {
	if len(key) < 8 || len(key) > 100 {
		return nil, false, FieldError("Idempotency-Key", "Idempotency-Key must contain 8-100 characters")
	}
	var prior models.RideIdempotencyKey
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&prior, "actor_id = ? AND key = ?", actor, key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if prior.Operation != operation || prior.RequestHash != hash {
		return nil, false, OpError(409, "Idempotency-Key was already used for another request")
	}
	if prior.RideID == models.UnassignedRideDriverID {
		return &models.Ride{}, true, nil
	}
	var ride models.Ride
	if err := tx.First(&ride, "id = ?", prior.RideID).Error; err != nil {
		return nil, false, err
	}
	return &ride, true, nil
}

func saveRideIdempotency(tx *gorm.DB, actor, key, operation, hash, rideID string, now time.Time) error {
	return tx.Create(&models.RideIdempotencyKey{ActorID: actor, Key: key, Operation: operation, RequestHash: hash, RideID: rideID, CreatedAt: now}).Error
}

func appendRideEvent(tx *gorm.DB, rideID, kind string, actor *string, actorType string, now time.Time, meta map[string]any) error {
	encoded := ""
	if len(meta) > 0 {
		data, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		encoded = string(data)
	}
	return tx.Create(&models.RideEvent{ID: newID(), RideID: rideID, Type: kind, ActorID: actor, ActorType: actorType, At: now, Meta: encoded}).Error
}

func lockRideActor(tx *gorm.DB, id string) (*models.User, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, OpError(401, "Invalid account")
	}
	var user models.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, OpError(401, "Invalid account")
		}
		return nil, err
	}
	if !user.IsActive || user.Status != "active" || user.DeletedAt != nil {
		return nil, OpError(403, "Active account required")
	}
	return &user, nil
}

func (s *Store) CreateRideRequest(ctx context.Context, passengerID, key string, input models.CreateRideInput, distanceMeters float64, estimatedFare int64, radiusKM int, offerTimeout time.Duration) (*models.Ride, error) {
	hash, err := rideRequestHash(input)
	if err != nil {
		return nil, err
	}
	now := s.now()
	var result models.Ride
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := lockRideActor(tx, passengerID)
		if err != nil {
			return err
		}
		if user.Role != models.RolePassenger {
			return OpError(403, "Passenger account required")
		}
		if prior, replay, err := checkRideIdempotency(tx, passengerID, key, "create", hash); err != nil {
			return err
		} else if replay {
			result = *prior
			return nil
		}
		var active int64
		if err := tx.Model(&models.Ride{}).Where("passenger_id = ? AND status IN ?", passengerID, rideActiveStatuses).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return OpError(409, "Passenger already has an active ride")
		}
		result = models.Ride{ID: newID(), PassengerID: passengerID, DriverID: models.UnassignedRideDriverID, Status: "searching", EstimatedFare: estimatedFare, VehicleType: input.VehicleType, Pickup: input.Pickup.Address, Dropoff: input.Dropoff.Address, PickupLat: input.Pickup.Lat, PickupLng: input.Pickup.Lng, DropoffLat: input.Dropoff.Lat, DropoffLng: input.Dropoff.Lng, ExpectedDistance: distanceMeters, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		actor := passengerID
		if err := appendRideEvent(tx, result.ID, "requested", &actor, "passenger", now, nil); err != nil {
			return err
		}
		if err := appendRideEvent(tx, result.ID, "searching", nil, "system", now, nil); err != nil {
			return err
		}
		if err := s.offerNextDriver(tx, &result, radiusKM, offerTimeout, now); err != nil {
			return err
		}
		return saveRideIdempotency(tx, passengerID, key, "create", hash, result.ID, now)
	})
	if err != nil {
		return nil, rideOperationError(err)
	}
	return &result, nil
}

func rideOperationError(err error) error {
	if err == nil {
		return nil
	}
	var field *OperationError
	if errors.As(err, &field) {
		return err
	}
	return OpError(503, "Ride service temporarily unavailable")
}

func (s *Store) offerNextDriver(tx *gorm.DB, ride *models.Ride, radiusKM int, timeout time.Duration, now time.Time) error {
	// Keep eligibility reads in the same transaction as dispatch. Besides
	// avoiding a second connection while rows are locked, this makes the
	// candidate checks use the transaction's consistent view of the ride.
	scopedStore := *s
	scopedStore.db = tx
	var profiles []models.DriverProfile
	if err := tx.Where("is_online = ? AND verification_status = ?", true, models.Approved).Order("user_id ASC").Find(&profiles).Error; err != nil {
		return err
	}
	var previous []string
	if err := tx.Model(&models.RideEvent{}).Where("ride_id = ? AND type IN ? AND actor_id IS NOT NULL", ride.ID, []string{"offer_declined", "offer_expired"}).Pluck("actor_id", &previous).Error; err != nil {
		return err
	}
	excluded := map[string]bool{}
	for _, id := range previous {
		excluded[id] = true
	}
	type candidate struct {
		profile models.DriverProfile
		point   models.DriverLocation
		dist    float64
	}
	candidates := []candidate{}
	for _, profile := range profiles {
		if excluded[profile.UserID] || ride.VehicleType != "" && profile.VehicleType != ride.VehicleType {
			continue
		}
		var currentProfile models.DriverProfile
		if err := tx.First(&currentProfile, "user_id = ?", profile.UserID).Error; err != nil || !currentProfile.IsOnline || currentProfile.VerificationStatus != models.Approved || ride.VehicleType != "" && currentProfile.VehicleType != ride.VehicleType {
			continue
		}
		var location models.DriverLocation
		if err := tx.First(&location, "driver_id = ? AND updated_at >= ?", profile.UserID, now.Add(-5*time.Minute)).Error; err != nil {
			continue
		}
		distance := RideDistanceMeters(ride.PickupLat, ride.PickupLng, location.Lat, location.Lng)
		if distance > float64(radiusKM)*1000 {
			continue
		}
		if scopedStore.CheckDispatchEligibility(ride.PassengerID, profile.UserID) != nil {
			continue
		}
		var active int64
		if err := tx.Model(&models.Ride{}).Where("driver_id = ? AND status IN ?", profile.UserID, rideActiveStatuses).Count(&active).Error; err != nil || active > 0 {
			continue
		}
		candidates = append(candidates, candidate{profile: profile, point: location, dist: distance})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].dist == candidates[j].dist {
			return candidates[i].profile.UserID < candidates[j].profile.UserID
		}
		return candidates[i].dist < candidates[j].dist
	})
	for _, chosen := range candidates {
		var lockedProfile models.DriverProfile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedProfile, "user_id = ?", chosen.profile.UserID).Error; err != nil || !lockedProfile.IsOnline || lockedProfile.VerificationStatus != models.Approved {
			continue
		}
		var active int64
		if err := tx.Model(&models.Ride{}).Where("driver_id = ? AND status IN ?", chosen.profile.UserID, rideActiveStatuses).Count(&active).Error; err != nil {
			return err
		}
		if active != 0 || scopedStore.CheckDispatchEligibility(ride.PassengerID, chosen.profile.UserID) != nil {
			continue
		}
		expires := now.Add(timeout)
		ride.DriverID = chosen.profile.UserID
		ride.Status = "offered"
		ride.OfferExpiresAt = &expires
		ride.UpdatedAt = now
		if err := tx.Save(ride).Error; err != nil {
			return err
		}
		return appendRideEvent(tx, ride.ID, "offered", &chosen.profile.UserID, "driver", now, map[string]any{"expires_at": expires})
	}
	ride.DriverID = models.UnassignedRideDriverID
	ride.Status = "no_driver_found"
	ride.OfferExpiresAt = nil
	ride.UpdatedAt = now
	if err := tx.Save(ride).Error; err != nil {
		return err
	}
	return appendRideEvent(tx, ride.ID, "no_driver_found", nil, "system", now, nil)
}

func (s *Store) AdvanceExpiredRideOffers(ctx context.Context, radiusKM int, timeout time.Duration) error {
	now := s.now()
	return rideOperationError(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rides []models.Ride
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("status = ? AND offer_expires_at <= ?", "offered", now).Order("offer_expires_at ASC, id ASC").Limit(100).Find(&rides).Error; err != nil {
			return err
		}
		for i := range rides {
			ride := &rides[i]
			oldDriver := ride.DriverID
			if err := appendRideEvent(tx, ride.ID, "offer_expired", &oldDriver, "driver", now, nil); err != nil {
				return err
			}
			ride.DriverID = models.UnassignedRideDriverID
			ride.Status = "searching"
			ride.OfferExpiresAt = nil
			ride.UpdatedAt = now
			if now.Sub(ride.CreatedAt) >= 15*time.Minute {
				ride.Status = "expired"
				if err := tx.Save(ride).Error; err != nil {
					return err
				}
				if err := appendRideEvent(tx, ride.ID, "expired", nil, "system", now, nil); err != nil {
					return err
				}
				continue
			}
			if err := tx.Save(ride).Error; err != nil {
				return err
			}
			if err := s.offerNextDriver(tx, ride, radiusKM, timeout, now); err != nil {
				return err
			}
		}
		return nil
	}))
}

func (s *Store) DriverRideOffers(ctx context.Context, driverID string, radiusKM int, timeout time.Duration) ([]models.Ride, error) {
	if err := s.AdvanceExpiredRideOffers(ctx, radiusKM, timeout); err != nil {
		return nil, err
	}
	offers := []models.Ride{}
	if err := s.db.WithContext(ctx).Where("driver_id = ? AND status = ? AND offer_expires_at > ?", driverID, "offered", s.now()).Order("created_at ASC, id ASC").Find(&offers).Error; err != nil {
		return nil, OpError(503, "Ride service temporarily unavailable")
	}
	return offers, nil
}

func (s *Store) AcceptRide(ctx context.Context, driverID, rideID, key string) (*models.Ride, error) {
	return s.rideDriverAction(ctx, driverID, rideID, key, "accept", "assigned", 10, 30*time.Second)
}
func (s *Store) DeclineRide(ctx context.Context, driverID, rideID, key string, radiusKM int, timeout time.Duration) (*models.Ride, error) {
	return s.rideDriverAction(ctx, driverID, rideID, key, "decline", "searching", radiusKM, timeout)
}

func (s *Store) rideDriverAction(ctx context.Context, driverID, rideID, key, action, target string, radiusKM int, timeout time.Duration) (*models.Ride, error) {
	hash, err := rideRequestHash(map[string]string{"ride_id": rideID})
	if err != nil {
		return nil, err
	}
	now := s.now()
	var result models.Ride
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := lockRideActor(tx, driverID)
		if err != nil {
			return err
		}
		if user.Role != models.RoleDriver {
			return OpError(403, "Driver account required")
		}
		if prior, replay, err := checkRideIdempotency(tx, driverID, key, action, hash); err != nil {
			return err
		} else if replay {
			result = *prior
			return nil
		}
		if _, err := uuid.Parse(rideID); err != nil {
			return FieldError("id", "valid ride UUID required")
		}
		var ride models.Ride
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&ride, "id = ?", rideID).Error; err != nil {
			return OpError(404, "Ride not found")
		}
		if ride.Status != "offered" || ride.DriverID != driverID || ride.OfferExpiresAt == nil || !ride.OfferExpiresAt.After(now) {
			return OpError(409, "Ride offer is no longer available")
		}
		var currentProfile models.DriverProfile
		if err := tx.First(&currentProfile, "user_id = ?", driverID).Error; err != nil || !currentProfile.IsOnline || ride.VehicleType != "" && currentProfile.VehicleType != ride.VehicleType {
			return OpError(403, "Driver is not currently eligible for this vehicle type")
		}
		scopedStore := *s
		scopedStore.db = tx
		if err := scopedStore.CheckDispatchEligibility(ride.PassengerID, driverID); err != nil {
			return OpError(403, "Ride participants are not eligible")
		}
		if action == "decline" {
			driver := driverID
			if err := appendRideEvent(tx, ride.ID, "offer_declined", &driver, "driver", now, nil); err != nil {
				return err
			}
			ride.Status = target
			ride.DriverID = models.UnassignedRideDriverID
			ride.OfferExpiresAt = nil
			ride.UpdatedAt = now
			if err := tx.Save(&ride).Error; err != nil {
				return err
			}
			if err := appendRideEvent(tx, ride.ID, "searching", nil, "system", now, nil); err != nil {
				return err
			}
			if err := s.offerNextDriver(tx, &ride, radiusKM, timeout, now); err != nil {
				return err
			}
		} else {
			accepted := now
			ride.Status = target
			ride.AcceptedAt = &accepted
			ride.OfferExpiresAt = nil
			ride.UpdatedAt = now
			if err := tx.Save(&ride).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.DriverProfile{}).Where("user_id = ?", driverID).Update("is_online", false).Error; err != nil {
				return err
			}
			if err := appendRideEvent(tx, ride.ID, "assigned", &driverID, "driver", now, nil); err != nil {
				return err
			}
			if err := scopedStore.queueRideNotification(ctx, ride.PassengerID, ride.ID, "assigned"); err != nil {
				return err
			}
		}
		if err := saveRideIdempotency(tx, driverID, key, action, hash, ride.ID, now); err != nil {
			return err
		}
		result = ride
		return nil
	})
	if err != nil {
		return nil, rideOperationError(err)
	}
	return &result, nil
}

func (s *Store) CancelRide(ctx context.Context, actorID, rideID, key, reason string) (*models.Ride, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 250 {
		return nil, FieldError("reason", "a cancellation reason of 1-250 characters is required")
	}
	hash, _ := rideRequestHash(map[string]string{"ride_id": rideID, "reason": reason})
	now := s.now()
	var result models.Ride
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := lockRideActor(tx, actorID)
		if err != nil {
			return err
		}
		if prior, replay, err := checkRideIdempotency(tx, actorID, key, "cancel", hash); err != nil {
			return err
		} else if replay {
			result = *prior
			return nil
		}
		var ride models.Ride
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&ride, "id = ? AND (passenger_id = ? OR driver_id = ?)", rideID, actorID, actorID).Error; err != nil {
			return OpError(404, "Ride not found")
		}
		status := ""
		if user.Role == models.RolePassenger && ride.PassengerID == actorID && ride.Status != "in_progress" {
			status = "cancelled_by_passenger"
		} else if user.Role == models.RoleDriver && ride.DriverID == actorID && (ride.Status == "offered" || ride.Status == "assigned" || ride.Status == "driver_arrived") {
			status = "cancelled_by_driver"
		}
		if status == "" {
			return OpError(409, "Ride cannot be cancelled in its current state")
		}
		if ride.Status == "completed" || ride.Status == "cancelled_by_passenger" || ride.Status == "cancelled_by_driver" || ride.Status == "cancelled_by_admin" || ride.Status == "expired" || ride.Status == "no_driver_found" {
			return OpError(409, "Ride is already closed")
		}
		actor := actorID
		ride.Status, ride.CancelReason, ride.CancelledBy, ride.CancelledAt, ride.OfferExpiresAt, ride.UpdatedAt = status, reason, &actor, &now, nil, now
		if err := tx.Save(&ride).Error; err != nil {
			return err
		}
		if err := appendRideEvent(tx, ride.ID, status, &actor, string(user.Role), now, map[string]any{"reason": reason}); err != nil {
			return err
		}
		if status == "cancelled_by_driver" {
			scopedStore := *s
			scopedStore.db = tx
			if err := scopedStore.queueRideNotification(ctx, ride.PassengerID, ride.ID, status); err != nil {
				return err
			}
		}
		if err := saveRideIdempotency(tx, actorID, key, "cancel", hash, ride.ID, now); err != nil {
			return err
		}
		result = ride
		return nil
	})
	if err != nil {
		return nil, rideOperationError(err)
	}
	return &result, nil
}

func (s *Store) TransitionRide(ctx context.Context, driverID, rideID, key, action string, finalFare int64) (*models.Ride, error) {
	if action != "arrived" && action != "start" && action != "complete" {
		return nil, FieldError("action", "unsupported ride transition")
	}
	hash, _ := rideRequestHash(map[string]any{"ride_id": rideID, "action": action})
	now := s.now()
	var result models.Ride
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := lockRideActor(tx, driverID)
		if err != nil {
			return err
		}
		if user.Role != models.RoleDriver {
			return OpError(403, "Driver account required")
		}
		if prior, replay, err := checkRideIdempotency(tx, driverID, key, action, hash); err != nil {
			return err
		} else if replay {
			result = *prior
			return nil
		}
		var ride models.Ride
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&ride, "id = ? AND driver_id = ?", rideID, driverID).Error; err != nil {
			return OpError(404, "Ride not found")
		}
		target := map[string]string{"arrived": "driver_arrived", "start": "in_progress", "complete": "completed"}[action]
		legal := action == "arrived" && ride.Status == "assigned" || action == "start" && ride.Status == "driver_arrived" || action == "complete" && ride.Status == "in_progress"
		if !legal {
			return OpError(409, "Ride transition is not allowed from its current state")
		}
		if action == "start" {
			started := now
			ride.StartedAt = &started
		}
		if action == "complete" {
			if finalFare <= 0 || finalFare > MaxMoney {
				return FieldError("final_fare", "server-calculated final fare is invalid")
			}
			ride.Fare = finalFare
			completed := now
			ride.CompletedAt = &completed
		}
		ride.Status = target
		ride.UpdatedAt = now
		if err := tx.Save(&ride).Error; err != nil {
			return err
		}
		if err := appendRideEvent(tx, ride.ID, target, &driverID, "driver", now, nil); err != nil {
			return err
		}
		scopedStore := *s
		scopedStore.db = tx
		if err := scopedStore.queueRideNotification(ctx, ride.PassengerID, ride.ID, target); err != nil {
			return err
		}
		if err := saveRideIdempotency(tx, driverID, key, action, hash, ride.ID, now); err != nil {
			return err
		}
		result = ride
		return nil
	})
	if err != nil {
		return nil, rideOperationError(err)
	}
	return &result, nil
}

func (s *Store) SetDriverLocation(ctx context.Context, driverID, key string, lat, lng, accuracy float64) error {
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 || accuracy < 0 || accuracy > 10000 {
		return FieldError("location", "latitude, longitude or accuracy is outside the supported range")
	}
	hash, _ := rideRequestHash(map[string]float64{"lat": lat, "lng": lng, "accuracy": accuracy})
	now := s.now()
	return rideOperationError(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := lockRideActor(tx, driverID)
		if err != nil {
			return err
		}
		if user.Role != models.RoleDriver {
			return OpError(403, "Driver account required")
		}
		if prior, replay, err := checkRideIdempotency(tx, driverID, key, "location", hash); err != nil {
			return err
		} else if replay {
			_ = prior
			return nil
		}
		var profile models.DriverProfile
		if err := tx.First(&profile, "user_id = ?", driverID).Error; err != nil || !profile.IsOnline {
			return OpError(403, "Driver must be eligible and online to update location")
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "driver_id"}}, DoUpdates: clause.AssignmentColumns([]string{"lat", "lng", "accuracy", "updated_at"})}).Create(&models.DriverLocation{DriverID: driverID, Lat: lat, Lng: lng, Accuracy: accuracy, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return saveRideIdempotency(tx, driverID, key, "location", hash, models.UnassignedRideDriverID, now)
	}))
}

func (s *Store) SetDriverAvailability(ctx context.Context, driverID, key string, online bool) error {
	hash, _ := rideRequestHash(map[string]bool{"online": online})
	now := s.now()
	if online {
		if err := s.ExpireDriverDocuments(now, driverID); err != nil {
			return rideOperationError(err)
		}
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := lockRideActor(tx, driverID)
		if err != nil {
			return err
		}
		if user.Role != models.RoleDriver {
			return OpError(403, "Driver account required")
		}
		if _, replay, err := checkRideIdempotency(tx, driverID, key, "availability", hash); err != nil {
			return err
		} else if replay {
			return nil
		}
		if online {
			scopedStore := *s
			scopedStore.db = tx
			if err := scopedStore.CheckDispatchEligibility("", driverID); err != nil {
				return OpError(403, "Driver is not currently eligible to go online")
			}
		}
		var profile models.DriverProfile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&profile, "user_id = ?", driverID).Error; err != nil {
			return OpError(403, "Driver profile is unavailable")
		}
		if online {
			var active int64
			if err := tx.Model(&models.Ride{}).Where("driver_id = ? AND status IN ?", driverID, rideActiveStatuses).Count(&active).Error; err != nil {
				return err
			}
			if active > 0 {
				return OpError(409, "Driver cannot go online while a ride is active")
			}
		}
		if err := tx.Model(&profile).Update("is_online", online).Error; err != nil {
			return err
		}
		return saveRideIdempotency(tx, driverID, key, "availability", hash, models.UnassignedRideDriverID, now)
	})
	return rideOperationError(err)
}

func (s *Store) GetRideForParticipant(ctx context.Context, rideID, userID string) (*models.Ride, error) {
	var ride models.Ride
	if err := s.db.WithContext(ctx).Where("id = ? AND (passenger_id = ? OR driver_id = ?)", rideID, userID, userID).First(&ride).Error; err != nil {
		return nil, OpError(404, "Ride not found")
	}
	return &ride, nil
}

// CurrentRideDriverLocation is available only to the active ride's parties.
// Old points and ended rides return no location, so a mobile pin cannot linger.
func (s *Store) CurrentRideDriverLocation(ctx context.Context, rideID, userID string) (*models.DriverLocation, error) {
	ride, err := s.GetRideForParticipant(ctx, rideID, userID)
	if err != nil {
		return nil, err
	}
	if ride.DriverID == models.UnassignedRideDriverID || ride.Status != "assigned" && ride.Status != "driver_arrived" && ride.Status != "in_progress" {
		return nil, nil
	}
	var point models.DriverLocation
	err = s.db.WithContext(ctx).Where("driver_id = ? AND updated_at >= ?", ride.DriverID, s.now().Add(-2*time.Minute)).First(&point).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, OpError(503, "Driver location unavailable")
	}
	return &point, nil
}

func (s *Store) ListRidesForParticipant(ctx context.Context, userID, status string, from, to *time.Time, page Page) ([]models.Ride, int64, error) {
	if page.Limit == 0 {
		page.Limit = 50
	}
	if page.Limit < 1 || page.Limit > 200 || page.Offset < 0 {
		return nil, 0, FieldError("pagination", "limit must be 1-200 and offset non-negative")
	}
	q := s.db.WithContext(ctx).Model(&models.Ride{}).Where("(passenger_id = ? OR driver_id = ?)", userID, userID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if from != nil {
		q = q.Where("created_at >= ?", *from)
	}
	if to != nil {
		q = q.Where("created_at < ?", *to)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, OpError(503, "Ride service temporarily unavailable")
	}
	rides := []models.Ride{}
	if err := q.Order("created_at DESC, id DESC").Limit(page.Limit).Offset(page.Offset).Find(&rides).Error; err != nil {
		return nil, 0, OpError(503, "Ride service temporarily unavailable")
	}
	return rides, total, nil
}

func (s *Store) RideEvents(ctx context.Context, rideID string) ([]models.RideEvent, error) {
	events := []models.RideEvent{}
	if err := s.db.WithContext(ctx).Where("ride_id = ?", rideID).Order("at ASC, id ASC").Find(&events).Error; err != nil {
		return nil, OpError(503, "Ride service temporarily unavailable")
	}
	return events, nil
}
