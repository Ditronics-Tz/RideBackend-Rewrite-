package store

import (
	"strings"
	"time"

	"ride-backend/models"
)

func (s *Store) UserCounts() (drivers, passengers int64, err error) {
	if err = s.db.Model(&models.User{}).Where("role = ?", models.RoleDriver).Count(&drivers).Error; err != nil {
		return
	}
	err = s.db.Model(&models.User{}).Where("role = ?", models.RolePassenger).Count(&passengers).Error
	return
}

func (s *Store) SearchUsersFiltered(role, search, status, verification, createdFrom, createdTo string, p Page) ([]*models.User, int64, error) {
	q := s.db.Model(&models.User{})
	if role != "" {
		q = q.Where("role = ?", role)
	}
	if status != "" {
		switch status {
		case "active", "suspended", "banned", "pending_deletion", "deleted":
			q = q.Where("status = ?", status)
		case "inactive", "deactivated":
			q = q.Where("is_active = ?", false)
		default:
			return nil, 0, FieldError("status", "invalid account status")
		}
	}
	if verification != "" {
		switch models.VerificationStatus(verification) {
		case models.PendingDocuments, models.Submitted, models.UnderReview, models.Approved, models.Rejected, models.Suspended, models.Expired:
		default:
			return nil, 0, FieldError("verification_status", "invalid verification status")
		}
		q = q.Where("id IN (?)", s.db.Model(&models.DriverProfile{}).Select("user_id").Where("verification_status = ?", verification))
	}
	var from, to *time.Time
	if createdFrom != "" {
		parsed, err := time.Parse(time.RFC3339, createdFrom)
		if err != nil {
			return nil, 0, FieldError("created_from", "created_from must be RFC3339")
		}
		from = &parsed
		q = q.Where("created_at >= ?", parsed)
	}
	if createdTo != "" {
		parsed, err := time.Parse(time.RFC3339, createdTo)
		if err != nil {
			return nil, 0, FieldError("created_to", "created_to must be RFC3339")
		}
		to = &parsed
		q = q.Where("created_at <= ?", parsed)
	}
	if from != nil && to != nil && !to.After(*from) {
		return nil, 0, FieldError("created_to", "created_to must be later than created_from")
	}
	if search = strings.ToLower(strings.TrimSpace(search)); search != "" {
		like := "%" + search + "%"
		q = q.Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ? OR phone LIKE ? OR id IN (?)", like, like, like, s.db.Model(&models.DriverProfile{}).Select("user_id").Where("plate_number LIKE ?", "%"+CanonicalIdentifier(search)+"%"))
	}
	rows := []*models.User{}
	total, err := s.PageRows(q, p, &rows)
	return rows, total, err
}

type AdminDailyCount struct {
	Day   string `gorm:"column:day"`
	Key   string `gorm:"column:key"`
	Count int64  `gorm:"column:count"`
}

func (s *Store) adminDateExpression(column string) string {
	if s.db.Dialector.Name() == "postgres" {
		return "TO_CHAR(" + column + " AT TIME ZONE 'UTC', 'YYYY-MM-DD')"
	}
	return "DATE(" + column + ")"
}

func (s *Store) AdminRegistrationCounts(from, until time.Time) ([]AdminDailyCount, error) {
	rows := []AdminDailyCount{}
	dateExpr := s.adminDateExpression("created_at")
	err := s.db.Model(&models.User{}).
		Select(dateExpr+" AS day, role AS key, COUNT(*) AS count").
		Where("created_at >= ? AND created_at < ?", from, until).
		Group("day, role").Order("day ASC").Scan(&rows).Error
	return rows, err
}

