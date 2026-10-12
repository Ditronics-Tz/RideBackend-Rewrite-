package store

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/config"
	"ride-backend/models"
)

// ProcessDataRequests safely handles pending work. Each pending request is claimed
// with a conditional UPDATE so cron and in-process runners can overlap.
func (s *Store) ProcessDataRequests(cfg *config.Config) error {
	if cfg == nil {
		return errors.New("data request configuration is missing")
	}
	if strings.TrimSpace(cfg.ExportDir) == "" {
		cfg.ExportDir = "./uploads/private/exports"
	}
	// Recover claims left by a crashed process; active claims are never stolen.
	stale := time.Now().UTC().Add(-5 * time.Minute)
	_ = s.db.Model(&models.DataExportRequest{}).Where("status = ? AND updated_at < ?", "processing", stale).Update("status", "pending").Error
	_ = s.db.Model(&models.AccountDeletionRequest{}).Where("status = ? AND updated_at < ?", "processing", stale).Update("status", "pending").Error
	var failures []string
	var exports []models.DataExportRequest
	if err := s.db.Where("status = ?", "pending").Order("requested_at").Find(&exports).Error; err != nil {
		return err
	}
	for _, r := range exports {
		if err := s.processExport(r, cfg); err != nil {
			failures = append(failures, "export: "+err.Error())
		}
	}
	var deletions []models.AccountDeletionRequest
	if err := s.db.Where("status = ? AND scheduled_for <= ?", "pending", time.Now().UTC()).Order("scheduled_for").Find(&deletions).Error; err != nil {
		return err
	}
	for _, r := range deletions {
		if err := s.processDeletion(r.ID, cfg); err != nil {
			failures = append(failures, "deletion: "+err.Error())
		}
	}
	if err := s.expireExports(time.Now().UTC(), cfg.ExportDir); err != nil {
		failures = append(failures, "export expiry failed")
	}
	if err := s.cleanupCompletedDeletions(cfg.ExportDir); err != nil {
		failures = append(failures, "deletion file cleanup failed")
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d data request operation(s) failed: %s", len(failures), strings.Join(failures, "; "))
	}
	return nil
}

