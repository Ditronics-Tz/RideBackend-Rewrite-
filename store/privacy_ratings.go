package store

import (
	"gorm.io/gorm"
	"ride-backend/models"
)

func init() {
	RegisterDataExporter(func(s *Store) DataExporter { return ratingExporter{s} })
	RegisterAnonymizer(ratingAnonymizer{})
}

type ratingExporter struct{ s *Store }

func (ratingExporter) Name() string     { return "ratings" }
func (ratingExporter) Tables() []string { return []string{"ratings"} }
func (e ratingExporter) Export(id string) (any, []string, error) {
	type item struct {
		Direction string      `json:"direction"`
		Score     int         `json:"score"`
		Comment   string      `json:"comment"`
		CreatedAt interface{} `json:"created_at"`
	}
	var rows []models.Rating
	if err := e.s.db.Where("from_user_id = ? OR to_user_id = ?", id, id).Order("created_at").Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	out := make([]item, 0, len(rows))
	for _, r := range rows {
		direction := "received"
		if r.FromUserID == id {
			direction = "given"
		}
		out = append(out, item{direction, r.Score, r.Comment, r.CreatedAt})
	}
	return map[string]any{"items": out}, nil, nil
}

type ratingAnonymizer struct{}

func (ratingAnonymizer) Name() string     { return "ratings" }
func (ratingAnonymizer) Tables() []string { return []string{"ratings"} }
func (ratingAnonymizer) Description() string {
	return "retains scores and dates for aggregate integrity; clears comments authored by or about the account"
}
func (ratingAnonymizer) Anonymize(tx *gorm.DB, id string) error {
	return tx.Model(&models.Rating{}).Where("from_user_id = ? OR to_user_id = ?", id, id).Update("comment", "").Error
}
