package store

import "ride-backend/models"

// The ship run excludes rewriting account export/anonymisation modules. Block
// automatic deletion of object-backed accounts until that module can delete
// remote objects, rather than silently losing object references or claiming
// that only clearing a local path erased the stored images/documents.
func init() {
	RegisterDeletionBlocker("object_storage_cleanup", func(s *Store, id string) ([]string, error) {
		var count int64
		for _, q := range []struct {
			model             any
			column, condition string
		}{
			{&models.DriverDocument{}, "driver_id", "object_key <> '' OR staging_object_key <> ''"},
			{&models.DriverProfile{}, "user_id", "profile_picture_key <> ''"},
			{&models.PassengerProfile{}, "user_id", "profile_picture_key <> ''"},
		} {
			var n int64
			if err := s.db.Model(q.model).Where(q.column+" = ?", id).Where("(" + q.condition + ")").Count(&n).Error; err != nil {
				return nil, err
			}
			count += n
		}
		if count > 0 {
			return []string{"object storage cleanup/export adapter requires operator review"}, nil
		}
		return nil, nil
	})
}
