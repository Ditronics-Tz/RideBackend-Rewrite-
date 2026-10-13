package store

import (
	"gorm.io/gorm/clause"
	"ride-backend/models"
	"strings"
	"time"
)

var reportCategories = map[string]bool{"unsafe_driving": true, "harassment": true, "no_show": true, "fare_dispute": true, "vehicle_mismatch": true, "discrimination": true, "fraud": true, "other": true}

func (s *Store) BlockUser(blocker, blocked string) error {
	return s.Transaction(func(tx *Store) error {
		if err := tx.sharedRide(blocker, blocked, ""); err != nil {
			return err
		}
		return tx.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.UserBlock{BlockerID: blocker, BlockedID: blocked, CreatedAt: tx.now()}).Error
	})
}
func (s *Store) UnblockUser(blocker, blocked string) error {
	return s.db.Where("blocker_id = ? AND blocked_id = ?", blocker, blocked).Delete(&models.UserBlock{}).Error
}
func (s *Store) SubmitReport(reporter string, r *models.Report, threshold, days int) error {
	if !reportCategories[r.Category] {
		return FieldError("category", "invalid report category")
	}
	if strings.TrimSpace(r.Description) == "" || len([]rune(r.Description)) > 1000 {
		return FieldError("description", "description must contain 1-1000 characters")
	}
	if r.RideID == "" {
		return FieldError("ride_id", "ride_id is required")
	}
	return s.Transaction(func(tx *Store) error {
		var u models.User
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", reporter).Error; err != nil {
			return err
		}
		if err := tx.sharedRide(reporter, r.ReportedID, r.RideID); err != nil {
			return err
		}
		var n int64
		if err := tx.db.Model(&models.Report{}).Where("reporter_id = ? AND reported_id = ? AND ride_id = ?", reporter, r.ReportedID, r.RideID).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return OpError(409, "this ride has already been reported")
		}
		if err := tx.db.Model(&models.Report{}).Where("reporter_id = ? AND created_at > ?", reporter, tx.now().Add(-24*time.Hour)).Count(&n).Error; err != nil {
			return err
		}
		if n >= 10 {
			return &OperationError{Status: 429, Message: "daily report limit reached", RetryAfter: 86400}
		}
		r.ID = newID()
		r.ReporterID = reporter
		r.Status = "open"
		r.Priority = "normal"
		r.AssignedTo = nil
		r.ResolutionNote = ""
		r.ResolutionOutcome = ""
		r.ResolvedBy = nil
		r.ResolvedAt = nil
		r.CreatedAt = tx.now()
		if err := tx.db.Create(r).Error; err != nil {
			return err
		}
		// Lock the target so concurrent distinct reporters cannot miss the threshold.
		u = models.User{}
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", r.ReportedID).Error; err != nil {
			return err
		}
		if threshold < 1 {
			threshold = 3
		}
		if days < 1 {
			days = 30
		}
		if err := tx.db.Model(&models.Report{}).Where("reported_id = ? AND created_at >= ?", r.ReportedID, tx.now().AddDate(0, 0, -days)).Distinct("reporter_id").Count(&n).Error; err != nil {
			return err
		}
		if n >= int64(threshold) {
			flag := models.AccountFlag{ID: newID(), UserID: r.ReportedID, Reason: "multiple_distinct_reports", Source: "reports", Status: "open", CreatedAt: tx.now()}
			result := tx.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&flag)
			if result.Error != nil {
				return result.Error
			}
			if err := tx.db.Model(&models.Report{}).Where("reported_id = ? AND status IN ?", r.ReportedID, []string{"open", "in_review"}).Update("priority", "high").Error; err != nil {
				return err
			}
			if result.RowsAffected > 0 {
				if err := tx.enqueue("staff:support", "account_flag", "", ""); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

type ReportFilter struct{ Status, Category, Assignee, Priority, User, Before string }

func (s *Store) Reports(f ReportFilter, p Page) ([]models.Report, int64, error) {
	q := s.db.Model(&models.Report{})
	for col, v := range map[string]string{"status": f.Status, "category": f.Category, "assigned_to": f.Assignee, "priority": f.Priority} {
		if v != "" {
			if col == "status" && v == "resolved" {
				q = q.Where("status IN ?", []string{"actioned", "dismissed"})
			} else {
				q = q.Where(col+" = ?", v)
			}
		}
	}
	if f.User != "" {
		q = q.Where("(reporter_id = ? OR reported_id = ?)", f.User, f.User)
	}
	if f.Before != "" {
		t, err := time.Parse(time.RFC3339, f.Before)
		if err != nil {
			return nil, 0, FieldError("before", "before must be RFC3339")
		}
		q = q.Where("created_at < ?", t)
	}
	rows := []models.Report{}
	total, err := s.PageRows(q, p, &rows)
	return rows, total, err
}

type PartySummary struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	Role            models.Role `json:"role"`
	Status          string      `json:"status"`
	AverageRating   float64     `json:"average_rating"`
	ReportsReceived int64       `json:"reports_received"`
	ReportsMade     int64       `json:"reports_made"`
}

