package store

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"ride-backend/models"
	"time"
)

func (s *Store) activeDriverTransport(tx *gorm.DB, driver string) (bool, error) {
	var n int64
	if err := tx.Model(&models.Ride{}).Where("driver_id=? AND status IN ?", driver, []string{"assigned", "driver_arrived", "in_progress"}).Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	w := *s
	w.db = tx
	return w.activeSharedDriver(driver, s.now().Unix())
}
func (s *Store) SharedDriverLocation(ctx context.Context, id, actor string) (*models.DriverLocation, error) {
	v, err := s.SharedDetail(id, actor, "")
	if err != nil {
		return nil, err
	}
	if v.DriverID != actor && (v.MySeat == nil || (v.MySeat.Status != "held" && v.MySeat.Status != "confirmed")) {
		return nil, OpError(404, "Trip not found")
	}
	if v.Status != "waiting" && v.Status != "in_progress" {
		return nil, nil
	}
	var p models.DriverLocation
	err = s.db.WithContext(ctx).Where("driver_id=? AND accuracy<=100 AND updated_at>=?", v.DriverID, s.now().Add(-2*time.Minute)).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}
