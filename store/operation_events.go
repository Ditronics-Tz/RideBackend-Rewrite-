package store

import (
	"context"
	"time"

	"ride-backend/models"

	"gorm.io/gorm"
)

type OperationFilter struct {
	Action, ActorType, ActorID, From, To, RequestID string
}

// RecordOperationEvent accepts only metadata assembled by the server. Callers
// must never pass request bodies, query strings, headers or concrete URL paths.
func (s *Store) RecordOperationEvent(ctx context.Context, event models.OperationEvent) error {
	event.ID = newID()
	event.CreatedAt = s.now().UTC()
	if err := s.db.WithContext(ctx).Create(&event).Error; err != nil {
		return ErrAuthUnavailable
	}
	return nil
}

func (s *Store) SearchOperationEvents(ctx context.Context, f OperationFilter, p Page) ([]models.OperationEvent, int64, error) {
	p, err := authPage(p)
	if err != nil {
		return nil, 0, err
	}
	q := s.db.WithContext(ctx).Model(&models.OperationEvent{})
	for _, field := range []struct{ name, value string }{{"action", f.Action}, {"actor_type", f.ActorType}, {"actor_id", f.ActorID}, {"request_id", f.RequestID}} {
		if field.value != "" {
			q = q.Where(field.name+" = ?", field.value)
		}
	}
	for _, bound := range []struct{ name, value, op string }{{"from", f.From, ">="}, {"to", f.To, "<"}} {
		if bound.value == "" {
			continue
		}
		date, err := time.ParseInLocation("2006-01-02", bound.value, time.FixedZone("EAT", 3*60*60))
		if err != nil {
			return nil, 0, FieldError(bound.name, "date must be YYYY-MM-DD")
		}
		if bound.name == "to" {
			date = date.AddDate(0, 0, 1)
		}
		q = q.Where("created_at "+bound.op+" ?", date)
	}
	var total int64
	rows := []models.OperationEvent{}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, ErrAuthUnavailable
	}
	if err := q.Order("created_at DESC, id DESC").Limit(p.Limit).Offset(p.Offset).Find(&rows).Error; err != nil {
		return nil, 0, ErrAuthUnavailable
	}
	return rows, total, nil
}

func init() {
	RegisterDataExporter(func(s *Store) DataExporter { return operationEventExporter{s} })
	RegisterAnonymizer(operationEventAnonymizer{})
}

type operationEventExporter struct{ s *Store }

func (operationEventExporter) Name() string     { return "operation_events" }
func (operationEventExporter) Tables() []string { return []string{"operation_events"} }
func (e operationEventExporter) Export(userID string) (any, []string, error) {
	rows := []models.OperationEvent{}
	if err := e.s.db.Where("actor_type = ? AND actor_id = ? OR target_id = ?", "app", userID, userID).Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, nil, ErrAuthUnavailable
	}
	return rows, nil, nil
}

type operationEventAnonymizer struct{}

func (operationEventAnonymizer) Name() string     { return "operation_events" }
func (operationEventAnonymizer) Tables() []string { return []string{"operation_events"} }
func (operationEventAnonymizer) Description() string {
	return "removes deleted user IDs from operation history; retains action, route template, outcome and timestamp"
}
func (operationEventAnonymizer) Anonymize(tx *gorm.DB, userID string) error {
	if err := tx.Model(&models.OperationEvent{}).Where("actor_type = ? AND actor_id = ?", "app", userID).Update("actor_id", "").Error; err != nil {
		return err
	}
	return tx.Model(&models.OperationEvent{}).Where("target_id = ?", userID).Update("target_id", "").Error
}