func (s *Store) Party(id string) (*PartySummary, error) {
	var u models.User
	if err := s.db.First(&u, "id = ?", id).Error; err != nil {
		return nil, err
	}
	out := &PartySummary{ID: id, Name: u.Name, Role: u.Role, Status: u.Status}
	if err := s.db.Model(&models.Rating{}).Where("to_user_id = ?", id).Select("COALESCE(AVG(score),0)").Scan(&out.AverageRating).Error; err != nil {
		return nil, err
	}
	if err := s.db.Model(&models.Report{}).Where("reported_id = ?", id).Count(&out.ReportsReceived).Error; err != nil {
		return nil, err
	}
	if err := s.db.Model(&models.Report{}).Where("reporter_id = ?", id).Count(&out.ReportsMade).Error; err != nil {
		return nil, err
	}
	return out, nil
}
func (s *Store) ReportDetail(id string, p Page) (map[string]any, error) {
	var r models.Report
	if err := s.db.First(&r, "id = ?", id).Error; err != nil {
		return nil, err
	}
	a, err := s.Party(r.ReporterID)
	if err != nil {
		return nil, err
	}
	b, err := s.Party(r.ReportedID)
	if err != nil {
		return nil, err
	}
	var ride models.Ride
	if err := s.db.First(&ride, "id = ?", r.RideID).Error; err != nil {
		return nil, err
	}
	notes := []models.ReportNote{}
	total, err := s.PageRows(s.db.Model(&models.ReportNote{}).Where("report_id = ?", id), p, &notes)
	if err != nil {
		return nil, err
	}
	return map[string]any{"report": r, "reporter": a, "reported": b, "ride": ride, "notes": notes, "notes_total": total}, nil
}

type ResolveReportInput struct {
	Outcome  string     `json:"outcome"`
	Note     string     `json:"note"`
	Until    *time.Time `json:"until"`
	Override bool       `json:"override"`
}

func (s *Store) WorkReport(id, action string, actor *models.StaffUser, note, assignee string, input ResolveReportInput) error {
	return s.Transaction(func(tx *Store) error {
		var r models.Report
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&r, "id = ?", id).Error; err != nil {
			return err
		}
		if action == "assign" {
			if assignee == "" {
				assignee = actor.ID
			}
			if actor.Role != models.StaffRoleAdmin && assignee != actor.ID {
				return OpError(403, "support may only assign to themselves")
			}
			var st models.StaffUser
			if err := tx.db.First(&st, "id = ? AND is_active = ?", assignee, true).Error; err != nil {
				return err
			}
			if r.Status != "open" && r.Status != "in_review" {
				return OpError(409, "report is already resolved")
			}
			if r.AssignedTo != nil && *r.AssignedTo != actor.ID && actor.Role != models.StaffRoleAdmin {
				return OpError(409, "report is held by another reviewer")
			}
			if err := tx.db.Model(&r).Updates(map[string]any{"assigned_to": assignee, "status": "in_review"}).Error; err != nil {
				return err
			}
		} else if action == "note" {
			if err := cleanNote(note, 4000); err != nil {
				return err
			}
			if err := tx.db.Create(&models.ReportNote{ID: newID(), ReportID: id, StaffID: actor.ID, Note: note, CreatedAt: tx.now()}).Error; err != nil {
				return err
			}
		} else if action == "resolve" {
			if err := cleanNote(input.Note, 2000); err != nil {
				return err
			}
			if r.Status == "actioned" || r.Status == "dismissed" {
				if actor.Role != models.StaffRoleAdmin || !input.Override {
					return OpError(409, "only an admin may explicitly override a resolution")
				}
			}
			if r.AssignedTo != nil && *r.AssignedTo != actor.ID && actor.Role != models.StaffRoleAdmin {
				return OpError(409, "report is held by another reviewer")
			}
			status := "actioned"
			switch input.Outcome {
			case "dismissed":
				status = "dismissed"
			case "warning":
				if err := tx.enqueue(r.ReportedID, "report_warning", "", ""); err != nil {
					return err
				}
			case "suspended":
				if actor.Role != models.StaffRoleAdmin && (input.Until == nil || input.Until.After(tx.now().Add(7*24*time.Hour))) {
					return OpError(403, "support suspension must end within 7 days")
				}
				if err := tx.ChangeAccountStatus(r.ReportedID, actor, "suspended", input.Note, input.Until); err != nil {
					return err
				}
			case "banned":
				if actor.Role != models.StaffRoleAdmin {
					return OpError(403, "only admins may ban")
				}
				if err := tx.ChangeAccountStatus(r.ReportedID, actor, "banned", input.Note, nil); err != nil {
					return err
				}
			case "escalated":
				status = "in_review"
				if err := tx.enqueue("staff:admin", "report_escalated", "", ""); err != nil {
					return err
				}
			default:
				return FieldError("outcome", "invalid resolution outcome")
			}
			if err := tx.db.Model(&r).Updates(map[string]any{"status": status, "resolution_outcome": input.Outcome, "resolution_note": input.Note, "resolved_by": actor.ID, "resolved_at": tx.now()}).Error; err != nil {
				return err
			}
		} else {
			return OpError(404, "unknown action")
		}
		return tx.CreateAuditLog(actor.ID, "report_"+action, "report", id, "", map[string]any{"outcome": input.Outcome})
	})
}
func (s *Store) ResolveAccountFlag(id string, actor *models.StaffUser) error {
	return s.Transaction(func(tx *Store) error {
		r := tx.db.Model(&models.AccountFlag{}).Where("id = ? AND status = ?", id, "open").Update("status", "resolved")
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return OpError(409, "flag is not open")
		}
		return tx.CreateAuditLog(actor.ID, "resolve_account_flag", "account_flag", id, "", nil)
	})
}
