package store

import (
	"errors"
	"gorm.io/gorm"
	"ride-backend/models"
	"strconv"
	"strings"
	"time"
)

type OperationError struct {
	Status     int
	Message    string
	Field      string
	RetryAfter int
}

func (e *OperationError) Error() string { return e.Message }
func OpError(status int, message string) error {
	return &OperationError{Status: status, Message: message}
}
func FieldError(field, message string) error {
	return &OperationError{Status: 400, Message: message, Field: field}
}

type Page struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func ParsePage(limit, offset string) (Page, error) {
	p := Page{Limit: 50}
	var err error
	if limit != "" {
		p.Limit, err = strconv.Atoi(limit)
		if err != nil || p.Limit < 1 || p.Limit > 200 {
			return p, FieldError("limit", "limit must be 1-200")
		}
	}
	if offset != "" {
		p.Offset, err = strconv.Atoi(offset)
		if err != nil || p.Offset < 0 {
			return p, FieldError("offset", "offset must be non-negative")
		}
	}
	return p, nil
}
func (s *Store) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}
func (s *Store) PageRows(q *gorm.DB, p Page, out any) (int64, error) {
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return 0, err
	}
	return total, q.Order("created_at DESC, id DESC").Limit(p.Limit).Offset(p.Offset).Find(out).Error
}
func cleanNote(note string, max int) error {
	if strings.TrimSpace(note) == "" || len([]rune(note)) > max {
		return FieldError("note", "a note is required and is too long if above the documented limit")
	}
	return nil
}
func (s *Store) RideForUser(id, user string) (*models.Ride, error) {
	var r models.Ride
	if err := s.db.Where("id = ? AND (passenger_id = ? OR driver_id = ?)", id, user, user).First(&r).Error; err != nil {
		return nil, err
	}
	return &r, nil
}
func (s *Store) sharedRide(a, b, ride string) error {
	if a == b {
		return FieldError("user_id", "you cannot report or block yourself")
	}
	q := s.db.Model(&models.Ride{}).Where("((passenger_id = ? AND driver_id = ?) OR (passenger_id = ? AND driver_id = ?)) AND status IN ?", a, b, b, a, []string{"assigned", "in_progress", "completed", "cancelled"})
	if ride != "" {
		q = q.Where("id = ?", ride)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return OpError(403, "only people who shared a ride can be blocked or reported")
	}
	return nil
}
func (s *Store) CheckRideEligibility(passenger, driver string) error {
	for _, id := range []string{passenger, driver} {
		if _, err := s.ActiveAccount(id); err != nil {
			return OpError(403, "account unavailable")
		}
	}
	var p models.DriverProfile
	if err := s.db.First(&p, "user_id = ?", driver).Error; err != nil {
		return err
	}
	if p.VerificationStatus != models.Approved {
		return OpError(403, "driver approval required")
	}
	var count int64
	if err := s.db.Model(&models.UserBlock{}).Where("(blocker_id = ? AND blocked_id = ?) OR (blocker_id = ? AND blocked_id = ?)", passenger, driver, driver, passenger).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return OpError(409, "pair is unavailable for matching")
	}
	return s.checkFinancialEligibility(driver)
}

func (s *Store) enqueue(user, template, phone, link string) error {
	return s.db.Create(&models.NotificationDelivery{ID: newID(), UserID: user, Template: template, Language: "sw", Phone: phone, Link: link, Status: "pending", CreatedAt: s.now()}).Error
}
func notFound(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }
