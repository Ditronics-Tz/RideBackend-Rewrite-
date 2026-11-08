package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/models"
)

func (s *Store) TransportSettings() (models.TransportSettings, error) {
	setting := models.TransportSettings{ID: 1}
	err := s.db.First(&setting, "id = ?", 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return setting, nil
	}
	return setting, err
}

func (s *Store) SaveTransportSettings(in models.TransportSettings) (models.TransportSettings, error) {
	if in.SharedFee < 500 || in.SharedFee > 1000 || in.HailPercent < 0 || in.HailPercent > 100 {
		return in, FieldError("fees", "Set a shared fee of TZS 500–1000 and a hailing commission of 0–100 percent")
	}
	in.ID = 1
	err := s.db.Transaction(func(tx *gorm.DB) error {
		seed := models.TransportSettings{ID: 1}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}
		current := in.Version
		in.Version++
		res := tx.Model(&seed).Where("version = ?", current).Select("enabled", "shared_fee", "hail_percent", "version").Updates(in)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return OpError(409, "Settings changed. Reload before saving.")
		}
		return nil
	})
	return in, err
}

func validSharedPoint(p models.RidePoint) bool {
	return p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180 && len(strings.TrimSpace(p.Address)) > 0 && len(p.Address) <= 250
}

func sharedCode(secret, seat string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("rida/shared-seat/v1/" + seat))
	return fmt.Sprintf("%06d", binary.BigEndian.Uint64(mac.Sum(nil)[:8])%1000000)
}

func (s *Store) activeSharedPassenger(id string, now int64) (bool, error) {
	var n int64
	err := s.db.Model(&models.SharedSeat{}).Joins("JOIN shared_trips t ON t.id = shared_seats.trip_id").
		Where("passenger_id = ? AND t.status IN ('waiting','in_progress') AND (shared_seats.status = 'confirmed' OR (t.deadline > ? AND shared_seats.status = 'held' AND expires > ?))", id, now, now).Count(&n).Error
	return n > 0, err
}

func (s *Store) expireWaiting(driver string, now int64) error {
	var trips []models.SharedTrip
	if err := s.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("driver_id=? AND status='waiting' AND deadline<=? AND NOT EXISTS (SELECT 1 FROM shared_seats b WHERE b.trip_id=shared_trips.id AND b.status='confirmed')", driver, now).Find(&trips).Error; err != nil {
		return err
	}
	for _, trip := range trips {
		trip.Status = "expired"
		if err := s.db.Save(&trip).Error; err != nil {
			return err
		}
		if err := s.appendTransportEvent(&trip, "", "shared_trip.expired"); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) activeSharedDriver(id string, now int64) (bool, error) {
	var n int64
	err := s.db.Model(&models.SharedTrip{}).Where("driver_id = ? AND (status = 'in_progress' OR (status = 'waiting' AND (deadline > ? OR EXISTS (SELECT 1 FROM shared_seats b WHERE b.trip_id=shared_trips.id AND b.status='confirmed'))))", id, now).Count(&n).Error
	return n > 0, err
}

// Shared and hailed mutations use the same lock order: transport cursor, actor, profile/trip, seat, wallet. The original request key and
// hash are stored in the same transaction, including failed pickup attempts.
func (s *Store) sharedMutation(actor, key string, payload any, apply func(*Store, int64) (string, error, error)) (string, error) {
	if len(key) < 8 || len(key) > 100 {
		return "", FieldError("Idempotency-Key", "Idempotency-Key must contain 8-100 characters")
	}
	hash, err := rideRequestHash(payload)
	if err != nil {
		return "", err
	}
	var tripID string
	var rejected error
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := lockTransportCursor(tx); err != nil {
			return err
		}
		if _, err := lockRideActor(tx, actor); err != nil {
			return err
		}
		var prior models.SharedRequest
		err := tx.First(&prior, "actor = ? AND key = ?", actor, key).Error
		if err == nil {
			if prior.Hash != hash {
				return OpError(409, "This key was used for another request")
			}
			if prior.TripID == models.UnassignedRideDriverID {
				return TransportError(409, "INVALID_PICKUP_CODE", "Pickup code was rejected")
			}
			tripID = prior.TripID
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		work := *s
		work.db = tx
		tripID, rejected, err = apply(&work, s.now().Unix())
		if err != nil {
			return err
		}
		id := tripID
		if rejected != nil {
			id = models.UnassignedRideDriverID
		}
		return tx.Create(&models.SharedRequest{Actor: actor, Key: key, Hash: hash, TripID: id}).Error
	})
	if err != nil {
		return "", err
	}
	return tripID, rejected
}

