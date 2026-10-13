package controllers

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"ride-backend/config"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
)

type DataRequestController struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewDataRequestController(cfg *config.Config, s *store.Store) *DataRequestController {
	return &DataRequestController{Cfg: cfg, Store: s}
}

func (dc *DataRequestController) reauth(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	u, ok := dc.Store.GetUserByID(uid)
	if !ok {
		return gorm.ErrRecordNotFound
	}
	var in struct {
		CurrentPassword string `json:"current_password"`
	}
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&in); err != nil {
			return fiber.NewError(400, "Invalid request body")
		}
	}
	if u.PasswordHash != "" {
		if in.CurrentPassword == "" || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.CurrentPassword)) != nil {
			return fiber.NewError(401, "Re-authentication failed")
		}
		return nil
	}
	issued, _ := c.Locals("token_issued_at").(time.Time)
	age := time.Since(issued)
	if issued.IsZero() || age < 0 || age > 5*time.Minute {
		return fiber.NewError(401, "Sign in again to confirm this request")
	}
	return nil
}
func (dc *DataRequestController) RequestDeletion(c *fiber.Ctx) error {
	if err := dc.reauth(c); err != nil {
		return c.Status(fiberErrStatus(err)).JSON(fiber.Map{"error": err.Error()})
	}
	uid, _ := middleware.CurrentUser(c)
	r, reasons, err := dc.Store.RequestDeletion(uid, "user", "", dc.Cfg.DeletionGraceDays)
	if err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	}
	if len(reasons) > 0 {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Account deletion is blocked", "request_id": r.ID, "reasons": reasons})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"request_id": r.ID, "status": r.Status, "scheduled_for": r.ScheduledFor})
}
func (dc *DataRequestController) CancelDeletion(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	r, err := dc.Store.CancelDeletion(uid)
	if err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": r.Status, "cancelled_at": r.CancelledAt})
}
func (dc *DataRequestController) DeletionStatus(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	r, err := dc.Store.CurrentDeletionRequest(uid)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(fiber.Map{"status": "none"})
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Unable to fetch deletion request"})
	}
	return c.JSON(fiber.Map{"id": r.ID, "status": r.Status, "requested_at": r.RequestedAt, "scheduled_for": r.ScheduledFor, "completed_at": r.CompletedAt, "cancelled_at": r.CancelledAt, "block_reason": r.BlockReason, "hold_reason": r.HoldReason})
}
func (dc *DataRequestController) RequestExport(c *fiber.Ctx) error {
	if err := dc.reauth(c); err != nil {
		return c.Status(fiberErrStatus(err)).JSON(fiber.Map{"error": err.Error()})
	}
	uid, _ := middleware.CurrentUser(c)
	r, err := dc.Store.RequestExport(uid)
	if err != nil {
		return c.Status(429).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(202).JSON(r)
}
func (dc *DataRequestController) ExportRequests(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	p, err := operationPage(c)
	if err != nil {
		return verificationError(c, err)
	}
	rows := []models.DataExportRequest{}
	var total int64
	q := dc.Store.DB().Model(&models.DataExportRequest{}).Where("user_id = ?", uid)
	err = q.Count(&total).Error
	if err == nil {
		err = q.Order("requested_at DESC,id DESC").Limit(p.Limit).Offset(p.Offset).Find(&rows).Error
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Unable to fetch export requests"})
	}
	return c.JSON(fiber.Map{"requests": rows, "total": total, "limit": p.Limit, "offset": p.Offset})
}
func (dc *DataRequestController) DownloadExport(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	path, err := dc.Store.RecordExportDownload(uid, c.Params("id"), dc.Cfg.ExportDir)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Export is unavailable"})
	}
	if _, err = os.Stat(path); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Export is unavailable"})
	}
	c.Set("Content-Type", "application/zip")
	c.Set("Content-Disposition", "attachment; filename=ride-data-export.zip")
	return c.SendFile(path)
}

