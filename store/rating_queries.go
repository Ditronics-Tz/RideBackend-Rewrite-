package store

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/models"
)

func (s *Store) RatingSummary(userID string) (float64, int64, error) {
	var result struct {
		Average float64
		Total   int64
	}
	err := s.db.Model(&models.Rating{}).Select("COALESCE(AVG(score),0) AS average,COUNT(*) AS total").Where("to_user_id = ? AND hidden_at IS NULL", userID).Scan(&result).Error
	return result.Average, result.Total, err
}

func (s *Store) RatingsPage(userID string, p Page) ([]*models.Rating, float64, int64, error) {
	avg, total, err := s.RatingSummary(userID)
	if err != nil {
		return nil, 0, 0, err
	}
	rows := []*models.Rating{}
	err = s.db.Where("to_user_id = ? AND hidden_at IS NULL", userID).Order("created_at DESC,id DESC").Limit(p.Limit).Offset(p.Offset).Find(&rows).Error
	return rows, avg, total, err
}

func (s *Store) RateCompletedRide(ctx context.Context, userID, rideID string, input models.RateRideInput) (*models.Rating, error) {
	input.Comment = strings.TrimSpace(input.Comment)
	if input.Score < 1 || input.Score > 5 {
		return nil, FieldError("score", "score must be between 1 and 5")
	}
	if len([]rune(input.Comment)) > 1000 {
		return nil, FieldError("comment", "comment must be at most 1000 characters")
	}
	if _, err := uuid.Parse(rideID); err != nil {
		return nil, FieldError("id", "valid ride UUID required")
	}
	var rating models.Rating
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := lockRideActor(tx, userID)
		if err != nil {
			return err
		}
		if user.Role != models.RolePassenger && user.Role != models.RoleDriver {
			return OpError(403, "Ride participant account required")
		}
		var ride models.Ride
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&ride, "id = ?", rideID).Error; err != nil {
			return OpError(404, "Completed ride not found")
		}
		if ride.Status != "completed" || ride.PassengerID != userID && ride.DriverID != userID {
			return OpError(403, "Only participants in a completed ride can rate")
		}
		toUser := ride.PassengerID
		if userID == ride.PassengerID {
			toUser = ride.DriverID
		}
		var existing int64
		if err := tx.Model(&models.Rating{}).Where("ride_id = ? AND from_user_id = ?", rideID, userID).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return OpError(409, "You already rated this ride")
		}
		rating = models.Rating{ID: newID(), RideID: &rideID, FromUserID: userID, ToUserID: toUser, Score: input.Score, Comment: input.Comment, CreatedAt: s.now()}
		if err := tx.Create(&rating).Error; err != nil {
			return err
		}
		return txStore(tx, s).QueueFraudEvent(toUser, "rating_created", nil, "rating:"+rating.ID)
	})
	if err != nil {
		return nil, rideOperationError(err)
	}
	s.recompute(rating.ToUserID)
	return &rating, nil
}

func txStore(tx *gorm.DB, base *Store) *Store {
	scoped := *base
	scoped.db = tx
	return &scoped
}

type AdminRatingFilter struct {
	User     string
	Q        string
	MaxScore int
}

func (s *Store) AdminRatings(hidden string, filter AdminRatingFilter, page Page) ([]models.Rating, int64, error) {
	if page.Limit == 0 {
		page.Limit = 50
	}
	if page.Limit < 1 || page.Limit > 200 || page.Offset < 0 {
		return nil, 0, FieldError("pagination", "limit must be 1-200 and offset non-negative")
	}
	if filter.MaxScore < 0 || filter.MaxScore > 5 {
		return nil, 0, FieldError("max_score", "max_score must be between 0 and 5")
	}
	q := s.db.Model(&models.Rating{})
	if hidden == "true" {
		q = q.Where("hidden_at IS NOT NULL")
	} else if hidden != "all" {
		q = q.Where("hidden_at IS NULL")
	}
	if filter.User != "" {
		q = q.Where("to_user_id = ?", filter.User)
	}
	if filter.MaxScore > 0 {
		q = q.Where("score <= ?", filter.MaxScore)
	}
	if needle := strings.ToLower(strings.TrimSpace(filter.Q)); needle != "" {
		like := "%" + needle + "%"
		q = q.Where("LOWER(comment) LIKE ?", like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, OpError(503, "Ratings query unavailable")
	}
	rows := []models.Rating{}
	if err := q.Order("created_at DESC, id DESC").Limit(page.Limit).Offset(page.Offset).Find(&rows).Error; err != nil {
		return nil, 0, OpError(503, "Ratings query unavailable")
	}
	for i := range rows {
		rows[i].Hidden = rows[i].HiddenAt != nil
	}
	return rows, total, nil
}

func (s *Store) ModerateRating(id, staffID string, hidden bool, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return FieldError("reason", "reason must contain 1-500 characters")
	}
	var rating models.Rating
	err := s.Transaction(func(tx *Store) error {
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&rating, "id = ?", id).Error; err != nil {
			return OpError(404, "Rating not found")
		}
		updates := map[string]any{"hidden_at": nil, "hidden_by": nil}
		if hidden {
			now := tx.now()
			updates["hidden_at"], updates["hidden_by"] = &now, &staffID
		}
		if err := tx.db.Model(&rating).Updates(updates).Error; err != nil {
			return err
		}
		action := "rating_restore"
		if hidden {
			action = "rating_hide"
		}
		return tx.CreateAuditLog(staffID, action, "rating", id, "", map[string]string{"reason": reason})
	})
	if err != nil {
		return rideOperationError(err)
	}
	s.recompute(rating.ToUserID)
	return nil
}
