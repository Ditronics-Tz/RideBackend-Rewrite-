package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/models"
	"ride-backend/notifications"
)

func (s *Store) queueAppNotification(ctx context.Context, userID, kind, title, body string, data map[string]string) error {
	if _, err := uuid.Parse(userID); err != nil {
		return nil // External emergency contacts have no app inbox.
	}
	if len([]rune(title)) > 160 || len([]rune(body)) > 500 {
		return FieldError("notification", "notification content is too long")
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	now := s.now()
	n := models.AppNotification{ID: newID(), UserID: userID, Kind: kind, Title: title, Body: body, Data: string(payload), CreatedAt: now}
	if err := s.db.WithContext(ctx).Create(&n).Error; err != nil {
		return err
	}
	if s.PushSender == nil {
		return nil // The inbox is complete without a transport provider.
	}
	var devices []models.PushDevice
	if err := s.db.WithContext(ctx).Where("user_id = ? AND active = true", userID).Find(&devices).Error; err != nil {
		return err
	}
	for _, device := range devices {
		d := models.PushDelivery{NotificationID: n.ID, DeviceID: device.ID, Status: "pending", NextAttemptAt: now}
		if err := s.db.WithContext(ctx).Create(&d).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) queueRideNotification(ctx context.Context, passengerID, rideID, status string) error {
	messages := map[string]string{
		"assigned":            "A driver has accepted your ride.",
		"driver_arrived":      "Your driver has arrived.",
		"in_progress":         "Your ride has started.",
		"completed":           "Your ride is complete.",
		"cancelled_by_driver": "Your ride was cancelled by the driver.",
		"cancelled_by_admin":  "Your ride was cancelled by Rida support.",
	}
	body, ok := messages[status]
	if !ok {
		return nil
	}
	return s.queueAppNotification(ctx, passengerID, "ride", "Ride update", body, map[string]string{"ride_id": rideID, "status": status})
}

func (s *Store) ListAppNotifications(ctx context.Context, userID string, page Page) ([]models.AppNotification, int64, int64, error) {
	q := s.db.WithContext(ctx).Model(&models.AppNotification{}).Where("user_id = ?", userID)
	var total, unread int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, 0, err
	}
	if err := q.Where("read_at IS NULL").Count(&unread).Error; err != nil {
		return nil, 0, 0, err
	}
	rows := []models.AppNotification{}
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC, id DESC").Limit(page.Limit).Offset(page.Offset).Find(&rows).Error
	return rows, total, unread, err
}

// LatestAppNotificationID reads the durable inbox, so change detection works
// across API instances and only observes committed notifications.
func (s *Store) LatestAppNotificationID(ctx context.Context, userID string) (string, error) {
	var ids []string
	err := s.db.WithContext(ctx).Model(&models.AppNotification{}).
		Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").Limit(1).Pluck("id", &ids).Error
	if err != nil || len(ids) == 0 {
		return "", err
	}
	return ids[0], nil
}

func (s *Store) MarkAppNotificationRead(ctx context.Context, userID, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return FieldError("id", "invalid notification ID")
	}
	r := s.db.WithContext(ctx).Model(&models.AppNotification{}).Where("id = ? AND user_id = ?", id, userID).Update("read_at", gorm.Expr("COALESCE(read_at, ?)", s.now()))
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Store) MarkAllAppNotificationsRead(ctx context.Context, userID string) error {
	return s.db.WithContext(ctx).Model(&models.AppNotification{}).Where("user_id = ? AND read_at IS NULL", userID).Update("read_at", s.now()).Error
}

func (s *Store) encryptPushToken(token string) (string, error) {
	if len(s.PushKey) != 32 {
		return "", OpError(503, "push registration is not configured")
	}
	block, err := aes.NewCipher(s.PushKey)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(token), nil)), nil
}

func (s *Store) decryptPushToken(encoded string) (string, error) {
	if len(s.PushKey) != 32 {
		return "", errors.New("push encryption unavailable")
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.PushKey)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(raw) < aead.NonceSize() {
		return "", errors.New("invalid push token ciphertext")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	return string(plain), err
}

func (s *Store) RegisterPushDevice(ctx context.Context, userID, sessionID, token string) (*models.PushDevice, error) {
	if len(token) < 20 || len(token) > 4096 || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n\t ") {
		return nil, FieldError("token", "invalid FCM registration token")
	}
	ciphertext, err := s.encryptPushToken(token)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(token))
	now := s.now()
	var device models.PushDevice
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session models.AppSession
		if err := tx.Where("id = ? AND user_id = ? AND revoked_at IS NULL AND expires_at > ? AND absolute_expires_at > ?", sessionID, userID, now, now).First(&session).Error; err != nil {
			return OpError(401, "active app session required")
		}
		key := hex.EncodeToString(hash[:])
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("token_hash = ?", key).First(&device).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			device = models.PushDevice{ID: newID(), TokenHash: key, CreatedAt: now}
		}
		if device.UserID != "" && device.UserID != userID {
			return OpError(409, "push token belongs to another account; sign out there first")
		}
		device.UserID = userID
		device.SessionFamilyID = session.FamilyID
		device.TokenCiphertext = ciphertext
		device.Platform = "android"
		device.Active = true
		device.UpdatedAt = now
		if err := tx.Save(&device).Error; err != nil {
			return err
		}
		return tx.Model(&models.PushDevice{}).Where("user_id = ? AND session_family_id = ? AND id <> ? AND active = true", userID, session.FamilyID, device.ID).Updates(map[string]any{"active": false, "token_ciphertext": "", "updated_at": now}).Error
	})
	return &device, err
}

