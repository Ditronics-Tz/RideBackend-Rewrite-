package store

import (
	"errors"

	backenddb "ride-backend/db"
	"ride-backend/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpdatePassengerBasics keeps account and passenger profile fields in sync.
// A newly entered phone is profile data until the existing OTP flow verifies it.
func (s *Store) UpdatePassengerBasics(userID string, name, phone *string) (*models.PassengerProfile, error) {
	var canonicalPhone *string
	if phone != nil {
		value, err := backenddb.CanonicalPhone(*phone)
		if err != nil {
			return nil, FieldError("phone_number", err.Error())
		}
		canonicalPhone = &value
	}
	var profile models.PassengerProfile
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", userID).Error; err != nil {
			return err
		}
		if user.Role != models.RolePassenger {
			return OpError(403, "passenger role required")
		}
		if err := tx.FirstOrCreate(&profile, &models.PassengerProfile{UserID: userID, FullName: user.Name}).Error; err != nil {
			return err
		}
		userUpdates := map[string]any{}
		profileUpdates := map[string]any{}
		if name != nil {
			userUpdates["name"] = *name
			profileUpdates["full_name"] = *name
		}
		if canonicalPhone != nil {
			if *canonicalPhone != "" && *canonicalPhone != user.Phone {
				var count int64
				if err := tx.Model(&models.User{}).Where("phone = ? AND id <> ?", *canonicalPhone, userID).Count(&count).Error; err != nil {
					return err
				}
				if count > 0 {
					return OpError(409, "phone number is already registered")
				}
			}
			userUpdates["phone"] = *canonicalPhone
			profileUpdates["phone"] = *canonicalPhone
			if *canonicalPhone != user.Phone {
				userUpdates["phone_verified_at"] = nil
			}
		}
		if len(userUpdates) > 0 {
			userUpdates["updated_at"] = s.now()
			if err := tx.Model(&user).Updates(userUpdates).Error; err != nil {
				var sqlState interface{ SQLState() string }
				if errors.Is(err, gorm.ErrDuplicatedKey) || errors.As(err, &sqlState) && sqlState.SQLState() == "23505" {
					return OpError(409, "phone number is already registered")
				}
				return err
			}
		}
		if len(profileUpdates) > 0 {
			profileUpdates["updated_at"] = s.now()
			if err := tx.Model(&profile).Updates(profileUpdates).Error; err != nil {
				return err
			}
		}
		return tx.First(&profile, "user_id = ?", userID).Error
	})
	return &profile, err
}