func (s *Store) AdminRideCounts(from, until time.Time) (requested, completed []AdminDailyCount, err error) {
	requested = []AdminDailyCount{}
	completed = []AdminDailyCount{}
	createdExpr := s.adminDateExpression("created_at")
	err = s.db.Model(&models.Ride{}).
		Select(createdExpr+" AS day, status AS key, COUNT(*) AS count").
		Where("created_at >= ? AND created_at < ?", from, until).
		Group("day, status").Order("day ASC").Scan(&requested).Error
	if err != nil {
		return
	}
	completedExpr := s.adminDateExpression("completed_at")
	err = s.db.Model(&models.Ride{}).
		Select(completedExpr+" AS day, status AS key, COUNT(*) AS count").
		Where("status = ? AND completed_at >= ? AND completed_at < ?", "completed", from, until).
		Group("day, status").Order("day ASC").Scan(&completed).Error
	return
}

func (s *Store) AdminNotificationDeliveries(status string, p Page) ([]models.NotificationDelivery, int64, models.NotificationDeliveryCounts, error) {
	var counts models.NotificationDeliveryCounts
	for _, item := range []struct {
		status string
		dest   *int64
	}{{"pending", &counts.Pending}, {"processing", &counts.Processing}, {"delivered", &counts.Delivered}, {"failed", &counts.Failed}} {
		if err := s.db.Model(&models.NotificationDelivery{}).Where("status = ?", item.status).Count(item.dest).Error; err != nil {
			return nil, 0, counts, err
		}
	}
	q := s.db.Model(&models.NotificationDelivery{})
	if status != "" {
		switch status {
		case "pending", "processing", "delivered", "failed":
			q = q.Where("status = ?", status)
		default:
			return nil, 0, counts, FieldError("status", "status must be pending, processing, delivered, or failed")
		}
	}
	rows := []models.NotificationDelivery{}
	total, err := s.PageRows(q, p, &rows)
	return rows, total, counts, err
}

type AuditFilter struct{ Q, Action, Actor, From, To string }

func (s *Store) SearchAuditLogs(f AuditFilter, p Page) ([]*models.StaffAuditLog, int64, error) {
	q := s.db.Model(&models.StaffAuditLog{})
	if f.Action != "" {
		q = q.Where("action = ?", f.Action)
	}
	if f.Actor != "" {
		q = q.Where("actor_id = ?", f.Actor)
	}
	if f.Q != "" {
		q = q.Where("LOWER(action) LIKE ? OR LOWER(target_type) LIKE ?", "%"+strings.ToLower(f.Q)+"%", "%"+strings.ToLower(f.Q)+"%")
	}
	for _, bound := range []struct{ field, raw, op string }{{"from", f.From, ">="}, {"to", f.To, "<="}} {
		if bound.raw == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, bound.raw)
		if err != nil {
			t, err = time.Parse("2006-01-02", bound.raw)
			if bound.field == "to" {
				t = t.Add(24*time.Hour - time.Nanosecond)
			}
		}
		if err != nil {
			return nil, 0, FieldError(bound.field, "date must be YYYY-MM-DD or RFC3339")
		}
		q = q.Where("created_at "+bound.op+" ?", t)
	}
	rows := []*models.StaffAuditLog{}
	total, err := s.PageRows(q, p, &rows)
	return rows, total, err
}

func (s *Store) SearchStaff(search, role, status string, p Page) ([]*models.StaffUser, int64, error) {
	q := s.db.Model(&models.StaffUser{})
	if role != "" {
		if !models.IsValidStaffRole(models.StaffRole(role)) {
			return nil, 0, FieldError("role", "invalid staff role")
		}
		q = q.Where("role = ?", role)
	}
	if status != "" {
		if status != "active" && status != "inactive" {
			return nil, 0, FieldError("status", "status must be active or inactive")
		}
		q = q.Where("is_active = ?", status == "active")
	}
	if search != "" {
		like := "%" + strings.ToLower(strings.TrimSpace(search)) + "%"
		q = q.Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ?", like, like)
	}
	rows := []*models.StaffUser{}
	total, err := s.PageRows(q, p, &rows)
	return rows, total, err
}
