package store

import (
	"crypto/sha256"
	"encoding/hex"
	"gorm.io/gorm/clause"
	"ride-backend/models"
	"strings"
	"time"
)

func (s *Store) ObserveDevice(user, device, platform, ip string) error {
	if device == "" {
		return nil
	}
	if len(device) > 200 {
		return FieldError("X-Device-ID", "device ID too long")
	}
	if s.Policy == nil || s.Policy.DeviceHashPepper == "" {
		return nil
	}
	if platform != "android" && platform != "ios" && platform != "web" {
		platform = "unknown"
	}
	sum := sha256.Sum256([]byte(s.Policy.DeviceHashPepper + "\x00" + device))
	hash := hex.EncodeToString(sum[:])
	now := s.now()
	return s.Transaction(func(tx *Store) error {
		var previous models.UserDevice
		err := tx.db.First(&previous, "user_id = ? AND device_hash = ?", user, hash).Error
		if err == nil && now.Sub(previous.LastSeen) < time.Minute {
			return nil
		}
		if err != nil && !notFound(err) {
			return err
		}
		row := models.UserDevice{ID: newID(), UserID: user, DeviceHash: hash, Platform: platform, LastIP: ip, FirstSeen: now, LastSeen: now, CreatedAt: now}
		if err := tx.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "device_hash"}}, DoUpdates: clause.Assignments(map[string]any{"last_seen": now, "last_ip": ip, "platform": platform})}).Create(&row).Error; err != nil {
			return err
		}
		return tx.QueueFraudEvent(user, "login", nil, "device:"+user+":"+hash+":"+now.Format("200601021504"))
	})
}
func (s *Store) QueueFraudEvent(user, kind string, ride *string, key string) error {
	if key == "" {
		key = newID()
	}
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.FraudEvent{ID: newID(), UserID: user, Type: kind, RideID: ride, Status: "pending", DedupeKey: key, CreatedAt: s.now()}).Error
}
func (s *Store) RecordRideLocation(user, ride string, lat, lng, accuracy float64) error {
	if !ValidLocation(lat, lng, accuracy) {
		return FieldError("location", "invalid coordinates")
	}
	return s.Transaction(func(tx *Store) error {
		r, err := tx.RideForUser(ride, user)
		if err != nil {
			return err
		}
		if r.DriverID != user || r.Status != "in_progress" {
			return OpError(403, "only the assigned driver of an in-progress ride may send locations")
		}
		var n int64
		if err := tx.db.Model(&models.RideLocationPoint{}).Where("ride_id = ? AND at > ?", ride, tx.now().Add(-time.Minute)).Count(&n).Error; err != nil {
			return err
		}
		if n >= 30 {
			return &OperationError{Status: 429, Message: "location rate limit", RetryAfter: 2}
		}
		if err := tx.db.Create(&models.RideLocationPoint{ID: newID(), UserID: user, RideID: ride, Lat: lat, Lng: lng, Accuracy: accuracy, At: tx.now(), CreatedAt: tx.now()}).Error; err != nil {
			return err
		}
		return tx.db.Model(&models.Ride{}).Where("id = ?", ride).Updates(map[string]any{"last_lat": lat, "last_lng": lng, "last_location_at": tx.now()}).Error
	})
}
func (s *Store) HasFraudMitigation(user, kind string) (bool, error) {
	var n int64
	err := s.db.Model(&models.FraudMitigation{}).Where("user_id = ? AND type = ? AND active = ?", user, kind, true).Count(&n).Error
	return n > 0, err
}
func (s *Store) checkPayoutFraud(user string) error {
	held, err := s.HasFraudMitigation(user, "payout_hold")
	if err != nil {
		return err
	}
	if held {
		return OpError(403, "payouts are held pending review")
	}
	return nil
}
func normalizeIdentity(v string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '+' || r == '(' || r == ')' {
			return -1
		}
		return r
	}, strings.ToLower(v))
}

func (s *Store) LinkedIdentifiers(id string) ([]string, error) {
	var u models.User
	if err := s.db.First(&u, "id = ?", id).Error; err != nil {
		return nil, err
	}
	var profile models.DriverProfile
	if err := s.db.First(&profile, "user_id = ?", id).Error; err != nil && !notFound(err) {
		return nil, err
	}
	users := []models.User{}
	if err := s.db.Where("id <> ? AND status <> ?", id, "deleted").Find(&users).Error; err != nil {
		return nil, err
	}
	ids := []string{}
	for _, other := range users {
		if u.Phone != "" && normalizeIdentity(u.Phone) == normalizeIdentity(other.Phone) {
			ids = append(ids, other.ID)
		}
	}
	drivers := []models.DriverProfile{}
	if err := s.db.Where("user_id <> ?", id).Find(&drivers).Error; err != nil {
		return nil, err
	}
	for _, other := range drivers {
		if (profile.PlateNumber != "" && normalizeIdentity(profile.PlateNumber) == normalizeIdentity(other.PlateNumber)) || (profile.LicenceNumber != "" && normalizeIdentity(profile.LicenceNumber) == normalizeIdentity(other.LicenceNumber)) {
			ids = append(ids, other.UserID)
		}
	}
	return ids, nil
}
