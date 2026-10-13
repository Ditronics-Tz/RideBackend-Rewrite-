package store

import (
	"strings"
	"time"

	"gorm.io/gorm/clause"
	"ride-backend/models"
)

var supportTicketCategories = map[string]bool{"account": true, "ride": true, "payment": true, "safety": true, "other": true}
var supportTicketStatuses = map[string]bool{"open": true, "in_review": true, "resolved": true, "closed": true}
var supportTicketPriorities = map[string]bool{"low": true, "normal": true, "high": true, "urgent": true}

func (s *Store) CreateSupportTicket(userID string, input models.CreateSupportTicketInput) (*models.SupportTicket, error) {
	input.Subject = strings.TrimSpace(input.Subject)
	input.Description = strings.TrimSpace(input.Description)
	input.Category = strings.TrimSpace(input.Category)
	if input.Subject == "" || len([]rune(input.Subject)) > 160 {
		return nil, FieldError("subject", "subject must contain 1-160 characters")
	}
	if input.Description == "" || len([]rune(input.Description)) > 4000 {
		return nil, FieldError("description", "description must contain 1-4000 characters")
	}
	if !supportTicketCategories[input.Category] {
		return nil, FieldError("category", "category must be account, ride, payment, safety or other")
	}
	if _, err := s.ActiveAccount(userID); err != nil {
		return nil, OpError(403, "Active account required")
	}
	now := s.now()
	ticket := &models.SupportTicket{ID: newID(), UserID: userID, Subject: input.Subject, Description: input.Description, Category: input.Category, Status: "open", Priority: "normal", CreatedAt: now, UpdatedAt: now}
	if err := s.db.Create(ticket).Error; err != nil {
		return nil, OpError(503, "Unable to create support ticket")
	}
	return ticket, nil
}

type SupportTicketFilter struct{ Status, Category, Priority, Assignee, User, Q, From, To string }

func (s *Store) ListSupportTickets(filter SupportTicketFilter, page Page) ([]models.SupportTicket, int64, error) {
	if page.Limit == 0 {
		page.Limit = 50
	}
	if page.Limit < 1 || page.Limit > 200 || page.Offset < 0 {
		return nil, 0, FieldError("pagination", "limit must be 1-200 and offset non-negative")
	}
	q := s.db.Model(&models.SupportTicket{})
	for column, value := range map[string]string{"status": filter.Status, "category": filter.Category, "priority": filter.Priority, "assigned_to": filter.Assignee, "user_id": filter.User} {
		if value != "" {
			q = q.Where(column+" = ?", value)
		}
	}
	if filter.Q != "" {
		needle := "%" + strings.ToLower(strings.TrimSpace(filter.Q)) + "%"
		q = q.Where("(LOWER(subject) LIKE ? OR LOWER(description) LIKE ?)", needle, needle)
	}
	if filter.From != "" {
		from, err := time.Parse(time.RFC3339, filter.From)
		if err != nil {
			return nil, 0, FieldError("from", "from must be RFC3339")
		}
		q = q.Where("created_at >= ?", from)
	}
	if filter.To != "" {
		to, err := time.Parse(time.RFC3339, filter.To)
		if err != nil {
			return nil, 0, FieldError("to", "to must be RFC3339")
		}
		q = q.Where("created_at < ?", to)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, OpError(503, "Support ticket query unavailable")
	}
	rows := []models.SupportTicket{}
	if err := q.Order("created_at DESC, id DESC").Limit(page.Limit).Offset(page.Offset).Find(&rows).Error; err != nil {
		return nil, 0, OpError(503, "Support ticket query unavailable")
	}
	return rows, total, nil
}

func (s *Store) SupportTicketDetail(id string, page Page) (map[string]any, error) {
	var ticket models.SupportTicket
	if err := s.db.First(&ticket, "id = ?", id).Error; err != nil {
		return nil, OpError(404, "Ticket not found")
	}
	if page.Limit == 0 {
		page.Limit = 50
	}
	notes := []models.SupportTicketNote{}
	total, err := s.PageRows(s.db.Model(&models.SupportTicketNote{}).Where("ticket_id = ?", id), page, &notes)
	if err != nil {
		return nil, OpError(503, "Ticket notes unavailable")
	}
	return map[string]any{"ticket": ticket, "notes": notes, "notes_total": total}, nil
}