func (s *Store) PublishShared(actor, key string, in models.SharedPublish) (string, error) {
	if !validSharedPoint(in.Pickup) || !validSharedPoint(in.Dropoff) || RideDistanceMeters(in.Pickup.Lat, in.Pickup.Lng, in.Dropoff.Lat, in.Dropoff.Lng) < 50 {
		return "", FieldError("route", "Select different pickup and destination points")
	}
	if in.Capacity < 1 || in.Capacity > 100 || in.Seats < 1 || in.Seats > in.Capacity || in.Price < 1 || in.Price > MaxMoney/int64(in.Seats) || in.WaitMinutes < 6 || in.WaitMinutes > 180 {
		return "", FieldError("trip", "Check capacity, offered seats, price and wait (6–180 minutes)")
	}
	return s.sharedMutation(actor, key, in, func(w *Store, now int64) (string, error, error) {
		settings, err := w.TransportSettings()
		if err != nil {
			return "", nil, err
		}
		if !settings.Enabled {
			return "", nil, TransportError(409, "SHARING_DISABLED", "Ride sharing is closed")
		}
		var p models.DriverProfile
		if err := w.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, "user_id = ?", actor).Error; err != nil {
			return "", nil, err
		}
		switch strings.ToLower(strings.TrimSpace(p.VehicleType)) {
		case "bajaji", "car", "sedan", "suv", "van", "minivan", "hatchback", "standard", "comfort", "xl":
		default:
			return "", nil, FieldError("vehicle", "An approved bajaji or car is required")
		}
		if err := w.CheckDispatchEligibility("", actor); err != nil {
			return "", nil, err
		}
		if err := w.checkFinancialEligibility(actor); err != nil {
			return "", nil, err
		}
		if err := w.expireWaiting(actor, now); err != nil {
			return "", nil, err
		}
		if busy, err := w.activeSharedDriver(actor, now); err != nil {
			return "", nil, err
		} else if busy {
			return "", nil, OpError(409, "Finish your current shared trip first")
		}
		var active int64
		if err := w.db.Model(&models.Ride{}).Where("driver_id = ? AND status IN ?", actor, rideActiveStatuses).Count(&active).Error; err != nil {
			return "", nil, err
		}
		if active > 0 {
			return "", nil, OpError(409, "Finish your current hailed ride first")
		}
		if p.PassengerSeats < 1 {
			return "", nil, FieldError("capacity", "Register passenger capacity in vehicle setup before publishing")
		}
		if in.Capacity != p.PassengerSeats {
			return "", nil, FieldError("capacity", "Enter the registered passenger capacity or update your vehicle")
		}
		trip := models.SharedTrip{ID: newID(), DriverID: actor, Status: "waiting", Vehicle: p.VehicleType, Pickup: in.Pickup, Dropoff: in.Dropoff, Capacity: in.Capacity, Seats: in.Seats, Price: in.Price, Fee: settings.SharedFee, FeeVersion: settings.Version, Created: now, Deadline: now + int64(in.WaitMinutes)*60}
		if err := w.db.Model(&p).Update("is_online", false).Error; err != nil {
			return "", nil, err
		}
		if err := w.db.Create(&trip).Error; err != nil {
			return "", nil, err
		}
		return trip.ID, nil, w.appendTransportEvent(&trip, actor, "shared_trip.published")
	})
}

