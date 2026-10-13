package store

import "ride-backend/models"

func (s *Store) RatingSummary(userID string) (float64, int64, error) {
	var result struct {
		Average float64
		Total   int64
	}
	err := s.db.Model(&models.Rating{}).Select("COALESCE(AVG(score),0) AS average,COUNT(*) AS total").Where("to_user_id = ?", userID).Scan(&result).Error
	return result.Average, result.Total, err
}

func (s *Store) RatingsPage(userID string, p Page) ([]*models.Rating, float64, int64, error) {
	avg, total, err := s.RatingSummary(userID)
	if err != nil {
		return nil, 0, 0, err
	}
	rows := []*models.Rating{}
	err = s.db.Where("to_user_id = ?", userID).Order("created_at DESC,id DESC").Limit(p.Limit).Offset(p.Offset).Find(&rows).Error
	return rows, avg, total, err
}
