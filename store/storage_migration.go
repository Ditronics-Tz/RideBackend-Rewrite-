package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"ride-backend/models"
	"ride-backend/storage"
)

// StorageMigrationCounts deliberately contains no object keys, paths or identities.
type StorageMigrationCounts struct {
	Documents       int `json:"documents"`
	ProfilePhotos   int `json:"profile_photos"`
	Migrated        int `json:"migrated"`
	AlreadyMigrated int `json:"already_migrated"`
	SkippedExternal int `json:"skipped_external"`
	SkippedMissing  int `json:"skipped_missing"`
	Rejected        int `json:"rejected"`
	Conflicts       int `json:"conflicts"`
}

var errStorageMigration = errors.New("storage migration failed; provider, database and file details are redacted")
var errMigrationSource = errors.New("invalid migration source")

// MigrateFilesToR2 copies authoritative local document and profile references.
// It never deletes source files. Dry runs neither contact the destination nor
// change rows. Incidents, exports and backups are not migrated by this command.
func (s *Store) MigrateFilesToR2(ctx context.Context, root string, destination storage.Storage, dryRun bool) (StorageMigrationCounts, error) {
	counts := StorageMigrationCounts{}
	if !dryRun && destination == nil {
		return counts, errStorageMigration
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return counts, errStorageMigration
	}
	confined, err := os.OpenRoot(absRoot)
	if err != nil {
		return counts, errStorageMigration
	}
	defer confined.Close()
	db := s.db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	var docs []models.DriverDocument
	var drivers []models.DriverProfile
	var passengers []models.PassengerProfile
	for _, query := range []struct {
		model, out any
		columns    string
	}{
		{&models.DriverDocument{}, &docs, "id, driver_id, file_path, file_url, object_key, staging_object_key"},
		{&models.DriverProfile{}, &drivers, "user_id, profile_picture_url, profile_picture_key, licence_image_url"},
		{&models.PassengerProfile{}, &passengers, "user_id, profile_picture_url, profile_picture_key"},
	} {
		if err := db.Model(query.model).Select(query.columns).Find(query.out).Error; err != nil {
			return counts, errStorageMigration
		}
	}
	privateSources := map[string]bool{}
	for _, doc := range docs {
		if rel, err := migrationRelativePath(absRoot, doc.FilePath); err == nil {
			privateSources[strings.ToLower(rel)] = true
		}
		if rel, external, err := migrationPhotoPath(doc.FileURL); err == nil && !external {
			privateSources[strings.ToLower(rel)] = true
		}
	}
	for _, driver := range drivers {
		if rel, external, err := migrationPhotoPath(driver.LicenceImageURL); err == nil && !external {
			privateSources[strings.ToLower(rel)] = true
		}
	}
	copyObject := func(rel, prefix string, visibility storage.Visibility, maxSize int64) (string, storage.ObjectInfo, bool, error) {
		payload, contentType, err := migrationReadSource(confined, rel, maxSize, visibility)
		if errors.Is(err, os.ErrNotExist) {
			counts.SkippedMissing++
			return "", storage.ObjectInfo{}, false, nil
		}
		if err != nil {
			counts.Rejected++
			return "", storage.ObjectInfo{}, false, nil
		}
		if err := ctx.Err(); err != nil {
			return "", storage.ObjectInfo{}, false, errStorageMigration
		}
		digest := sha256.Sum256(payload)
		name := uuid.NewSHA1(uuid.NameSpaceURL, append([]byte(prefix+"/"+rel+"/"), digest[:]...)).String()
		key := prefix + "/" + name + migrationExtension(contentType)
		if err := storage.ValidateKey(key); err != nil {
			counts.Rejected++
			return "", storage.ObjectInfo{}, false, nil
		}
		info := storage.ObjectInfo{Key: key, Size: int64(len(payload)), ContentType: contentType}
		if dryRun {
			return key, info, true, nil
		}
		// Always replace the deterministic destination on retry, then verify it.
		// A prior failed DB switch therefore cannot make a stale object authoritative.
		if _, err := destination.Put(ctx, visibility, key, storage.Upload{Body: bytes.NewReader(payload), Size: info.Size, ContentType: contentType}); err != nil {
			return "", info, false, errStorageMigration
		}
		stored, err := destination.Head(ctx, visibility, key)
		if err != nil || stored.Size != info.Size || stored.ContentType != info.ContentType {
			return "", info, false, errStorageMigration
		}
		return key, info, true, nil
	}
	for _, doc := range docs {
		counts.Documents++
		if doc.ObjectKey != "" || doc.StagingObjectKey != "" {
			counts.AlreadyMigrated++
			continue
		}
		if doc.FilePath == "" {
			counts.SkippedMissing++
			continue
		}
		rel, err := migrationRelativePath(absRoot, doc.FilePath)
		if err != nil {
			counts.Rejected++
			continue
		}
		key, info, eligible, err := copyObject(rel, "documents/"+doc.DriverID+"/"+doc.ID, storage.Private, 10*1024*1024)
		if err != nil {
			return counts, err
		}
		if !eligible || dryRun {
			continue
		}
		result := db.Model(&models.DriverDocument{}).Where("id = ? AND file_path = ? AND (object_key = '' OR object_key IS NULL)", doc.ID, doc.FilePath).Updates(map[string]any{"object_key": key, "file_path": "", "file_url": "", "content_type": info.ContentType, "expected_size": info.Size})
		if result.Error != nil {
			return counts, errStorageMigration
		}
		if result.RowsAffected == 0 {
			counts.Conflicts++
		} else {
			counts.Migrated++
		}
	}
	migratePhoto := func(model any, userID, reference, existingKey string) error {
		if reference == "" && existingKey == "" {
			return nil
		}
		counts.ProfilePhotos++
		if existingKey != "" {
			counts.AlreadyMigrated++
			return nil
		}
		rel, external, err := migrationPhotoPath(reference)
		if external {
			counts.SkippedExternal++
			return nil
		}
		if err != nil {
			counts.Rejected++
			return nil
		}
		if privateSources[strings.ToLower(rel)] {
			counts.Rejected++
			return nil
		}
		key, _, eligible, err := copyObject(rel, "profile/"+userID, storage.Public, 5*1024*1024)
		if err != nil {
			return err
		}
		if !eligible || dryRun {
			return nil
		}
		result := db.Model(model).Where("user_id = ? AND profile_picture_url = ? AND (profile_picture_key = '' OR profile_picture_key IS NULL)", userID, reference).Updates(map[string]any{"profile_picture_key": key, "profile_picture_url": ""})
		if result.Error != nil {
			return errStorageMigration
		}
		if result.RowsAffected == 0 {
			counts.Conflicts++
		} else {
			counts.Migrated++
		}
		return nil
	}
	for _, profile := range drivers {
		if err := migratePhoto(&models.DriverProfile{}, profile.UserID, profile.ProfilePictureURL, profile.ProfilePictureKey); err != nil {
			return counts, err
		}
	}
	for _, profile := range passengers {
		if err := migratePhoto(&models.PassengerProfile{}, profile.UserID, profile.ProfilePictureURL, profile.ProfilePictureKey); err != nil {
			return counts, err
		}
	}
	return counts, nil
}

