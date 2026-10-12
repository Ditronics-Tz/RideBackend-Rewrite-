package store

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/models"
)

func init() {
	RegisterDataExporter(func(s *Store) DataExporter { return requestExporter{s} })
	RegisterAnonymizer(requestAnonymizer{})
}

type requestExporter struct{ s *Store }

func (requestExporter) Name() string { return "data_requests" }
func (requestExporter) Tables() []string {
	return []string{"account_deletion_requests", "data_export_requests"}
}
func (e requestExporter) Export(id string) (any, []string, error) {
	s := e.s
	var d []models.AccountDeletionRequest
	var exportsRows []models.DataExportRequest
	if err := s.db.Where("user_id = ?", id).Order("requested_at").Find(&d).Error; err != nil {
		return nil, nil, err
	}
	if err := s.db.Where("user_id = ?", id).Order("requested_at").Find(&exportsRows).Error; err != nil {
		return nil, nil, err
	}
	deletions := make([]map[string]any, 0, len(d))
	for _, r := range d {
		deletions = append(deletions, map[string]any{"id": r.ID, "status": r.Status, "source": r.Source, "requested_at": r.RequestedAt, "scheduled_for": r.ScheduledFor, "completed_at": r.CompletedAt, "cancelled_at": r.CancelledAt})
	}
	exports := make([]map[string]any, 0, len(exportsRows))
	for _, r := range exportsRows {
		exports = append(exports, map[string]any{"id": r.ID, "status": r.Status, "requested_at": r.RequestedAt, "ready_at": r.ReadyAt, "expires_at": r.ExpiresAt, "file_size": r.FileSize, "download_count": r.DownloadCount})
	}
	return map[string]any{"deletion_requests": deletions, "export_requests": exports}, nil, nil
}

type requestAnonymizer struct{}

func (requestAnonymizer) Name() string { return "data_requests" }
func (requestAnonymizer) Tables() []string {
	return []string{"account_deletion_requests", "data_export_requests"}
}
func (requestAnonymizer) Description() string {
	return "retains request IDs, status and timestamps for compliance evidence; clears free text, file paths and processing errors"
}
func (requestAnonymizer) Anonymize(tx *gorm.DB, id string) error {
	if err := tx.Model(&models.AccountDeletionRequest{}).Where("user_id = ?", id).Updates(map[string]any{"block_reason": "", "hold_reason": "", "error": "", "verification_note": ""}).Error; err != nil {
		return err
	}
	return tx.Model(&models.DataExportRequest{}).Where("user_id = ?", id).Updates(map[string]any{"file_path": "", "error": ""}).Error
}

// DeletionBlockers is intentionally extensible: future ride/billing modules register
// a blocker without changing this coordinator. No rides or payments exist today.
type DeletionBlocker func(*Store, string) ([]string, error)

var deletionBlockers []struct {
	name string
	fn   DeletionBlocker
}
var blockerMu sync.RWMutex

func RegisterDeletionBlocker(name string, fn DeletionBlocker) func() {
	blockerMu.Lock()
	deletionBlockers = append(deletionBlockers, struct {
		name string
		fn   DeletionBlocker
	}{name, fn})
	blockerMu.Unlock()
	return func() {
		blockerMu.Lock()
		defer blockerMu.Unlock()
		for i, b := range deletionBlockers {
			if b.name == name {
				deletionBlockers = append(deletionBlockers[:i], deletionBlockers[i+1:]...)
				return
			}
		}
	}
}
func DeletionBlockerNames() []string {
	blockerMu.RLock()
	defer blockerMu.RUnlock()
	out := make([]string, 0, len(deletionBlockers))
	for _, b := range deletionBlockers {
		out = append(out, b.name)
	}
	return out
}

func (s *Store) DeletionBlockers(userID string) ([]string, error) {
	blockerMu.RLock()
	items := append([]struct {
		name string
		fn   DeletionBlocker
	}{}, deletionBlockers...)
	blockerMu.RUnlock()
	var out []string
	for _, b := range items {
		reasons, err := b.fn(s, userID)
		if err != nil {
			return nil, err
		}
		out = append(out, reasons...)
	}
	return out, nil
}

func (s *Store) RequestDeletion(userID, source, verificationNote string, graceDays int) (*models.AccountDeletionRequest, []string, error) {
	return s.RequestDeletionByActor(userID, source, verificationNote, graceDays, "user:"+userID)
}

