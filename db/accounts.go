package db

import (
	"fmt"
	"gorm.io/gorm"
	"ride-backend/models"
	"strings"
)

func CanonicalPhone(raw string) (string, error) {
	v := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(strings.TrimSpace(raw))
	if v == "" {
		return "", nil
	}
	if strings.HasPrefix(v, "0") && len(v) == 10 {
		v = "+255" + v[1:]
	}
	if strings.HasPrefix(v, "255") {
		v = "+" + v
	}
	if len(v) != 13 || !strings.HasPrefix(v, "+255") {
		return "", fmt.Errorf("phone must be a Tanzanian number, such as +255712345678")
	}
	for _, r := range v[1:] {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("phone contains invalid characters")
		}
	}
	return v, nil
}

func MigrateAccounts(gdb *gorm.DB) error {
	return gdb.Transaction(func(tx *gorm.DB) error {
		// Preserve existing disabled accounts while IsActive remains in use.
		if err := tx.Model(&models.User{}).Where("is_active = ? AND status = ?", false, "active").Update("status", "suspended").Error; err != nil {
			return err
		}
		var profiles []models.PassengerProfile
		if err := tx.Where("phone <> ''").Find(&profiles).Error; err != nil {
			return err
		}
		for _, p := range profiles {
			var u models.User
			if err := tx.First(&u, "id = ?", p.UserID).Error; err != nil {
				return err
			}
			if u.Phone != "" {
				continue
			}
			phone, err := CanonicalPhone(p.Phone)
			if err != nil {
				return fmt.Errorf("account migration: fix invalid passenger phone for user %s", p.UserID)
			}
			if err := tx.Model(&u).Update("phone", phone).Error; err != nil {
				return err
			}
		}
		var duplicates int64
		if err := tx.Raw("SELECT COUNT(*) FROM (SELECT phone FROM users WHERE phone <> '' GROUP BY phone HAVING COUNT(*) > 1) AS duplicates").Scan(&duplicates).Error; err != nil {
			return err
		}
		if duplicates > 0 {
			return fmt.Errorf("account migration: resolve duplicate phone numbers before starting; no phone values changed")
		}
		return tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_user_phone_unique ON users (phone) WHERE phone <> ''").Error
	})
}