func migrationRelativePath(root, source string) (string, error) {
	if source == "" {
		return "", errMigrationSource
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return "", errMigrationSource
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", errMigrationSource
	}
	rel = filepath.ToSlash(rel)
	if err := storage.ValidateKey(rel); err != nil {
		return "", errMigrationSource
	}
	return rel, nil
}

func migrationPhotoPath(reference string) (string, bool, error) {
	u, err := url.Parse(reference)
	if err != nil {
		return "", false, errMigrationSource
	}
	if u.Scheme != "" || u.Host != "" {
		return "", true, nil
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/api/v1/uploads/") {
		return "", false, errMigrationSource
	}
	key := strings.TrimPrefix(u.Path, "/api/v1/uploads/")
	if err := storage.ValidateKey(key); err != nil {
		return "", false, errMigrationSource
	}
	return "public/" + key, false, nil
}

func migrationReadSource(root *os.Root, rel string, maxSize int64, visibility storage.Visibility) ([]byte, string, error) {
	if err := storage.ValidateKey(rel); err != nil {
		return nil, "", errMigrationSource
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		info, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil {
			return nil, "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, "", errMigrationSource
		}
	}
	f, err := root.Open(filepath.FromSlash(rel))
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxSize {
		return nil, "", errMigrationSource
	}
	payload, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil || int64(len(payload)) != info.Size() {
		return nil, "", errMigrationSource
	}
	contentType := http.DetectContentType(payload)
	if migrationExtension(contentType) == "" || visibility == storage.Public && contentType == "application/pdf" {
		return nil, "", errMigrationSource
	}
	return payload, contentType, nil
}

func migrationExtension(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "application/pdf":
		return ".pdf"
	}
	return ""
}