func (s *Store) processExport(candidate models.DataExportRequest, cfg *config.Config) error {
	claim := s.db.Model(&models.DataExportRequest{}).Where("id = ? AND status = ?", candidate.ID, "pending").Updates(map[string]any{"status": "processing", "updated_at": time.Now().UTC()})
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected == 0 {
		return nil
	}
	sections, paths, err := s.exportSections(candidate.UserID)
	if err != nil {
		s.failExport(candidate.ID)
		return errors.New("export data unavailable")
	}
	data, err := json.MarshalIndent(sections, "", "  ")
	if err != nil {
		s.failExport(candidate.ID)
		return errors.New("export serialization failed")
	}
	dir := filepath.Clean(cfg.ExportDir)
	pub := filepath.Clean(filepath.Join("uploads", "public"))
	rel, err := filepath.Rel(pub, dir)
	if err != nil || rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..") {
		s.failExport(candidate.ID)
		return errors.New("export directory must not be public")
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		s.failExport(candidate.ID)
		return errors.New("export directory unavailable")
	}
	publicDir, _ := filepath.Abs(filepath.Join("uploads", "public"))
	resolvedPublic, pe := filepath.EvalSymlinks(publicDir)
	resolvedExport, ee := filepath.EvalSymlinks(dir)
	if pe == nil && ee == nil {
		publicPrefix := strings.TrimRight(resolvedPublic, string(os.PathSeparator)) + string(os.PathSeparator)
		if strings.EqualFold(filepath.Clean(resolvedExport), filepath.Clean(resolvedPublic)) || strings.HasPrefix(strings.ToLower(filepath.Clean(resolvedExport)), strings.ToLower(publicPrefix)) {
			s.failExport(candidate.ID)
			return errors.New("export directory must not resolve inside public uploads")
		}
	}
	_ = os.Chmod(dir, 0o700)
	random := make([]byte, 32)
	if _, err = rand.Read(random); err != nil {
		s.failExport(candidate.ID)
		return errors.New("random file name unavailable")
	}
	name := hex.EncodeToString(random) + ".zip"
	final := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".export-*.tmp")
	if err != nil {
		s.failExport(candidate.ID)
		return errors.New("export file unavailable")
	}
	tmpName := tmp.Name()
	_ = tmp.Chmod(0o600)
	w := zip.NewWriter(tmp)
	zw, err := w.Create("data.json")
	if err == nil {
		_, err = zw.Write(data)
	}
	for i, p := range paths {
		if err != nil {
			break
		}
		safe, ok := safeUploadPath(p)
		if !ok {
			continue
		}
		f, e := os.Open(safe)
		if e != nil {
			continue
		}
		entry := fmt.Sprintf("files/%03d_%s", i, filepath.Base(safe))
		dest, e := w.Create(entry)
		if e == nil {
			_, e = io.Copy(dest, f)
		}
		_ = f.Close()
		if e != nil {
			err = e
		}
	}
	if closeErr := w.Close(); err == nil {
		err = closeErr
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmpName, final)
	}
	if err != nil {
		_ = os.Remove(tmpName)
		s.failExport(candidate.ID)
		return errors.New("export archive could not be built")
	}
	info, err := os.Stat(final)
	if err != nil {
		_ = os.Remove(final)
		s.failExport(candidate.ID)
		return errors.New("export archive could not be inspected")
	}
	now := time.Now().UTC()
	hours := cfg.ExportTTLHours
	if hours <= 0 {
		hours = 168
	}
	res := s.db.Model(&models.DataExportRequest{}).Where("id = ? AND status = ?", candidate.ID, "processing").Updates(map[string]any{"status": "ready", "file_path": final, "file_size": info.Size(), "ready_at": now, "expires_at": now.Add(time.Duration(hours) * time.Hour), "updated_at": now})
	if res.Error != nil || res.RowsAffected != 1 {
		_ = os.Remove(final)
		return errors.New("export request changed during processing")
	}
	if s.ExportNotifier != nil {
		_ = s.ExportNotifier.NotifyExportReady(context.Background(), candidate.UserID, candidate.ID)
	}
	return nil
}
func (s *Store) failExport(id string) {
	_ = s.db.Model(&models.DataExportRequest{}).Where("id = ? AND status = ?", id, "processing").Updates(map[string]any{"status": "failed", "error": "Export could not be prepared", "updated_at": time.Now().UTC()}).Error
}
func safeUploadPath(path string) (string, bool) {
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	clean := filepath.Clean(path)
	allowed := []string{"uploads/public", "uploads/licences", "uploads/documents"}
	valid := false
	for _, root := range allowed {
		rel, err := filepath.Rel(root, clean)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			valid = true
			break
		}
	}
	if !valid {
		return "", false
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs, true
	} // Missing uploads are harmless and are skipped by the zipper.
	for _, root := range []string{"uploads/public", "uploads/licences", "uploads/documents"} {
		base, e := filepath.Abs(root)
		if e != nil {
			continue
		}
		base, e = filepath.EvalSymlinks(base)
		if e != nil {
			continue
		}
		rel, e := filepath.Rel(base, resolved)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return resolved, true
		}
	}
	return "", false
}

func (s *Store) processDeletion(id string, cfg *config.Config) error {
	claim := s.db.Model(&models.AccountDeletionRequest{}).Where("id = ? AND status = ? AND scheduled_for <= ?", id, "pending", time.Now().UTC()).Update("status", "processing")
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected == 0 {
		return nil
	}
	var req models.AccountDeletionRequest
	if err := s.db.First(&req, "id = ?", id).Error; err != nil {
		return err
	}
	blockers, err := s.DeletionBlockers(req.UserID)
	if err != nil {
		s.resetDeletion(id)
		return errors.New("deletion blockers unavailable")
	}
	if len(blockers) > 0 {
		data, _ := json.Marshal(blockers)
		_ = s.db.Model(&req).Updates(map[string]any{"status": "blocked", "block_reason": string(data)}).Error
		return nil
	}
	sections, files, err := s.exportSections(req.UserID)
	_ = sections
	if err != nil {
		s.resetDeletion(id)
		return errors.New("owned files unavailable")
	}
	filesClean := make([]string, 0, len(files))
	for _, p := range files {
		if safe, ok := safeUploadPath(p); ok {
			filesClean = append(filesClean, safe)
		}
	}
	var exportPaths []string
	var ex []models.DataExportRequest
	if err = s.db.Where("user_id = ?", req.UserID).Find(&ex).Error; err != nil {
		s.resetDeletion(id)
		return err
	}
	for _, x := range ex {
		if x.FilePath != "" {
			if safe, ok := safePrivateExportPath(x.FilePath, cfg.ExportDir); ok {
				exportPaths = append(exportPaths, safe)
			}
		}
	}
	filesClean = append(filesClean, exportPaths...)
	now := time.Now().UTC()
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var current models.AccountDeletionRequest
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ? AND status = ?", id, "processing").Error; e != nil {
			return e
		}
		if e := runAnonymizers(tx, current.UserID); e != nil {
			return e
		}
		var user models.User
		if e := tx.First(&user, "id = ?", current.UserID).Error; e != nil {
			return e
		}
		recomputeInTx(tx, user.ID)
		stored, _ := json.Marshal(filesClean)
		if e := tx.Model(&models.AccountDeletionRequest{}).Where("id = ?", id).Updates(map[string]any{"status": "completed", "completed_at": now, "error": "", "block_reason": "", "hold_reason": "", "verification_note": "", "cleanup_paths": string(stored), "updated_at": now}).Error; e != nil {
			return e
		}
		log := &models.StaffAuditLog{ID: newID(), ActorID: "system", Action: "account_anonymised", TargetType: "user", TargetID: user.ID, Meta: "{}", CreatedAt: now}
		return tx.Create(log).Error
	})
	if err != nil {
		s.resetDeletion(id)
		return errors.New("account anonymisation failed")
	}
	_ = s.cleanupCompletedDeletions(cfg.ExportDir)
	return nil
}
func (s *Store) resetDeletion(id string) {
	_ = s.db.Model(&models.AccountDeletionRequest{}).Where("id = ? AND status = ?", id, "processing").Update("status", "pending").Error
}
func safePrivateExportPath(path, dir string) (string, bool) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", false
	}
	root, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	resolvedRoot, rootErr := filepath.EvalSymlinks(root)
	resolvedParent, parentErr := filepath.EvalSymlinks(filepath.Dir(abs))
	if rootErr == nil && parentErr == nil {
		resolvedRel, e := filepath.Rel(resolvedRoot, resolvedParent)
		if e != nil || resolvedRel == ".." || strings.HasPrefix(resolvedRel, ".."+string(os.PathSeparator)) {
			return "", false
		}
	}
	return abs, true
}

