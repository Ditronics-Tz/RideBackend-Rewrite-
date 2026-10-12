package db

import (
	"fmt"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"path/filepath"
	"ride-backend/models"
	"time"
)

// MigrateVerification preserves legacy licence files but requires a full new review.
// A boolean approval cannot establish that all newly-required documents were checked.
func MigrateVerification(gdb *gorm.DB) error {
	return gdb.Transaction(func(tx *gorm.DB) error {
		for _, field := range []string{"licence_number", "plate_number"} {
			expression := "UPPER(REPLACE(REPLACE(TRIM(" + field + "), ' ', ''), '-', ''))"
			var duplicates int64
			if err := tx.Raw("SELECT COUNT(*) FROM (SELECT " + expression + " AS number FROM driver_profiles WHERE " + field + " <> '' GROUP BY " + expression + " HAVING COUNT(*) > 1) AS duplicates").Scan(&duplicates).Error; err != nil {
				return err
			}
			if duplicates > 0 {
				return fmt.Errorf("verification migration: resolve duplicate %s values before adding unique indexes; no accounts were changed", field)
			}
			if err := tx.Exec("UPDATE driver_profiles SET " + field + " = " + expression).Error; err != nil {
				return err
			}
			if err := tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_driver_unique_" + field + " ON driver_profiles (" + expression + ") WHERE " + field + " <> ''").Error; err != nil {
				return err
			}
		}
		if err := tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_current_document ON driver_documents (driver_id, type) WHERE is_current = true").Error; err != nil {
			return err
		}
		var profiles []models.DriverProfile
		if err := tx.Where("licence_image_url <> ''").Find(&profiles).Error; err != nil {
			return err
		}
		for _, p := range profiles {
			var count int64
			if err := tx.Model(&models.DriverDocument{}).Where("driver_id = ? AND type = ?", p.UserID, "driving_licence").Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			id := uuid.NewString()
			doc := models.DriverDocument{ID: id, DriverID: p.UserID, Type: "driving_licence", FilePath: filepath.Join("uploads", "licences", filepath.Base(p.LicenceImageURL)), FileURL: "/api/v1/files/documents/" + id, Status: "submitted", IsCurrent: true, CreatedAt: p.CreatedAt}
			if err := tx.Create(&doc).Error; err != nil {
				return err
			}
			r := models.VerificationReview{ID: uuid.NewString(), DriverID: p.UserID, DocumentID: &doc.ID, Decision: "legacy_import", Note: "Existing licence preserved; full document submission and review required", CreatedAt: time.Now().UTC()}
			if err := tx.Create(&r).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
