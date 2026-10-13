package store

import (
	"errors"
	"gorm.io/gorm"
	"ride-backend/config"
	"ride-backend/models"
	"ride-backend/storage"
)

func (s *Store) ConfigureStorage(cfg *config.Config) error {
	if s.Storage != nil {
		return nil
	}
	if cfg.StorageDriver == "r2" {
		provider, err := storage.NewR2(storage.R2Config{AccountID: cfg.R2AccountID, AccessKeyID: cfg.R2AccessKeyID, SecretAccessKey: cfg.R2SecretAccessKey, PublicBucket: cfg.R2PublicBucket, PrivateBucket: cfg.R2PrivateBucket, PublicBaseURL: cfg.R2PublicBaseURL})
		if err != nil {
			return err
		}
		s.Storage = provider
		return nil
	}
	provider, err := storage.NewLocal(storage.LocalConfig{PublicDir: "uploads/public", PrivateDir: "uploads/private/objects", PublicBaseURL: "/api/v1/uploads"})
	if err != nil {
		return err
	}
	s.Storage = provider
	return nil
}

func (s *Store) PublicProfilePictureKey(id string) (string, error) {
	var driver models.DriverProfile
	err := s.db.Select("user_id,profile_picture_key").First(&driver, "user_id = ?", id).Error
	if err == nil && driver.ProfilePictureKey != "" {
		return driver.ProfilePictureKey, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	var passenger models.PassengerProfile
	if err := s.db.Select("user_id,profile_picture_key").First(&passenger, "user_id = ?", id).Error; err != nil {
		return "", err
	}
	return passenger.ProfilePictureKey, nil
}