func seatState(seat models.SharedSeat, t models.SharedTrip, now int64) models.SharedSeat {
	if (seat.Status == "held" && now >= seat.Expires) || ((seat.Status == "held" || seat.Status == "confirmed") && t.Status == "expired") {
		seat.Status = "expired"
	}
	if (seat.Status == "held" || seat.Status == "confirmed") && t.Status == "cancelled" {
		seat.Status = "cancelled"
	}
	return seat
}
func (s *Store) sharedView(trip models.SharedTrip, actor, secret string, staff bool, now int64) (models.SharedView, error) {
	t := trip
	v := models.SharedView{SharedTrip: t, ServerTime: now}
	var seats []models.SharedSeat
	err := s.db.Where("trip_id = ?", t.ID).Order("expires ASC, id ASC").Find(&seats).Error
	if err != nil {
		return v, err
	}
	if t.Status == "waiting" && now >= t.Deadline {
		confirmed := false
		for _, seat := range seats {
			if seat.Status == "confirmed" {
				confirmed = true
				break
			}
		}
		if !confirmed {
			t.Status = "expired"
			v.SharedTrip = t
		}
	}
	for _, row := range seats {
		seat := seatState(row, t, now)
		switch seat.Status {
		case "held":
			v.Held++
		case "confirmed", "completed":
			v.Confirmed++
		}
		if actor == seat.PassengerID && (v.MySeat == nil || (v.MySeat.Status != "held" && v.MySeat.Status != "confirmed") || seat.Status == "held" || seat.Status == "confirmed") {
			v.MySeat = &seat
			v.PIN = ""
			if seat.Status == "held" {
				v.PIN = sharedCode(secret, seat.ID)
			}
		}
		if staff || actor == t.DriverID {
			if seat.Status == "confirmed" || seat.Status == "held" || seat.Status == "completed" {
				v.Manifest = append(v.Manifest, seat)
			}
		}
	}
	if t.Status == "waiting" && t.Deadline-now >= 300 {
		v.Available = t.Seats - v.Held - v.Confirmed
	}
	if actor == t.DriverID || staff || v.MySeat != nil {
		var a models.TransportAssessment
		if err := s.db.First(&a, "mode='shared' AND trip_id=?", t.ID).Error; err == nil {
			v.Receipt = &a
		} else if !notFound(err) {
			return v, err
		}
	}
	if t.Status == "completed" && v.Receipt != nil {
		v.Confirmed = v.Receipt.Passengers
	}
	var profile models.DriverProfile
	if err := s.db.First(&profile, "user_id=?", t.DriverID).Error; err == nil {
		v.DriverRating = profile.AverageRating
	}
	var driver models.User
	if err := s.db.First(&driver, "id=?", t.DriverID).Error; err == nil {
		v.DriverName = driver.Name
	}
	if staff || actor == t.DriverID {
		v.PassengerNames = map[string]string{}
		if len(v.Manifest) > 0 {
			ids := []string{}
			for _, seat := range v.Manifest {
				ids = append(ids, seat.PassengerID)
			}
			var users []models.User
			if err := s.db.Where("id IN ?", ids).Find(&users).Error; err != nil {
				return v, err
			}
			for _, seat := range v.Manifest {
				for _, u := range users {
					if u.ID == seat.PassengerID {
						v.PassengerNames[seat.ID] = u.Name
					}
				}
			}
		}
	}
	return v, nil
}

