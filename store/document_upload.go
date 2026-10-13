package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"ride-backend/models"
	"ride-backend/storage"
)

const MaxDocumentBytes int64 = 8 * 1024 * 1024

var documentExtensions = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "application/pdf": ".pdf"}

type PresignDocumentInput struct {
	Type        string `json:"type"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	ExpiresAt   string `json:"expires_at"`
}

func ParseDocumentExpiry(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	var parsed time.Time
	var err error
	if len(raw) == 10 {
		parsed, err = time.ParseInLocation("2006-01-02", raw, time.FixedZone("EAT", 3*3600))
		parsed = parsed.AddDate(0, 0, 1)
	} else {
		parsed, err = time.Parse(time.RFC3339, raw)
	}
	if err != nil {
		return nil, FieldError("expires_at", "expires_at must be YYYY-MM-DD or RFC3339")
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func (s *Store) PresignDriverDocument(ctx context.Context, user string, in PresignDocumentInput) (*models.DriverDocument, storage.PresignedURL, error) {
	empty := storage.PresignedURL{}
	if s.Storage == nil {
		return nil, empty, OpError(503, "object storage is not configured")
	}
	ext, ok := documentExtensions[in.ContentType]
	if !ok || (in.Type == "vehicle_photo" && in.ContentType == "application/pdf") {
		return nil, empty, FieldError("content_type", "JPEG, PNG, WebP or PDF required; vehicle photos must be images")
	}
	if !models.ValidDocumentType(in.Type) {
		return nil, empty, FieldError("type", "invalid document type")
	}
	if in.Size < 1 || in.Size > MaxDocumentBytes {
		return nil, empty, FieldError("size", "document must contain 1-8388608 bytes")
	}
	expires, err := ParseDocumentExpiry(in.ExpiresAt)
	if err != nil {
		return nil, empty, err
	}
	if (models.RequiresExpiry(in.Type) && expires == nil) || (expires != nil && !expires.After(s.now())) {
		return nil, empty, FieldError("expires_at", "a future expiry date is required")
	}
	uploadExpiry := s.now().Add(5 * time.Minute)
	doc := &models.DriverDocument{ID: newID(), DriverID: user, Type: in.Type, Status: "pending_upload", ContentType: in.ContentType, ExpectedSize: in.Size, UploadExpiresAt: &uploadExpiry, ExpiresAt: expires, CreatedAt: s.now()}
	doc.ObjectKey = "staging/documents/" + user + "/" + doc.ID + "/" + newID() + ext
	doc.StagingObjectKey = doc.ObjectKey
	upload, err := s.Storage.PresignPut(ctx, storage.Private, doc.ObjectKey, in.ContentType, 5*time.Minute)
	if err != nil {
		return nil, empty, err
	}
	err = s.Transaction(func(tx *Store) error {
		p, err := lockDriver(tx.db, user)
		if err != nil {
			return err
		}
		if p.AssignedTo != nil {
			return conflict("review is claimed; wait for the reviewer")
		}
		return tx.db.Create(doc).Error
	})
	return doc, upload, err
}

func (s *Store) ConfirmDriverDocument(ctx context.Context, user, id string) (*models.DriverDocument, error) {
	var doc models.DriverDocument
	if err := s.db.First(&doc, "id = ? AND driver_id = ?", id, user).Error; err != nil {
		return nil, err
	}
	if doc.ObjectKey == "" {
		return nil, OpError(409, "document does not use object storage")
	}
	if doc.Status != "pending_upload" {
		return &doc, s.cleanupDocumentStaging(ctx, &doc)
	}
	if doc.UploadExpiresAt == nil || !doc.UploadExpiresAt.After(s.now()) {
		return nil, OpError(409, "upload link expired; request a new upload")
	}
	if s.Storage == nil {
		return nil, OpError(503, "object storage is not configured")
	}
	info, err := s.Storage.Head(ctx, storage.Private, doc.ObjectKey)
	if err != nil {
		return nil, err
	}
	if info.Size != doc.ExpectedSize || info.Size < 1 || info.Size > MaxDocumentBytes {
		return nil, FieldError("size", "uploaded document size does not match")
	}
	if info.ContentType != doc.ContentType {
		return nil, FieldError("content_type", "uploaded document type does not match")
	}
	body, _, err := s.Storage.Get(ctx, storage.Private, doc.ObjectKey)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(body, MaxDocumentBytes+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil {
		return nil, OpError(502, "unable to read uploaded object")
	}
	if int64(len(data)) != doc.ExpectedSize {
		return nil, FieldError("size", "uploaded document size does not match")
	}
	if http.DetectContentType(data) != doc.ContentType {
		return nil, FieldError("content_type", "uploaded document bytes do not match the declared type")
	}
	// A signed PUT can be reused until expiry. Copy validated bytes to a fresh
	// key that was never signed for upload, keeping confirmed evidence immutable.
	finalKey := "documents/" + user + "/" + id + "/" + newID() + documentExtensions[doc.ContentType]
	if _, err := s.Storage.Put(ctx, storage.Private, finalKey, storage.Upload{Body: bytes.NewReader(data), Size: int64(len(data)), ContentType: doc.ContentType}); err != nil {
		return nil, err
	}
	accepted := false
	err = s.Transaction(func(tx *Store) error {
		p, err := lockDriver(tx.db, user)
		if err != nil {
			return err
		}
		if p.AssignedTo != nil {
			return conflict("review is claimed; wait for the reviewer")
		}
		var saved models.DriverDocument
		if err := tx.db.First(&saved, "id = ? AND driver_id = ?", id, user).Error; err != nil {
			return err
		}
		if saved.Status != "pending_upload" {
			doc = saved
			return nil
		}
		if saved.UploadExpiresAt == nil || !saved.UploadExpiresAt.After(tx.now()) {
			return OpError(409, "upload link expired; request a new upload")
		}
		if err := tx.db.Model(&models.DriverDocument{}).Where("driver_id = ? AND type = ? AND is_current = ?", user, doc.Type, true).Update("is_current", false).Error; err != nil {
			return err
		}
		result := tx.db.Model(&models.DriverDocument{}).Where("id = ? AND status = ?", id, "pending_upload").Updates(map[string]any{"status": "submitted", "is_current": true, "object_key": finalKey})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return conflict("document upload changed")
		}
		doc.Status = "submitted"
		doc.IsCurrent = true
		doc.ObjectKey = finalKey
		accepted = true
		if p.VerificationStatus != models.Suspended {
			p.VerificationStatus = models.PendingDocuments
		}
		p.IsOnline = false
		if err := tx.db.Save(p).Error; err != nil {
			return err
		}
		return appendReview(tx.db, user, &doc.ID, nil, "uploaded", "", doc.Type)
	})
	if err != nil {
		return nil, err
	}
	if !accepted && finalKey != doc.ObjectKey {
		if err := s.Storage.Delete(ctx, storage.Private, finalKey); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return nil, err
		}
	}
	return &doc, s.cleanupDocumentStaging(ctx, &doc)
}

func (s *Store) cleanupDocumentStaging(ctx context.Context, doc *models.DriverDocument) error {
	if doc.StagingObjectKey == "" {
		return nil
	}
	if s.Storage == nil {
		return OpError(503, "object storage is not configured")
	}
	if err := s.Storage.Delete(ctx, storage.Private, doc.StagingObjectKey); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if err := s.db.Model(&models.DriverDocument{}).Where("id = ? AND staging_object_key = ?", doc.ID, doc.StagingObjectKey).Update("staging_object_key", "").Error; err != nil {
		return err
	}
	doc.StagingObjectKey = ""
	return nil
}
