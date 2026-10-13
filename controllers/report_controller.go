package controllers

import (
	"errors"
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
	"ride-backend/config"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
	"strconv"
)

type OperationsController struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewOperationsController(cfg *config.Config, s *store.Store) *OperationsController {
	return &OperationsController{cfg, s}
}
func operationError(c *fiber.Ctx, err error) error {
	var e *store.OperationError
	if errors.As(err, &e) {
		body := fiber.Map{"error": e.Message}
		if e.Message == "cash limit reached" {
			body["code"] = "CASH_LIMIT_REACHED"
		}
		if e.Field != "" {
			body["fields"] = map[string]string{e.Field: e.Message}
		}
		if e.RetryAfter > 0 {
			c.Set("Retry-After", strconv.Itoa(e.RetryAfter))
		}
		return c.Status(e.Status).JSON(body)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.Status(404).JSON(fiber.Map{"error": "record not found"})
	}
	if errors.Is(err, store.ErrVerificationInvalid) || errors.Is(err, store.ErrVerificationConflict) {
		return verificationError(c, err)
	}
	return c.Status(500).JSON(fiber.Map{"error": "Unable to process request"})
}
func operationPage(c *fiber.Ctx) (store.Page, error) {
	return store.ParsePage(c.Query("limit"), c.Query("offset"))
}
func (oc *OperationsController) Blocks(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	switch c.Method() {
	case "POST":
		var in struct {
			UserID string `json:"user_id"`
		}
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("user_id", "user_id is required"))
		}
		if err := oc.Store.BlockUser(uid, in.UserID); err != nil {
			return operationError(c, err)
		}
		return c.SendStatus(204)
	case "DELETE":
		if err := oc.Store.UnblockUser(uid, c.Params("userId")); err != nil {
			return operationError(c, err)
		}
		return c.SendStatus(204)
	default:
		p, err := operationPage(c)
		if err != nil {
			return operationError(c, err)
		}
		var total int64
		rows := []models.UserBlock{}
		q := oc.Store.DB().Model(&models.UserBlock{}).Where("blocker_id = ?", uid)
		if err := q.Count(&total).Error; err != nil {
			return operationError(c, err)
		}
		if err := q.Order("created_at DESC, blocked_id DESC").Limit(p.Limit).Offset(p.Offset).Find(&rows).Error; err != nil {
			return operationError(c, err)
		}
		return c.JSON(fiber.Map{"blocks": rows, "total": total, "limit": p.Limit, "offset": p.Offset})
	}
}
func (oc *OperationsController) AppReports(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	if c.Method() == "POST" {
		var in struct {
			ReportedID  string `json:"reported_id"`
			RideID      string `json:"ride_id"`
			Category    string `json:"category"`
			Description string `json:"description"`
		}
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("body", "invalid request"))
		}
		r := models.Report{ReportedID: in.ReportedID, RideID: in.RideID, Category: in.Category, Description: in.Description}
		if err := oc.Store.SubmitReport(uid, &r, oc.Cfg.ReportFlagThreshold, oc.Cfg.ReportFlagWindowDays); err != nil {
			return operationError(c, err)
		}
		return c.Status(201).JSON(fiber.Map{"id": r.ID, "status": r.Status})
	}
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	rows := []struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
	}{}
	q := oc.Store.DB().Model(&models.Report{}).Where("reporter_id = ?", uid)
	total, err := oc.Store.PageRows(q.Select("id, status, created_at"), p, &rows)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"reports": rows, "total": total, "limit": p.Limit, "offset": p.Offset})
}

func (oc *OperationsController) SupportTickets(c *fiber.Ctx) error {
	userID, _ := middleware.CurrentUser(c)
	if c.Method() == "POST" {
		var input models.CreateSupportTicketInput
		if len(c.Body()) > 8192 || c.BodyParser(&input) != nil {
			return operationError(c, store.FieldError("body", "invalid support ticket request"))
		}
		ticket, err := oc.Store.CreateSupportTicket(userID, input)
		if err != nil {
			return operationError(c, err)
		}
		return c.Status(201).JSON(ticket)
	}
	page, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	tickets, total, err := oc.Store.ListSupportTickets(store.SupportTicketFilter{User: userID}, page)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"tickets": tickets, "total": total, "limit": page.Limit, "offset": page.Offset})
}

