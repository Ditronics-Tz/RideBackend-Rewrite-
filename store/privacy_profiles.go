package store

import (
	"gorm.io/gorm"
	"ride-backend/models"
)

func init() {
	RegisterDataExporter(func(s *Store) DataExporter { return profileExporter{s} })
	RegisterAnonymizer(profileAnonymizer{})
}

type profileExporter struct{ s *Store }

func (profileExporter) Name() string { return "profiles" }
func (profileExporter) Tables() []string {
	return []string{"passenger_profiles", "driver_profiles", "driver_documents", "verification_reviews", "driver_notifications"}
}
func (e profileExporter) Export(id string) (any, []string, error) {
	var pp models.PassengerProfile
	var dp models.DriverProfile
	var docs []models.DriverDocument
	var reviews []models.VerificationReview
	var notes []models.DriverNotification
	db := e.s.db
	_ = db.First(&pp, "user_id = ?", id).Error
	_ = db.First(&dp, "user_id = ?", id).Error
	_ = db.Where("driver_id = ?", id).Find(&docs).Error
	_ = db.Where("driver_id = ?", id).Find(&reviews).Error
	_ = db.Where("driver_id = ?", id).Find(&notes).Error
	files := []string{}
	if pp.ProfilePictureURL != "" {
		files = append(files, publicPath(pp.ProfilePictureURL))
	}
	if dp.ProfilePictureURL != "" {
		files = append(files, publicPath(dp.ProfilePictureURL))
	}
	if dp.LicenceImageURL != "" {
		files = append(files, legacyPrivatePath(dp.LicenceImageURL))
	}
	docOut := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		docOut = append(docOut, map[string]any{"id": d.ID, "type": d.Type, "status": d.Status, "rejection_code": d.RejectionCode, "rejection_note": d.RejectionNote, "expires_at": d.ExpiresAt, "created_at": d.CreatedAt})
		if d.FilePath != "" {
			files = append(files, d.FilePath)
		}
	}
	reviewOut := make([]map[string]any, 0, len(reviews))
	for _, r := range reviews {
		reviewOut = append(reviewOut, map[string]any{"decision": r.Decision, "reason_code": r.ReasonCode, "note": r.Note, "created_at": r.CreatedAt})
	}
	notifyOut := make([]map[string]any, 0, len(notes))
	for _, n := range notes {
		notifyOut = append(notifyOut, map[string]any{"message": n.Message, "created_at": n.CreatedAt})
	}
	passenger := map[string]any{"full_name": pp.FullName, "profile_picture_url": pp.ProfilePictureURL, "phone": pp.Phone, "average_rating": pp.AverageRating, "total_ratings": pp.TotalRatings, "created_at": pp.CreatedAt}
	driver := map[string]any{"licence_number": dp.LicenceNumber, "vehicle_card_number": dp.VehicleCardNumber, "plate_number": dp.PlateNumber, "licence_image_url": dp.LicenceImageURL, "profile_picture_url": dp.ProfilePictureURL, "verification_status": dp.VerificationStatus, "is_online": dp.IsOnline, "average_rating": dp.AverageRating, "total_ratings": dp.TotalRatings, "created_at": dp.CreatedAt}
	return map[string]any{"passenger_profile": passenger, "driver_profile": driver, "documents": docOut, "reviews": reviewOut, "notifications": notifyOut}, files, nil
}
func publicPath(url string) string {
	const prefix = "/api/v1/uploads/"
	if len(url) >= len(prefix) && url[:len(prefix)] == prefix {
		return "uploads/public/" + url[len(prefix):]
	}
	return ""
}
func legacyPrivatePath(url string) string {
	const prefix = "/api/v1/files/licences/"
	if len(url) >= len(prefix) && url[:len(prefix)] == prefix {
		return "uploads/licences/" + url[len(prefix):]
	}
	return ""
}

type profileAnonymizer struct{}

func (profileAnonymizer) Name() string { return "profiles_and_documents" }
func (profileAnonymizer) Tables() []string {
	return []string{"passenger_profiles", "driver_profiles", "driver_documents", "verification_reviews", "driver_duplicate_flags", "driver_notifications"}
}
func (profileAnonymizer) Description() string {
	return "clears names, contact, vehicle identifiers, image references, document paths and notes; retains verification decisions and UUIDs"
}
func (profileAnonymizer) Anonymize(tx *gorm.DB, id string) error {
	if err := tx.Model(&models.PassengerProfile{}).Where("user_id = ?", id).Updates(map[string]any{"full_name": "", "phone": "", "profile_picture_url": ""}).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.DriverProfile{}).Where("user_id = ?", id).Updates(map[string]any{"licence_number": "", "vehicle_card_number": "", "plate_number": "", "licence_image_url": "", "profile_picture_url": "", "is_online": false}).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.DriverDocument{}).Where("driver_id = ?", id).Updates(map[string]any{"file_path": "", "file_url": "", "rejection_note": ""}).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.VerificationReview{}).Where("driver_id = ?", id).Update("note", "").Error; err != nil {
		return err
	}
	if err := tx.Model(&models.DriverDuplicateFlag{}).Where("driver_id = ? OR matched_driver_id = ?", id, id).Delete(&models.DriverDuplicateFlag{}).Error; err != nil {
		return err
	}
	return tx.Model(&models.DriverNotification{}).Where("driver_id = ?", id).Update("message", "").Error
}
