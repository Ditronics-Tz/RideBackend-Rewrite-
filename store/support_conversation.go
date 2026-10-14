package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/models"
	"ride-backend/storage"
)

const maxTicketAttachmentBytes = 5 << 20

func ticketForActor(tx *gorm.DB, ticketID, actorID string, staff bool) (*models.SupportTicket, error) {
	if _, err := uuid.Parse(ticketID); err != nil {
		return nil, FieldError("id", "valid ticket ID required")
	}
	q := tx.Where("id = ?", ticketID)
	if !staff {
		q = q.Where("user_id = ?", actorID)
	}
	var ticket models.SupportTicket
	if err := q.First(&ticket).Error; err != nil {
		return nil, OpError(404, "Ticket not found")
	}
	return &ticket, nil
}

func (s *Store) TicketConversation(ctx context.Context, ticketID, actorID string, staff bool, page Page) (map[string]any, error) {
	ticket, err := ticketForActor(s.db.WithContext(ctx), ticketID, actorID, staff)
	if err != nil {
		return nil, err
	}
	if page.Limit == 0 {
		page.Limit = 50
	}
	if page.Limit < 1 || page.Limit > 200 || page.Offset < 0 {
		return nil, FieldError("pagination", "limit must be 1-200 and offset non-negative")
	}
	var total int64
	q := s.db.WithContext(ctx).Model(&models.SupportTicketMessage{}).Where("ticket_id = ?", ticketID)
	if err := q.Count(&total).Error; err != nil {
		return nil, OpError(503, "Ticket conversation unavailable")
	}
	messages := []models.SupportTicketMessage{}
	if err := q.Order("created_at ASC, id ASC").Limit(page.Limit).Offset(page.Offset).Find(&messages).Error; err != nil {
		return nil, OpError(503, "Ticket conversation unavailable")
	}
	attachments := []models.SupportTicketAttachment{}
	if err := s.db.WithContext(ctx).Where("ticket_id = ?", ticketID).Order("created_at ASC, id ASC").Find(&attachments).Error; err != nil {
		return nil, OpError(503, "Ticket attachments unavailable")
	}
	return map[string]any{"ticket": ticket, "messages": messages, "messages_total": total, "attachments": attachments, "limit": page.Limit, "offset": page.Offset}, nil
}

func (s *Store) AddTicketMessage(ctx context.Context, ticketID, actorID, key, body string, staff bool) (*models.SupportTicketMessage, error) {
	if len(key) < 8 || len(key) > 100 {
		return nil, FieldError("Idempotency-Key", "Idempotency-Key must contain 8-100 characters")
	}
	body = strings.TrimSpace(body)
	if n := len([]rune(body)); n < 1 || n > 4000 {
		return nil, FieldError("body", "message must contain 1-4000 characters")
	}
	hash := tokenHash(body)
	var message models.SupportTicketMessage
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ticket, err := ticketForActor(tx.Clauses(clause.Locking{Strength: "UPDATE"}), ticketID, actorID, staff)
		if err != nil {
			return err
		}
		if err := tx.Where("ticket_id = ? AND actor_id = ? AND request_key = ?", ticketID, actorID, key).First(&message).Error; err == nil {
			if message.RequestHash != hash {
				return OpError(409, "Idempotency-Key was already used for another message")
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		actorType := "app"
		if staff {
			actorType = "staff"
		}
		message = models.SupportTicketMessage{ID: newID(), TicketID: ticketID, ActorID: actorID, ActorType: actorType, Body: body, RequestKey: key, RequestHash: hash, CreatedAt: s.now()}
		if err := tx.Create(&message).Error; err != nil {
			return err
		}
		updates := map[string]any{"updated_at": s.now()}
		if !staff && (ticket.Status == "resolved" || ticket.Status == "closed") {
			updates["status"], updates["resolved_by"], updates["resolved_at"] = "open", nil, nil
			updates["resolution_outcome"], updates["resolution_note"] = "", ""
		}
		if err := tx.Model(ticket).Updates(updates).Error; err != nil {
			return err
		}
		if staff {
			scoped := *s
			scoped.db = tx
			return scoped.CreateAuditLog(actorID, "ticket_message", "ticket", ticketID, "", map[string]any{"message_id": message.ID})
		}
		return nil
	})
	if err != nil {
		var op *OperationError
		if errors.As(err, &op) {
			return nil, err
		}
		return nil, OpError(503, "Ticket message unavailable")
	}
	return &message, nil
}