func (s *Store) RequestDeletionByActor(userID, source, verificationNote string, graceDays int, actorID string) (*models.AccountDeletionRequest, []string, error) {
	now := time.Now().UTC()
	if graceDays < 0 {
		graceDays = 14
	}
	var created models.AccountDeletionRequest
	var reasons []string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var u models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", userID).Error; err != nil {
			return err
		}
		if u.DeletedAt != nil || u.Status == "deleted" {
			return errors.New("account already deleted")
		}
		var active int64
		if err := tx.Model(&models.AccountDeletionRequest{}).Where("user_id = ? AND status IN ?", userID, []string{"pending", "processing", "blocked", "on_hold"}).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return errors.New("an active deletion request already exists")
		}
		var blockers []string
		// Blocker callbacks use a Store bound to this transaction, so related reads are consistent.
		txStore := *s
		txStore.db = tx
		var err error
		blockers, err = txStore.DeletionBlockers(userID)
		if err != nil {
			return err
		}
		reasons = blockers
		status := "pending"
		if len(blockers) > 0 {
			status = "blocked"
		}
		created = models.AccountDeletionRequest{ID: newID(), UserID: userID, Status: status, Source: source, RequestedAt: now, ScheduledFor: now.Add(time.Duration(graceDays) * 24 * time.Hour), PreviousStatus: u.Status, VerificationNote: verificationNote, CreatedAt: now, UpdatedAt: now}
		if len(blockers) > 0 {
			data, _ := json.Marshal(blockers)
			created.BlockReason = string(data)
		}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		if status == "pending" {
			if err := tx.Model(&models.User{}).Where("id = ?", userID).Updates(map[string]any{"status": "pending_deletion", "is_active": false, "updated_at": now}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.DriverProfile{}).Where("user_id = ?", userID).Update("is_online", false).Error; err != nil {
				return err
			}
		}
		return txStore.CreateAuditLog(actorID, "deletion_requested", "user", userID, "", nil)
	})
	if err != nil {
		return nil, nil, err
	}
	return &created, reasons, nil
}

func (s *Store) CancelDeletion(userID string) (*models.AccountDeletionRequest, error) {
	now := time.Now().UTC()
	var req models.AccountDeletionRequest
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND status IN ?", userID, []string{"pending", "blocked"}).Order("requested_at DESC").First(&req).Error; err != nil {
			return err
		}
		if req.Status == "pending" && !now.Before(req.ScheduledFor) {
			return errors.New("grace period has ended")
		}
		if err := tx.Model(&req).Updates(map[string]any{"status": "cancelled", "cancelled_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		req.Status = "cancelled"
		req.CancelledAt = &now
		previous := req.PreviousStatus
		if previous == "" {
			previous = "active"
		}
		if err := tx.Model(&models.User{}).Where("id = ? AND status = ?", userID, "pending_deletion").Updates(map[string]any{"status": previous, "is_active": previous == "active", "updated_at": now}).Error; err != nil {
			return err
		}
		txStore := *s
		txStore.db = tx
		return txStore.CreateAuditLog("user:"+userID, "deletion_cancelled", "user", userID, "", nil)
	})
	return &req, err
}
func (s *Store) CurrentDeletionRequest(userID string) (*models.AccountDeletionRequest, error) {
	var r models.AccountDeletionRequest
	err := s.db.Where("user_id = ?", userID).Order("requested_at DESC").First(&r).Error
	return &r, err
}

func (s *Store) RequestExport(userID string) (*models.DataExportRequest, error) {
	now := time.Now().UTC()
	var r models.DataExportRequest
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var u models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, "id = ?", userID).Error; err != nil {
			return err
		}
		var active int64
		if err := tx.Model(&models.DataExportRequest{}).Where("user_id = ? AND status IN ?", userID, []string{"pending", "processing", "ready"}).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return errors.New("an active export request already exists")
		}
		var recent int64
		if err := tx.Model(&models.DataExportRequest{}).Where("user_id = ? AND requested_at > ?", userID, now.Add(-24*time.Hour)).Count(&recent).Error; err != nil {
			return err
		}
		if recent > 0 {
			return errors.New("an export request was created in the last 24 hours")
		}
		r = models.DataExportRequest{ID: newID(), UserID: userID, Status: "pending", RequestedAt: now, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&r).Error; err != nil {
			return err
		}
		txStore := *s
		txStore.db = tx
		return txStore.CreateAuditLog("user:"+userID, "export_requested", "data_export", r.ID, "", nil)
	})
	return &r, err
}
func (s *Store) ListExportRequests(userID string) ([]models.DataExportRequest, error) {
	var r []models.DataExportRequest
	err := s.db.Where("user_id = ?", userID).Order("requested_at DESC").Find(&r).Error
	return r, err
}

func (s *Store) RecordExportDownload(userID, requestID, exportDir string) (string, error) {
	var path string
	now := time.Now().UTC()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&models.DataExportRequest{}).Where("id = ? AND user_id = ? AND status = ? AND expires_at > ?", requestID, userID, "ready", now).Updates(map[string]any{"download_count": gorm.Expr("download_count + 1"), "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Model(&models.DataExportRequest{}).Select("file_path").Where("id = ?", requestID).Scan(&path).Error; err != nil {
			return err
		}
		if !IsPrivateExportPath(path, exportDir) {
			return gorm.ErrRecordNotFound
		}
		txStore := *s
		txStore.db = tx
		return txStore.CreateAuditLog("user:"+userID, "export_downloaded", "data_export", requestID, "", nil)
	})
	return path, err
}