type WorkSupportTicketInput struct {
	Action     string `json:"action"`
	Assignee   string `json:"assigned_to"`
	Reason     string `json:"reason"`
	Status     string `json:"status"`
	Priority   string `json:"priority"`
	Note       string `json:"note"`
	Outcome    string `json:"resolution_outcome"`
	Resolution string `json:"resolution_note"`
}

func (s *Store) WorkSupportTicket(id string, actor *models.StaffUser, input WorkSupportTicketInput) error {
	if actor == nil || actor.Role != models.StaffRoleAdmin && actor.Role != models.StaffRoleSupport {
		return OpError(403, "Support or admin role required")
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if len([]rune(input.Reason)) > 500 {
		return FieldError("reason", "reason must be at most 500 characters")
	}
	return s.Transaction(func(tx *Store) error {
		var ticket models.SupportTicket
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&ticket, "id = ?", id).Error; err != nil {
			return OpError(404, "Ticket not found")
		}
		if ticket.AssignedTo != nil && *ticket.AssignedTo != actor.ID && actor.Role != models.StaffRoleAdmin {
			return OpError(409, "Ticket is assigned to another reviewer")
		}
		now := tx.now()
		updates := map[string]any{"updated_at": now}
		switch input.Action {
		case "assign":
			assignee := strings.TrimSpace(input.Assignee)
			if assignee == "" {
				assignee = actor.ID
			}
			if actor.Role != models.StaffRoleAdmin && assignee != actor.ID {
				return OpError(403, "Support may only assign tickets to themselves")
			}
			var staff models.StaffUser
			if err := tx.db.First(&staff, "id = ? AND is_active = ?", assignee, true).Error; err != nil || staff.Role != models.StaffRoleSupport && staff.Role != models.StaffRoleAdmin {
				return FieldError("assigned_to", "assignee must be active support or admin staff")
			}
			if ticket.Status == "resolved" || ticket.Status == "closed" {
				return OpError(409, "Resolved tickets cannot be assigned")
			}
			updates["assigned_to"], updates["status"] = assignee, "in_review"
		case "note":
			if err := cleanNote(input.Note, 4000); err != nil {
				return err
			}
			if err := tx.db.Create(&models.SupportTicketNote{ID: newID(), TicketID: ticket.ID, StaffID: actor.ID, Note: strings.TrimSpace(input.Note), CreatedAt: now}).Error; err != nil {
				return err
			}
		case "update":
			if input.Status != "" {
				if !supportTicketStatuses[input.Status] {
					return FieldError("status", "invalid ticket status")
				}
				if (ticket.Status == "resolved" || ticket.Status == "closed") && input.Status != ticket.Status && actor.Role != models.StaffRoleAdmin {
					return OpError(403, "Only admin may reopen a resolved ticket")
				}
				updates["status"] = input.Status
				if input.Status == "resolved" || input.Status == "closed" {
					if strings.TrimSpace(input.Outcome) == "" || strings.TrimSpace(input.Resolution) == "" {
						return FieldError("resolution_note", "resolution outcome and note are required to resolve a ticket")
					}
					if len([]rune(input.Resolution)) > 2000 {
						return FieldError("resolution_note", "resolution note must be at most 2000 characters")
					}
					updates["resolution_outcome"], updates["resolution_note"] = strings.TrimSpace(input.Outcome), strings.TrimSpace(input.Resolution)
					updates["resolved_by"], updates["resolved_at"] = actor.ID, now
				} else {
					updates["resolved_by"], updates["resolved_at"] = nil, nil
				}
			}
			if input.Priority != "" {
				if !supportTicketPriorities[input.Priority] {
					return FieldError("priority", "invalid ticket priority")
				}
				updates["priority"] = input.Priority
			}
			if len(updates) == 1 {
				return FieldError("action", "ticket update requires status or priority")
			}
		default:
			return FieldError("action", "action must be assign, note or update")
		}
		if err := tx.db.Model(&ticket).Updates(updates).Error; err != nil {
			return err
		}
		return tx.CreateAuditLog(actor.ID, "ticket_"+input.Action, "ticket", ticket.ID, "", map[string]any{"status": input.Status, "priority": input.Priority, "reason": input.Reason})
	})
}