func (dc *DataRequestController) AdminList(c *fiber.Ctx) error {
	kind := c.Query("type")
	if kind != "deletion" && kind != "export" {
		return c.Status(400).JSON(fiber.Map{"error": "type must be deletion or export"})
	}
	p, err := operationPage(c)
	if err != nil {
		return verificationError(c, err)
	}
	limit, offset := p.Limit, p.Offset
	var total int64
	var rows any
	db := dc.Store.DB()
	if kind == "deletion" {
		q := db.Model(&models.AccountDeletionRequest{})
		if st := c.Query("status"); st != "" {
			q = q.Where("status = ?", st)
		}
		if err := q.Count(&total).Error; err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Unable to list data requests"})
		}
		var r []models.AccountDeletionRequest
		if err := q.Order("requested_at DESC").Limit(limit).Offset(offset).Find(&r).Error; err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Unable to list data requests"})
		}
		rows = r
	} else {
		q := db.Model(&models.DataExportRequest{})
		if st := c.Query("status"); st != "" {
			q = q.Where("status = ?", st)
		}
		if err := q.Count(&total).Error; err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Unable to list data requests"})
		}
		var r []models.DataExportRequest
		if err := q.Order("requested_at DESC").Limit(limit).Offset(offset).Find(&r).Error; err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Unable to list data requests"})
		}
		for i := range r {
			r[i].FilePath = ""
		}
		rows = r
	}
	return c.JSON(fiber.Map{"total": total, "limit": limit, "offset": offset, "requests": rows})
}
func (dc *DataRequestController) AdminDetail(c *fiber.Ctx) error {
	id := c.Params("id")
	var d models.AccountDeletionRequest
	if err := dc.Store.DB().First(&d, "id = ?", id).Error; err == nil {
		return c.JSON(fiber.Map{"type": "deletion", "request": d})
	}
	var e models.DataExportRequest
	if err := dc.Store.DB().First(&e, "id = ?", id).Error; err == nil {
		e.FilePath = ""
		return c.JSON(fiber.Map{"type": "export", "request": e})
	}
	return c.Status(404).JSON(fiber.Map{"error": "Data request not found"})
}
func (dc *DataRequestController) AdminRequestDeletion(c *fiber.Ctx) error {
	var in struct {
		VerificationNote string `json:"verification_note"`
	}
	if c.BodyParser(&in) != nil || strings.TrimSpace(in.VerificationNote) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "verification_note is required"})
	}
	id := c.Params("id")
	actor := middleware.CurrentStaff(c)
	r, reasons, err := dc.Store.RequestDeletionByActor(id, "admin", strings.TrimSpace(in.VerificationNote), dc.Cfg.DeletionGraceDays, actor.ID)
	if err != nil {
		return c.Status(409).JSON(fiber.Map{"error": err.Error()})
	}
	if len(reasons) > 0 {
		return c.Status(409).JSON(fiber.Map{"error": "Account deletion is blocked", "request_id": r.ID, "reasons": reasons})
	}
	return c.Status(201).JSON(r)
}
func (dc *DataRequestController) Hold(c *fiber.Ctx) error {
	var in struct {
		Reason string `json:"reason"`
	}
	if c.BodyParser(&in) != nil || strings.TrimSpace(in.Reason) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "reason is required"})
	}
	id := c.Params("id")
	actor := middleware.CurrentStaff(c)
	now := time.Now().UTC()
	err := dc.Store.Transaction(func(tx *store.Store) error {
		res := tx.DB().Model(&models.AccountDeletionRequest{}).Where("id = ? AND status IN ?", id, []string{"pending", "blocked"}).Updates(map[string]any{"status": "on_hold", "hold_reason": strings.TrimSpace(in.Reason), "hold_by": actor.ID, "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.CreateAuditLog(actor.ID, "deletion_held", "data_request", id, c.IP(), nil)
	})
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return c.Status(500).JSON(fiber.Map{"error": "Unable to place legal hold"})
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.Status(409).JSON(fiber.Map{"error": "Deletion request cannot be held"})
	}
	return c.JSON(fiber.Map{"status": "on_hold"})
}
func (dc *DataRequestController) Release(c *fiber.Ctx) error {
	id := c.Params("id")
	actor := middleware.CurrentStaff(c)
	err := dc.Store.Transaction(func(tx *store.Store) error {
		res := tx.DB().Model(&models.AccountDeletionRequest{}).Where("id = ? AND status = ?", id, "on_hold").Updates(map[string]any{"status": "pending", "hold_reason": "", "hold_by": nil, "updated_at": time.Now().UTC()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.CreateAuditLog(actor.ID, "deletion_released", "data_request", id, c.IP(), nil)
	})
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return c.Status(500).JSON(fiber.Map{"error": "Unable to release legal hold"})
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.Status(409).JSON(fiber.Map{"error": "Request is not on hold"})
	}
	return c.JSON(fiber.Map{"status": "pending"})
}
func (dc *DataRequestController) Execute(c *fiber.Ctx) error {
	var in struct {
		VerificationNote string `json:"verification_note"`
	}
	if c.BodyParser(&in) != nil || strings.TrimSpace(in.VerificationNote) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "verification_note confirming the user asked for immediate deletion is required"})
	}
	id := c.Params("id")
	var r models.AccountDeletionRequest
	if err := dc.Store.DB().First(&r, "id = ?", id).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Deletion request not found"})
	}
	if r.Status != "pending" {
		return c.Status(409).JSON(fiber.Map{"error": "Deletion request is not pending"})
	}
	now := time.Now().UTC()
	actor := middleware.CurrentStaff(c)
	err := dc.Store.Transaction(func(tx *store.Store) error {
		if e := tx.DB().Model(&r).Updates(map[string]any{"scheduled_for": now, "verification_note": strings.TrimSpace(in.VerificationNote), "updated_at": now}).Error; e != nil {
			return e
		}
		return tx.CreateAuditLog(actor.ID, "deletion_execute_requested", "user", r.UserID, c.IP(), nil)
	})
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Unable to schedule immediate deletion"})
	}
	if err := dc.Store.ProcessDataRequests(dc.Cfg); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Deletion processing failed"})
	}
	var result models.AccountDeletionRequest
	if err := dc.Store.DB().First(&result, "id = ?", id).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Unable to read deletion result"})
	}
	if result.Status == "blocked" {
		return c.Status(409).JSON(fiber.Map{"error": "Account deletion is blocked", "reasons": result.BlockReason})
	}
	return c.JSON(fiber.Map{"status": result.Status})
}

func fiberErrStatus(err error) int {
	var e *fiber.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 500
}