func (s *Store) SharedAction(actor, role, tripID, key, action, secret string, in models.SharedAction) (string, error) {
	// The request fingerprint must not retain an offline-verifiable hash of a six-digit PIN.
	digest := hmac.New(sha256.New, []byte(secret))
	digest.Write([]byte(in.PIN))
	payload := []any{tripID, action, in.SeatID, in.Reason, fmt.Sprintf("%x", digest.Sum(nil))}
	return s.sharedMutation(actor, key, payload, func(w *Store, now int64) (string, error, error) {
		var t models.SharedTrip
		if err := w.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&t, "id = ?", tripID).Error; err != nil {
			return "", nil, err
		}
		v, err := w.sharedView(t, actor, secret, false, now)
		if err != nil {
			return "", nil, err
		}
		t = v.SharedTrip
		if action != "reserve" && action != "release" && (role != "driver" || actor != t.DriverID) {
			return "", nil, OpError(404, "Trip not found")
		}
		if (action == "reserve" || action == "release") && role != "passenger" {
			return "", nil, OpError(403, "Passenger account required")
		}
		switch action {
		case "reserve":
			var recent int64
			if err := w.db.Model(&models.SharedSeat{}).Where("passenger_id=? AND created>?", actor, now-60).Count(&recent).Error; err != nil {
				return "", nil, err
			}
			if recent >= 5 {
				return "", nil, TransportError(429, "RESERVATION_RATE_LIMIT", "Wait before reserving another seat")
			}
			settings, err := w.TransportSettings()
			if err != nil {
				return "", nil, err
			}
			if !settings.Enabled || v.Available <= 0 {
				return "", nil, TransportError(409, "SEAT_UNAVAILABLE", "Booking has closed or the trip is full")
			}
			if err := w.CheckDispatchEligibility(actor, t.DriverID); err != nil {
				return "", nil, err
			}
			if busy, err := w.activeSharedPassenger(actor, now); err != nil {
				return "", nil, err
			} else if busy {
				return "", nil, TransportError(409, "COMMITMENT_CONFLICT", "You already have an active seat")
			}
			var count int64
			if err := w.db.Model(&models.Ride{}).Where("passenger_id = ? AND status IN ?", actor, rideActiveStatuses).Count(&count).Error; err != nil {
				return "", nil, err
			}
			if count > 0 {
				return "", nil, TransportError(409, "COMMITMENT_CONFLICT", "Finish your hailed ride first")
			}
			err = w.db.Create(&models.SharedSeat{ID: newID(), TripID: t.ID, PassengerID: actor, Status: "held", Created: now, Expires: now + 300}).Error
			if err != nil {
				return "", nil, err
			}
			if err := w.appendTransportEvent(&t, actor, "seat_reservation.created"); err != nil {
				return "", nil, err
			}
			return t.ID, nil, w.queueAppNotification(w.db.Statement.Context, t.DriverID, "ride", "Shared ride reservation", "A passenger reserved a seat. Open Rida to review your manifest.", map[string]string{"shared_trip_id": t.ID, "status": "held"})
		case "release":
			if v.MySeat == nil || t.Status != "waiting" || (v.MySeat.Status != "held" && v.MySeat.Status != "confirmed") {
				return "", nil, TransportError(409, "NO_ACTIVE_SEAT", "There is no active seat to release")
			}
			if err := w.db.Model(v.MySeat).Update("status", "cancelled").Error; err != nil {
				return "", nil, err
			}
			if err := w.appendTransportEvent(&t, actor, "seat_reservation.cancelled"); err != nil {
				return "", nil, err
			}
			return t.ID, nil, w.queueAppNotification(w.db.Statement.Context, t.DriverID, "ride", "Seat cancelled", "A passenger released a seat on your shared trip.", map[string]string{"shared_trip_id": t.ID, "status": "cancelled"})
		case "verify":
			var seat models.SharedSeat
			if err := w.db.First(&seat, "trip_id = ? AND id = ?", t.ID, in.SeatID).Error; err != nil {
				return "", nil, err
			}
			if t.Status != "waiting" || seatState(seat, t, now).Status != "held" {
				return "", nil, TransportError(409, "HOLD_EXPIRED", "Seat hold has ended")
			}
			if !hmac.Equal([]byte(sharedCode(secret, seat.ID)), []byte(in.PIN)) {
				seat.Failures++
				if seat.Failures >= 5 {
					seat.Status = "released"
				}
				if err := w.db.Save(&seat).Error; err != nil {
					return "", nil, err
				}
				return t.ID, TransportError(409, "INVALID_PICKUP_CODE", "Pickup code was rejected"), w.appendTransportEvent(&t, actor, "seat_reservation.verification_failed")
			}
			if err := w.db.Model(&seat).Updates(map[string]any{"status": "confirmed", "confirmed_at": now}).Error; err != nil {
				return "", nil, err
			}
			if err := w.appendTransportEvent(&t, actor, "seat_reservation.confirmed"); err != nil {
				return "", nil, err
			}
			return t.ID, nil, w.queueAppNotification(w.db.Statement.Context, seat.PassengerID, "ride", "Seat confirmed", "Your driver verified your shared ride pickup.", map[string]string{"shared_trip_id": t.ID, "status": "confirmed"})
		case "start":
			if t.Status != "waiting" || v.Held != 0 || v.Confirmed == 0 {
				return "", nil, TransportError(409, "DEPARTURE_NOT_READY", "Confirm passengers and resolve live holds before departure")
			}
			t.Status = "in_progress"
		case "cancel":
			if t.Status != "waiting" {
				return "", nil, TransportError(409, "TRIP_NOT_WAITING", "Only waiting trips can be cancelled")
			}
			t.Status = "cancelled"
			if err := w.db.Model(&models.SharedSeat{}).Where("trip_id=? AND status IN ?", t.ID, []string{"held", "confirmed"}).Update("status", "cancelled").Error; err != nil {
				return "", nil, err
			}
		case "complete":
			if t.Status != "in_progress" {
				return "", nil, TransportError(409, "TRIP_NOT_STARTED", "Start the trip before completing it")
			}
			if err := w.assessTransport("shared", t.ID, actor, t.Price*int64(v.Confirmed), t.Fee, v.Confirmed, t.FeeVersion); err != nil {
				return "", nil, err
			}
			if err := w.db.Model(&models.SharedSeat{}).Where("trip_id=? AND status='confirmed'", t.ID).Updates(map[string]any{"status": "completed", "completed_at": now}).Error; err != nil {
				return "", nil, err
			}
			t.Status = "completed"
			t.Completed = now
		default:
			return "", nil, OpError(404, "Unknown trip action")
		}
		if err := w.appendTransportEvent(&t, actor, "shared_trip."+t.Status, in.Reason); err != nil {
			return "", nil, err
		}
		if err := w.db.Save(&t).Error; err != nil {
			return "", nil, err
		}
		for _, seat := range v.Manifest {
			if err := w.queueAppNotification(w.db.Statement.Context, seat.PassengerID, "ride", "Shared trip update", "Your shared trip is "+strings.ReplaceAll(t.Status, "_", " ")+".", map[string]string{"shared_trip_id": t.ID, "status": t.Status}); err != nil {
				return "", nil, err
			}
		}
		return t.ID, nil, nil
	})
}

