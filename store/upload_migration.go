package store

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"ride-backend/models"
	"ride-backend/utils"
)

// MigrateLegacyUploads reads metadata, never filenames, to determine publicity.
// A private reference always wins over a public reference to the same file.
func (s *Store) MigrateLegacyUploads(root string, dry bool) (utils.UploadMigrationCounts, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return utils.UploadMigrationCounts{}, err
	}
	classes := map[string]utils.UploadClass{}
	canonical := func(path string) string {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return ""
		}
		return filepath.Clean(absolute)
	}
	mark := func(path string, class utils.UploadClass) {
		path = canonical(path)
		if classes[path] != utils.UploadPrivate {
			classes[path] = class
		}
	}
	nameFromURL := func(value string) string {
		u, err := url.Parse(value)
		if err != nil {
			return ""
		}
		name := filepath.Base(u.Path)
		if name == "." || name == string(os.PathSeparator) {
			return ""
		}
		return name
	}
	markName := func(name string, class utils.UploadClass) {
		if name != "" {
			mark(filepath.Join(root, name), class)
			mark(filepath.Join(root, "public", name), class)
		}
	}
	drivers := []models.DriverProfile{}
	passengers := []models.PassengerProfile{}
	documents := []models.DriverDocument{}
	incidents := []models.IncidentAttachment{}
	exports := []models.DataExportRequest{}
	for _, query := range []struct {
		model, out any
		columns    string
	}{
		{&models.DriverProfile{}, &drivers, "user_id, profile_picture_url, licence_image_url"},
		{&models.PassengerProfile{}, &passengers, "user_id, profile_picture_url"},
		{&models.DriverDocument{}, &documents, "id, file_path, file_url"},
		{&models.IncidentAttachment{}, &incidents, "id, file_path"},
		{&models.DataExportRequest{}, &exports, "id, file_path"},
	} {
		if err := s.db.Model(query.model).Select(query.columns).Find(query.out).Error; err != nil {
			return utils.UploadMigrationCounts{}, err
		}
	}
	for _, p := range drivers {
		if strings.Contains(p.ProfilePictureURL, "/api/v1/uploads/") {
			markName(nameFromURL(p.ProfilePictureURL), utils.UploadPublic)
		}
		if p.LicenceImageURL != "" {
			markName(nameFromURL(p.LicenceImageURL), utils.UploadPrivate)
		}
	}
	for _, p := range passengers {
		if strings.Contains(p.ProfilePictureURL, "/api/v1/uploads/") {
			markName(nameFromURL(p.ProfilePictureURL), utils.UploadPublic)
		}
	}
	for _, d := range documents {
		if d.FilePath != "" {
			mark(d.FilePath, utils.UploadPrivate)
			markName(filepath.Base(d.FilePath), utils.UploadPrivate)
		}
		if d.FileURL != "" {
			markName(nameFromURL(d.FileURL), utils.UploadPrivate)
		}
	}
	for _, r := range incidents {
		if r.FilePath != "" {
			mark(r.FilePath, utils.UploadPrivate)
			markName(filepath.Base(r.FilePath), utils.UploadPrivate)
		}
	}
	for _, r := range exports {
		if r.FilePath != "" {
			mark(r.FilePath, utils.UploadPrivate)
			markName(filepath.Base(r.FilePath), utils.UploadPrivate)
		}
	}
	return utils.MigrateUploads(root, func(path string) utils.UploadClass { return classes[canonical(path)] }, func(move utils.UploadMove) error {
		if move.Class != utils.UploadPrivate {
			return nil
		}
		return s.Transaction(func(tx *Store) error {
			for _, d := range documents {
				changes := map[string]any{}
				if d.FilePath != "" && canonical(d.FilePath) == move.Source {
					changes["file_path"] = move.Destination
					changes["file_url"] = "/api/v1/files/documents/" + d.ID
				}
				if strings.Contains(d.FileURL, "/api/v1/uploads/") && nameFromURL(d.FileURL) == filepath.Base(move.Source) {
					changes["file_url"] = "/api/v1/files/documents/" + d.ID
				}
				if len(changes) > 0 {
					if err := tx.db.Model(&models.DriverDocument{}).Where("id = ?", d.ID).Updates(changes).Error; err != nil {
						return err
					}
				}
			}
			for _, p := range drivers {
				if p.LicenceImageURL == "" || nameFromURL(p.LicenceImageURL) != filepath.Base(move.Source) {
					continue
				}
				oldPrivate := filepath.Join(root, "licences", nameFromURL(p.LicenceImageURL))
				if _, err := os.Lstat(oldPrivate); err == nil {
					continue
				} else if !os.IsNotExist(err) {
					return err
				}
				if err := tx.db.Model(&models.DriverProfile{}).Where("user_id = ? AND licence_image_url = ?", p.UserID, p.LicenceImageURL).Update("licence_image_url", "/api/v1/files/licences/"+filepath.Base(move.Destination)).Error; err != nil {
					return err
				}
			}
			for _, r := range incidents {
				if r.FilePath != "" && canonical(r.FilePath) == move.Source {
					if err := tx.db.Model(&models.IncidentAttachment{}).Where("id = ?", r.ID).Update("file_path", move.Destination).Error; err != nil {
						return err
					}
				}
			}
			for _, r := range exports {
				if r.FilePath != "" && canonical(r.FilePath) == move.Source {
					if err := tx.db.Model(&models.DataExportRequest{}).Where("id = ?", r.ID).Update("file_path", move.Destination).Error; err != nil {
						return err
					}
				}
			}
			return nil
		})
	}, dry)
}