func (s *Store) ListPushDevices(ctx context.Context, userID string) ([]models.PushDevice, error) {
	rows := []models.PushDevice{}
	err := s.db.WithContext(ctx).Select("id, platform, created_at").Where("user_id = ? AND active = true", userID).Order("created_at DESC").Find(&rows).Error
	return rows, err
}

func (s *Store) UnregisterPushDevice(ctx context.Context, userID, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return FieldError("id", "invalid device ID")
	}
	return s.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).Delete(&models.PushDevice{}).Error
}

func (s *Store) ProcessPushDeliveries(ctx context.Context) error {
	if s.PushSender == nil {
		return nil
	}
	now := s.now()
	if err := s.db.WithContext(ctx).Model(&models.PushDelivery{}).Where("status = 'processing' AND claimed_at < ?", now.Add(-time.Minute)).Updates(map[string]any{"status": "pending", "next_attempt_at": now}).Error; err != nil {
		return err
	}
	rows := []models.PushDelivery{}
	if err := s.db.WithContext(ctx).Where("status = 'pending' AND next_attempt_at <= ?", now).Order("next_attempt_at ASC").Limit(100).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		claim := s.db.WithContext(ctx).Model(&models.PushDelivery{}).Where("notification_id = ? AND device_id = ? AND status = 'pending'", row.NotificationID, row.DeviceID).Updates(map[string]any{"status": "processing", "claimed_at": now, "attempts": row.Attempts + 1})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			continue
		}
		status := "sent"
		var notification models.AppNotification
		var device models.PushDevice
		if err := s.db.WithContext(ctx).First(&notification, "id = ?", row.NotificationID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		if err := s.db.WithContext(ctx).First(&device, "id = ?", row.DeviceID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		var activeSessions int64
		if err := s.db.WithContext(ctx).Model(&models.AppSession{}).Where("family_id = ? AND user_id = ? AND revoked_at IS NULL AND expires_at > ? AND absolute_expires_at > ?", device.SessionFamilyID, device.UserID, now, now).Count(&activeSessions).Error; err != nil {
			return err
		}
		if !device.Active || device.UserID != notification.UserID || activeSessions == 0 {
			status = "skipped"
		} else {
			prefs, err := s.PassengerPreferences(ctx, notification.UserID)
			if err != nil {
				status = "pending"
			} else if !prefs.PushEnabled || notification.Kind == "promo" && !prefs.Promotions || notification.Kind == "ride" && !prefs.RideUpdates {
				status = "skipped"
			} else {
				token, err := s.decryptPushToken(device.TokenCiphertext)
				if err == nil {
					var data map[string]string
					_ = json.Unmarshal([]byte(notification.Data), &data)
					err = s.PushSender.SendPush(ctx, token, notifications.PushMessage{ID: notification.ID, Kind: notification.Kind, Title: notification.Title, Body: notification.Body, PromoID: data["promo_id"], RideID: data["ride_id"]})
				}
				if errors.Is(err, notifications.ErrInvalidPushToken) {
					status = "invalid"
					if err := s.db.WithContext(ctx).Model(&device).Updates(map[string]any{"active": false, "token_ciphertext": "", "updated_at": s.now()}).Error; err != nil {
						return err
					}
				} else if err != nil {
					status = "pending"
				}
			}
		}
		if status == "pending" && row.Attempts >= 9 {
			status = "failed"
		}
		backoff := time.Duration(1<<min(row.Attempts, 6)) * time.Minute
		updates := map[string]any{"status": status, "next_attempt_at": s.now().Add(backoff)}
		if status == "sent" {
			updates["sent_at"] = s.now()
		}
		if err := s.db.WithContext(ctx).Model(&models.PushDelivery{}).Where("notification_id = ? AND device_id = ? AND status = 'processing'", row.NotificationID, row.DeviceID).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) AdminAppNotificationCounts(ctx context.Context) (int64, models.AppPushCounts, error) {
	var inbox int64
	if err := s.db.WithContext(ctx).Model(&models.AppNotification{}).Count(&inbox).Error; err != nil {
		return 0, models.AppPushCounts{}, err
	}
	var groups []struct {
		Status string
		Count  int64
	}
	if err := s.db.WithContext(ctx).Model(&models.PushDelivery{}).Select("status, COUNT(*) AS count").Group("status").Scan(&groups).Error; err != nil {
		return 0, models.AppPushCounts{}, err
	}
	counts := models.AppPushCounts{}
	for _, row := range groups {
		switch row.Status {
		case "pending":
			counts.Pending = row.Count
		case "processing":
			counts.Processing = row.Count
		case "sent":
			counts.Sent = row.Count
		case "skipped":
			counts.Skipped = row.Count
		case "invalid":
			counts.Invalid = row.Count
		case "failed":
			counts.Failed = row.Count
		}
	}
	return inbox, counts, nil
}