func (s *Store) SharedDetail(id, actor, secret string) (models.SharedView, error) {
	var t models.SharedTrip
	if err := s.db.First(&t, "id = ?", id).Error; err != nil {
		return models.SharedView{}, err
	}
	v, err := s.sharedView(t, actor, secret, false, s.now().Unix())
	if err == nil && actor != t.DriverID && v.MySeat == nil && v.Status != "waiting" {
		return models.SharedView{}, OpError(404, "Trip not found")
	}
	return v, err
}

func (s *Store) SharedList(actor, role, scope, secret string, lat, lng, radius float64, limit, offset int) ([]models.SharedView, error) {
	now := s.now().Unix()
	out := []models.SharedView{}
	q := s.db.Model(&models.SharedTrip{})
	switch scope {
	case "mine":
		if role == "driver" {
			q = q.Where("driver_id = ?", actor)
		} else {
			q = q.Where("id IN (?)", s.db.Model(&models.SharedSeat{}).Select("trip_id").Where("passenger_id = ?", actor))
		}
	case "staff":
		if role != "staff" {
			return nil, OpError(403, "Staff only")
		}
	case "nearby":
		if role != "passenger" || math.IsNaN(radius) || radius <= 0 || radius > 50 || !validSharedPoint(models.RidePoint{Lat: lat, Lng: lng, Address: "area"}) {
			return nil, FieldError("area", "Choose a pickup area within 50 km")
		}
		setting, err := s.TransportSettings()
		if err != nil || !setting.Enabled {
			return out, err
		}
		q = q.Where("EXISTS (SELECT 1 FROM users u JOIN driver_profiles p ON p.user_id=u.id WHERE u.id=shared_trips.driver_id AND u.is_active=true AND u.status='active' AND u.deleted_at IS NULL AND p.verification_status='approved')")
		q = q.Where("status = 'waiting' AND deadline >= ? AND pickup_lat BETWEEN ? AND ?", now+300, lat-radius/110, lat+radius/110).
			Where("seats > (SELECT count(*) FROM shared_seats b WHERE b.trip_id=shared_trips.id AND (b.status='confirmed' OR (b.status='held' AND b.expires>?)))", now).
			Where("NOT EXISTS (SELECT 1 FROM user_blocks b WHERE (b.blocker_id=? AND b.blocked_id=shared_trips.driver_id) OR (b.blocked_id=? AND b.blocker_id=shared_trips.driver_id))", actor, actor)
		if s.db.Dialector.Name() == "postgres" {
			q = q.Where("6371 * acos(least(1.0,greatest(-1.0,sin(radians(pickup_lat))*sin(radians(?))+cos(radians(pickup_lat))*cos(radians(?))*cos(radians(pickup_lng-?))))) <= ?", lat, lat, lng, radius)
		}
	default:
		return nil, FieldError("scope", "Unknown trip list")
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, FieldError("page", "Invalid page")
	}
	var trips []models.SharedTrip
	if scope != "nearby" || s.db.Dialector.Name() == "postgres" {
		q = q.Limit(limit).Offset(offset)
	}
	if err := q.Order("created DESC, id ASC").Find(&trips).Error; err != nil {
		return nil, err
	}
	for _, t := range trips {
		if scope == "nearby" && s.db.Dialector.Name() != "postgres" {
			if RideDistanceMeters(lat, lng, t.Pickup.Lat, t.Pickup.Lng) > radius*1000 {
				continue
			}
			if offset > 0 {
				offset--
				continue
			}
		}
		v, err := s.sharedView(t, actor, secret, scope == "staff", now)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (s *Store) ActiveDriverHail(actor string) (*models.Ride, error) {
	var r models.Ride
	err := s.db.Where("driver_id = ? AND status IN ?", actor, []string{"assigned", "driver_arrived", "in_progress"}).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &r, err
}

func (s *Store) HailFee(ride *models.Ride) (int64, error) {
	if ride.AssessedFee != nil {
		return *ride.AssessedFee, nil
	}
	var payment models.PaymentTransaction
	err := s.db.Where("ride_id=? AND status = ?", ride.ID, "succeeded").First(&payment).Error
	if err == nil {
		return payment.Commission, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	return s.rideCommission(ride)
}

// DriverPeriodEarnings counts completed trips once. New hails use their assessed
// commission, legacy hails use their settled payment, and shared cash fares use
// verified passengers at completion.
func (s *Store) DriverPeriodEarnings(driver string, period string) (map[string]any, error) {
	days, ok := map[string]int{"today": 1, "week": 7, "month": 30}[period]
	if !ok {
		return nil, FieldError("period", "Use today, week or month")
	}
	now := s.now()
	since := now.AddDate(0, 0, -days)
	if period == "today" {
		local := now.In(time.FixedZone("EAT", 3*3600))
		since = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	}
	type totals struct {
		Gross    int64
		Fee      int64
		Trips    int64
		Distance int64
	}
	var newHails, oldHails, shared totals
	if err := s.db.Model(&models.Ride{}).Where("driver_id=? AND status='completed' AND completed_at>=? AND assessed_fee IS NOT NULL", driver, since).
		Select("coalesce(sum(fare),0) gross,coalesce(sum(assessed_fee),0) fee,count(*) trips,coalesce(round(sum(expected_distance)),0) distance").Scan(&newHails).Error; err != nil {
		return nil, err
	}
	if err := s.db.Table("payment_transactions p").Joins("JOIN rides r ON r.id=p.ride_id").Where("r.driver_id=? AND r.assessed_fee IS NULL AND p.status='succeeded' AND p.created_at>=?", driver, since).
		Select("coalesce(sum(p.fare),0) gross,coalesce(sum(p.commission),0) fee,count(*) trips").Scan(&oldHails).Error; err != nil {
		return nil, err
	}
	if err := s.db.Model(&models.SharedTrip{}).Where("driver_id=? AND status='completed' AND completed>=?", driver, since.Unix()).
		Select("coalesce(sum(price * (select count(*) from shared_seats b where b.trip_id=shared_trips.id and b.status IN ('confirmed','completed'))),0) gross,coalesce(sum(fee),0) fee,count(*) trips").Scan(&shared).Error; err != nil {
		return nil, err
	}
	gross := newHails.Gross + oldHails.Gross + shared.Gross
	fee := newHails.Fee + oldHails.Fee + shared.Fee
	return map[string]any{"period": period, "gross_tzs": gross, "commission_tzs": fee, "net_tzs": gross - fee, "trips": newHails.Trips + oldHails.Trips + shared.Trips, "distance_meters": newHails.Distance}, nil
}

func (s *Store) StaffCancelShared(actor *models.StaffUser, id, reason string) error {
	reason = strings.TrimSpace(reason)
	if actor == nil || actor.Role != models.StaffRoleAdmin {
		return OpError(403, "Administrator required")
	}
	if reason == "" || len([]rune(reason)) > 250 {
		return FieldError("reason", "Supply a reason of up to 250 characters")
	}
	return s.Transaction(func(w *Store) error {
		if err := lockTransportCursor(w.db); err != nil {
			return err
		}
		var trip models.SharedTrip
		if err := w.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&trip, "id=?", id).Error; err != nil {
			return err
		}
		if trip.Status == "cancelled" {
			return nil
		}
		v, err := w.sharedView(trip, "", "", true, w.now().Unix())
		if err != nil {
			return err
		}
		if v.Status != "waiting" {
			return OpError(409, "Only a waiting shared trip can be cancelled")
		}
		trip.Status = "cancelled"
		if err := w.appendTransportEvent(&trip, actor.ID, "shared_trip.cancelled", reason); err != nil {
			return err
		}
		if err := w.db.Model(&models.SharedSeat{}).Where("trip_id=? AND status IN ?", id, []string{"held", "confirmed"}).Update("status", "cancelled").Error; err != nil {
			return err
		}
		if err := w.db.Model(&trip).Update("status", "cancelled").Error; err != nil {
			return err
		}
		for _, seat := range v.Manifest {
			if err := w.queueAppNotification(w.db.Statement.Context, seat.PassengerID, "ride", "Shared trip cancelled", "Rida cancelled the shared trip and released your seat.", map[string]string{"shared_trip_id": id, "status": "cancelled"}); err != nil {
				return err
			}
		}
		return w.CreateAuditLog(actor.ID, "shared_trip_cancelled", "shared_trip", id, "", map[string]string{"reason": reason})
	})
}