func (oc *OperationsController) Reports(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	rows, total, err := oc.Store.Reports(store.ReportFilter{Status: c.Query("status"), Category: c.Query("category"), Assignee: c.Query("assignee"), Priority: c.Query("priority"), User: c.Query("user"), Before: c.Query("before")}, p)
	if err != nil {
		return operationError(c, err)
	}
	body := fiber.Map{"reports": rows, "total": total, "limit": p.Limit, "offset": p.Offset}
	if c.Path() == "/api/v1/admin/complaints" {
		c.Set("Deprecation", "true")
		body["complaints"] = rows
		body["count"] = len(rows)
	}
	return c.JSON(body)
}
func (oc *OperationsController) ReportDetail(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	out, err := oc.Store.ReportDetail(c.Params("id"), p)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(out)
}
func (oc *OperationsController) ReportAction(c *fiber.Ctx) error {
	var in struct {
		store.ResolveReportInput
		AssignedTo string `json:"assigned_to"`
	}
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("body", "invalid request"))
		}
	}
	action := c.Params("action")
	if c.Method() == "PATCH" {
		c.Set("Deprecation", "true")
		action = "resolve"
	}
	if err := oc.Store.WorkReport(c.Params("id"), action, middleware.CurrentStaff(c), in.Note, in.AssignedTo, in.ResolveReportInput); err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"id": c.Params("id"), "action": action})
}
func (oc *OperationsController) Flags(c *fiber.Ctx) error {
	if c.Method() == "POST" {
		if err := oc.Store.ResolveAccountFlag(c.Params("id"), middleware.CurrentStaff(c)); err != nil {
			return operationError(c, err)
		}
		return c.JSON(fiber.Map{"status": "resolved"})
	}
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	q := oc.Store.DB().Model(&models.AccountFlag{})
	if c.Query("status") != "" {
		q = q.Where("status = ?", c.Query("status"))
	}
	rows := []models.AccountFlag{}
	total, err := oc.Store.PageRows(q, p, &rows)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"flags": rows, "total": total, "limit": p.Limit, "offset": p.Offset})
}
func (oc *OperationsController) UserReports(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	rows, total, err := oc.Store.Reports(store.ReportFilter{User: c.Params("id")}, p)
	if err != nil {
		return operationError(c, err)
	}
	party, err := oc.Store.Party(c.Params("id"))
	if err != nil {
		return operationError(c, err)
	}
	var blockedBy, blocked int64
	if err := oc.Store.DB().Model(&models.UserBlock{}).Where("blocked_id = ?", c.Params("id")).Count(&blockedBy).Error; err != nil {
		return operationError(c, err)
	}
	if err := oc.Store.DB().Model(&models.UserBlock{}).Where("blocker_id = ?", c.Params("id")).Count(&blocked).Error; err != nil {
		return operationError(c, err)
	}
	out := fiber.Map{"reports": rows, "total": total, "limit": p.Limit, "offset": p.Offset, "party": party, "blocked_by_count": blockedBy, "blocked_count": blocked}
	if c.QueryBool("include_blocks") {
		actor := middleware.CurrentStaff(c)
		if actor.Role != models.StaffRoleAdmin {
			return operationError(c, store.OpError(403, "block lists require admin"))
		}
		blocks := []models.UserBlock{}
		if err := oc.Store.DB().Where("blocker_id = ? OR blocked_id = ?", party.ID, party.ID).Order("created_at DESC, blocker_id DESC, blocked_id DESC").Limit(p.Limit).Offset(p.Offset).Find(&blocks).Error; err != nil {
			return operationError(c, err)
		}
		out["blocks"] = blocks
		if err := oc.Store.CreateAuditLog(actor.ID, "view_user_blocks", "user", party.ID, c.IP(), nil); err != nil {
			return operationError(c, err)
		}
	}
	return c.JSON(out)
}
