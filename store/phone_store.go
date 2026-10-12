package store

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"math/big"
	backenddb "ride-backend/db"
	"ride-backend/models"
	"time"
)

// The production adapter must send SMS to phone. Email does not prove phone ownership.
type SMSSender interface {
	SendOTP(context.Context, string, string) error
}

func phoneHash(id, phone, code, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(id + "|" + phone + "|" + code))
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Store) RequestPhoneOTP(ctx context.Context, id, raw, secret string) error {
	phone, err := backenddb.CanonicalPhone(raw)
	if err != nil || phone == "" {
		return invalid("a valid Tanzanian phone number is required")
	}
	if s.SMS == nil {
		return fmt.Errorf("SMS provider is not configured")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var u models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", id).Error; err != nil {
			return err
		}
		var previous models.PhoneChallenge
		err := tx.First(&previous, "user_id = ?", id).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		now := time.Now().UTC()
		if err == nil && previous.SentAt.Add(time.Minute).After(now) {
			return conflict("wait 60 seconds before requesting another code")
		}
		var count int64
		if err := tx.Model(&models.User{}).Where("phone = ? AND id <> ?", phone, id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return conflict("phone is already registered")
		}
		n, err := rand.Int(rand.Reader, big.NewInt(1000000))
		if err != nil {
			return err
		}
		code := fmt.Sprintf("%06d", n.Int64())
		challenge := models.PhoneChallenge{UserID: id, Phone: phone, CodeHash: phoneHash(id, phone, code, secret), ExpiresAt: now.Add(5 * time.Minute), SentAt: now}
		if err := tx.Save(&challenge).Error; err != nil {
			return err
		}
		return s.SMS.SendOTP(ctx, phone, code)
	})
}
func (s *Store) VerifyPhoneOTP(id, code, secret string) error {
	if len(code) != 6 {
		return invalid("code must contain six digits")
	}
	valid := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var u models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", id).Error; err != nil {
			return err
		}
		var ch models.PhoneChallenge
		if err := tx.First(&ch, "user_id = ?", id).Error; err != nil {
			return invalid("request a new code")
		}
		if ch.Attempts >= 5 || !ch.ExpiresAt.After(time.Now()) {
			return invalid("code expired or attempts exhausted")
		}
		ch.Attempts++
		if !hmac.Equal([]byte(ch.CodeHash), []byte(phoneHash(id, ch.Phone, code, secret))) {
			return tx.Save(&ch).Error
		}
		now := time.Now().UTC()
		if err := tx.Model(&u).Updates(map[string]interface{}{"phone": ch.Phone, "phone_verified_at": now}).Error; err != nil {
			return conflict("phone is already registered")
		}
		if err := tx.Model(&models.PassengerProfile{}).Where("user_id = ?", id).Update("phone", ch.Phone).Error; err != nil {
			return err
		}
		if err := tx.Delete(&ch).Error; err != nil {
			return err
		}
		valid = true
		return nil
	})
	if err != nil {
		return err
	}
	if !valid {
		return invalid("invalid code")
	}
	return nil
}