func (s *Store) UploadTicketAttachment(ctx context.Context, ticketID, actorID, key string, staff bool, body []byte) (*models.SupportTicketAttachment, error) {
	if len(key) < 8 || len(key) > 100 {
		return nil, FieldError("Idempotency-Key", "Idempotency-Key must contain 8-100 characters")
	}
	if len(body) == 0 || len(body) > maxTicketAttachmentBytes {
		return nil, FieldError("file", "file must be 1 byte to 5 MiB")
	}
	contentType := http.DetectContentType(body)
	if contentType != "image/jpeg" && contentType != "image/png" && contentType != "application/pdf" {
		return nil, FieldError("file", "JPEG, PNG or PDF required")
	}
	if s.Storage == nil {
		return nil, OpError(503, "Private file storage unavailable")
	}
	if _, err := ticketForActor(s.db.WithContext(ctx), ticketID, actorID, staff); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(body)
	hash := hex.EncodeToString(digest[:])
	var prior models.SupportTicketAttachment
	err := s.db.WithContext(ctx).Where("ticket_id = ? AND uploader_id = ? AND request_key = ?", ticketID, actorID, key).First(&prior).Error
	if err == nil {
		if prior.RequestHash != hash {
			return nil, OpError(409, "Idempotency-Key was already used for another attachment")
		}
		return &prior, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, OpError(503, "Ticket attachments unavailable")
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&models.SupportTicketAttachment{}).Where("ticket_id = ?", ticketID).Count(&count).Error; err != nil {
		return nil, OpError(503, "Ticket attachments unavailable")
	}
	if count >= 10 {
		return nil, OpError(409, "Maximum 10 ticket attachments")
	}
	id := newID()
	objectKey := "support/tickets/" + ticketID + "/" + id
	if _, err := s.Storage.Put(ctx, storage.Private, objectKey, storage.Upload{Body: bytes.NewReader(body), Size: int64(len(body)), ContentType: contentType}); err != nil {
		return nil, OpError(503, "Private file storage unavailable")
	}
	actorType := "app"
	if staff {
		actorType = "staff"
	}
	attachment := models.SupportTicketAttachment{ID: id, TicketID: ticketID, UploaderID: actorID, ActorType: actorType, ObjectKey: objectKey, RequestKey: key, RequestHash: hash, ContentType: contentType, Size: int64(len(body)), CreatedAt: s.now()}
	var saved models.SupportTicketAttachment
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := ticketForActor(tx.Clauses(clause.Locking{Strength: "UPDATE"}), ticketID, actorID, staff); err != nil {
			return err
		}
		if err := tx.Where("ticket_id = ? AND uploader_id = ? AND request_key = ?", ticketID, actorID, key).First(&saved).Error; err == nil {
			if saved.RequestHash != hash {
				return OpError(409, "Idempotency-Key was already used for another attachment")
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var currentCount int64
		if err := tx.Model(&models.SupportTicketAttachment{}).Where("ticket_id = ?", ticketID).Count(&currentCount).Error; err != nil {
			return err
		}
		if currentCount >= 10 {
			return OpError(409, "Maximum 10 ticket attachments")
		}
		if err := tx.Create(&attachment).Error; err != nil {
			return err
		}
		saved = attachment
		return nil
	})
	if err != nil {
		_ = s.Storage.Delete(ctx, storage.Private, objectKey)
		var op *OperationError
		if errors.As(err, &op) {
			return nil, err
		}
		return nil, OpError(503, "Ticket attachments unavailable")
	}
	if saved.ID != attachment.ID {
		_ = s.Storage.Delete(ctx, storage.Private, objectKey)
	}
	return &saved, nil
}

func (s *Store) ReadTicketAttachment(ctx context.Context, ticketID, attachmentID, actorID string, staff bool) ([]byte, *models.SupportTicketAttachment, error) {
	if _, err := ticketForActor(s.db.WithContext(ctx), ticketID, actorID, staff); err != nil {
		return nil, nil, err
	}
	if _, err := uuid.Parse(attachmentID); err != nil {
		return nil, nil, FieldError("attachment_id", "valid attachment ID required")
	}
	var attachment models.SupportTicketAttachment
	if err := s.db.WithContext(ctx).Where("id = ? AND ticket_id = ?", attachmentID, ticketID).First(&attachment).Error; err != nil {
		return nil, nil, OpError(404, "Attachment not found")
	}
	if s.Storage == nil {
		return nil, nil, OpError(503, "Private file storage unavailable")
	}
	reader, _, err := s.Storage.Get(ctx, storage.Private, attachment.ObjectKey)
	if err != nil {
		return nil, nil, OpError(503, "Private file storage unavailable")
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxTicketAttachmentBytes+1))
	if err != nil || len(data) > maxTicketAttachmentBytes {
		return nil, nil, OpError(503, "Private file storage unavailable")
	}
	return data, &attachment, nil
}