func IsPrivateExportPath(path, dir string) bool { _, ok := safePrivateExportPath(path, dir); return ok }
func (s *Store) cleanupCompletedDeletions(exportDir string) error {
	var rows []models.AccountDeletionRequest
	if err := s.db.Where("status = ? AND cleanup_paths <> ''", "completed").Find(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		var paths []string
		if json.Unmarshal([]byte(r.CleanupPaths), &paths) != nil {
			return errors.New("owned file cleanup record is invalid")
		}
		cleaned := true
		for _, p := range paths {
			if safe, ok := safeCleanupPath(p, exportDir); ok {
				if err := os.Remove(safe); err != nil && !os.IsNotExist(err) {
					cleaned = false
				}
			} else {
				cleaned = false
			}
		}
		if !cleaned {
			return errors.New("owned file cleanup is incomplete")
		}
		if err := s.db.Model(&r).Update("cleanup_paths", "").Error; err != nil {
			return err
		}
	}
	return nil
}
func safeCleanupPath(path, exportDir string) (string, bool) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", false
	}
	for _, root := range []string{"uploads/public", "uploads/licences", "uploads/documents", exportDir} {
		base, e := filepath.Abs(root)
		if e != nil {
			continue
		}
		rel, e := filepath.Rel(base, abs)
		if e == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return abs, true
		}
	}
	return "", false
}
func (s *Store) expireExports(now time.Time, dir string) error {
	var rows []models.DataExportRequest
	if err := s.db.Where("status = ? AND expires_at <= ?", "ready", now).Find(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		if r.FilePath != "" {
			safe, ok := safePrivateExportPath(r.FilePath, dir)
			if !ok {
				return errors.New("export cleanup path is invalid")
			}
			if err := os.Remove(safe); err != nil && !os.IsNotExist(err) {
				return errors.New("expired export file cleanup failed")
			}
		}
		if err := s.db.Model(&models.DataExportRequest{}).Where("id = ? AND status = ? AND expires_at <= ?", r.ID, "ready", now).Updates(map[string]any{"status": "expired", "file_path": "", "updated_at": now}).Error; err != nil {
			return err
		}
	}
	return nil
}
func recomputeInTx(tx *gorm.DB, id string) {
	type agg struct {
		Avg   float64
		Count int64
	}
	var a agg
	_ = tx.Model(&models.Rating{}).Select("COALESCE(AVG(score),0) AS avg, COUNT(*) AS count").Where("to_user_id = ?", id).Scan(&a).Error
	_ = tx.Model(&models.DriverProfile{}).Where("user_id = ?", id).Updates(map[string]any{"average_rating": a.Avg, "total_ratings": a.Count}).Error
	_ = tx.Model(&models.PassengerProfile{}).Where("user_id = ?", id).Updates(map[string]any{"average_rating": a.Avg, "total_ratings": a.Count}).Error
}
