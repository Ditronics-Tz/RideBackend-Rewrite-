package controllers

import (
	"errors"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"net/http"
	"os"
	"path/filepath"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/storage"
	"ride-backend/store"
	"ride-backend/utils"
	"strings"
	"time"
)

func verificationError(c *fiber.Ctx, err error) error {
	var op *store.OperationError
	if errors.As(err, &op) {
		return operationError(c, err)
	}
	status := fiber.StatusInternalServerError
	message := "Unable to process driver verification"
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status = 404
		message = "Driver or document not found"
	case errors.Is(err, store.ErrVerificationConflict):
		status = 409
		message = err.Error()
	case errors.Is(err, store.ErrVerificationInvalid):
		status = 400
		message = err.Error()
	}
	return c.Status(status).JSON(fiber.Map{"error": message})
}

func (ac *AdminController) GetDriverVerification(c *fiber.Ctx) error {
	result, err := ac.Store.VerificationDetail(c.Params("id"), true)
	if err != nil {
		return verificationError(c, err)
	}
	return c.JSON(result)
}
func (ac *AdminController) ClaimDriverVerification(c *fiber.Ctx) error {
	if err := ac.Store.ClaimDriver(c.Params("id"), middleware.CurrentStaff(c), false); err != nil {
		return verificationError(c, err)
	}
	return ac.GetDriverVerification(c)
}
func (ac *AdminController) ReleaseDriverVerification(c *fiber.Ctx) error {
	if err := ac.Store.ClaimDriver(c.Params("id"), middleware.CurrentStaff(c), true); err != nil {
		return verificationError(c, err)
	}
	return ac.GetDriverVerification(c)
}
func (ac *AdminController) ReviewDriverDocument(c *fiber.Ctx) error {
	id := c.Params("id")
	if value, ok := c.Locals("review_driver_id").(string); ok {
		id = value
	}
	var input struct {
		Decision   string `json:"decision"`
		ReasonCode string `json:"reason_code"`
		Note       string `json:"note"`
	}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid review body"})
	}
	actor := middleware.CurrentStaff(c)
	var existing models.DriverDocument
	if err := ac.Store.DB().First(&existing, "id = ? AND driver_id = ?", c.Params("documentId"), id).Error; err != nil {
		return verificationError(c, err)
	}
	if existing.Status != "submitted" && actor.Role != models.StaffRoleAdmin {
		return fiber.NewError(403, "Only admins can override a rejection or revoke an approval")
	}
	if err := ac.Store.ReviewDriverDocument(id, c.Params("documentId"), actor, input.Decision, input.ReasonCode, input.Note); err != nil {
		return verificationError(c, err)
	}
	_ = ac.Store.CreateAuditLog(actor.ID, "review_driver_document", "driver_document", c.Params("documentId"), c.IP(), fiber.Map{"decision": input.Decision, "reason_code": input.ReasonCode})
	result, err := ac.Store.VerificationDetail(id, true)
	if err != nil {
		return verificationError(c, err)
	}
	return c.JSON(result)
}
func (ac *AdminController) SuspendDriverVerification(c *fiber.Ctx) error {
	var input struct {
		Suspended *bool  `json:"suspended"`
		Note      string `json:"note"`
	}
	if err := c.BodyParser(&input); err != nil || input.Suspended == nil {
		return c.Status(400).JSON(fiber.Map{"error": "suspended boolean and note are required"})
	}
	actor := middleware.CurrentStaff(c)
	status := "active"
	if *input.Suspended {
		status = "suspended"
	}
	if err := ac.Store.ChangeAccountStatus(c.Params("id"), actor, status, input.Note, nil); err != nil {
		return verificationError(c, err)
	}
	_ = ac.Store.CreateAuditLog(actor.ID, "driver_suspension", "driver_profile", c.Params("id"), c.IP(), fiber.Map{"suspended": *input.Suspended})
	return ac.GetDriverVerification(c)
}

func (pc *ProfileController) driverIdentity(c *fiber.Ctx) (string, error) {
	id, role := middleware.CurrentUser(c)
	u, ok := pc.Store.GetUserByID(id)
	if !ok || !u.IsActive || role != models.RoleDriver || u.Role != models.RoleDriver {
		return "", fiber.NewError(403, "Active driver account required")
	}
	return id, nil
}
func (pc *ProfileController) GetMyVerification(c *fiber.Ctx) error {
	id, err := pc.driverIdentity(c)
	if err != nil {
		return err
	}
	result, err := pc.Store.VerificationDetail(id, false)
	if err != nil {
		return verificationError(c, err)
	}
	return c.JSON(result)
}
func (pc *ProfileController) UploadDriverDocument(c *fiber.Ctx) error {
	id, err := pc.driverIdentity(c)
	if err != nil {
		return err
	}
	if pc.Cfg.StorageDriver == "r2" {
		return fiber.NewError(410, "Use /driver/documents/presign then /driver/documents/:id/confirm for object-backed uploads")
	}
	kind := c.FormValue("type")
	if !models.ValidDocumentType(kind) {
		return c.Status(400).JSON(fiber.Map{"error": "Unknown document type"})
	}
	var expires *time.Time
	if raw := c.FormValue("expires_at"); raw != "" {
		// Dates expire after the displayed calendar day in Tanzania, not at its start.
		var parsed time.Time
		if len(raw) == 10 {
			parsed, err = time.ParseInLocation("2006-01-02", raw, time.FixedZone("EAT", 3*3600))
			parsed = parsed.AddDate(0, 0, 1)
		} else {
			parsed, err = time.Parse(time.RFC3339, raw)
		}
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "expires_at must be YYYY-MM-DD or RFC3339"})
		}
		expires = &parsed
	}
	if models.RequiresExpiry(kind) && expires == nil {
		return c.Status(400).JSON(fiber.Map{"error": "Expiry date is required for licence and insurance"})
	}
	file, err := c.FormFile("file")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "file is required"})
	}
	if file.Size > 8*1024*1024 {
		return c.Status(400).JSON(fiber.Map{"error": "Document must be at most 8 MB"})
	}
	stream, err := file.Open()
	if err != nil {
		return fiber.NewError(400, "Unable to read document")
	}
	buf := make([]byte, 512)
	n, _ := stream.Read(buf)
	stream.Close()
	mime := http.DetectContentType(buf[:n])
	extensions := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "application/pdf": ".pdf"}
	ext, ok := extensions[mime]
	if !ok {
		return fiber.NewError(400, "Only JPEG, PNG, WebP and PDF documents are accepted")
	}
	if kind == "vehicle_photo" && mime == "application/pdf" {
		return fiber.NewError(400, "Vehicle photo must be an image")
	}
	if err := os.MkdirAll("uploads/documents", 0700); err != nil {
		return fiber.NewError(500, "Unable to create document directory")
	}
	path := filepath.Join("uploads", "documents", uuid.NewString()+ext)
	if err := c.SaveFile(file, path); err != nil {
		return fiber.NewError(500, "Unable to save document")
	}
	doc, err := pc.Store.AddDriverDocument(id, kind, path, expires)
	if err != nil {
		_ = os.Remove(path)
		return verificationError(c, err)
	}
	return c.Status(201).JSON(doc)
}
func (pc *ProfileController) SubmitMyVerification(c *fiber.Ctx) error {
	id, err := pc.driverIdentity(c)
	if err != nil {
		return err
	}
	if err := pc.Store.SubmitDriverDocuments(id); err != nil {
		return verificationError(c, err)
	}
	return pc.GetMyVerification(c)
}
func (pc *ProfileController) SetMyAvailability(c *fiber.Ctx) error {
	id, err := pc.driverIdentity(c)
	if err != nil {
		return err
	}
	var input struct {
		Online *bool `json:"online"`
	}
	if err := c.BodyParser(&input); err != nil || input.Online == nil {
		return fiber.NewError(400, "online boolean is required")
	}
	if err := pc.Store.SetDriverOnline(id, *input.Online); err != nil {
		return verificationError(c, err)
	}
	return c.JSON(fiber.Map{"online": *input.Online})
}
func (pc *ProfileController) MyVerificationNotifications(c *fiber.Ctx) error {
	id, err := pc.driverIdentity(c)
	if err != nil {
		return err
	}
	if err := pc.Store.ExpireDriverDocuments(time.Now().UTC(), id); err != nil {
		return verificationError(c, err)
	}
	notes := []models.DriverNotification{}
	p, err := operationPage(c)
	if err != nil {
		return verificationError(c, err)
	}
	total, err := pc.Store.PageRows(pc.Store.DB().Model(&models.DriverNotification{}).Where("driver_id = ?", id), p, &notes)
	if err != nil {
		return verificationError(c, err)
	}
	return c.JSON(fiber.Map{"notifications": notes, "total": total, "limit": p.Limit, "offset": p.Offset})
}

func (fc *FileController) GetDocument(c *fiber.Ctx) error {
	var doc models.DriverDocument
	// Authenticate before revealing whether a private document exists.
	parts := strings.SplitN(c.Get("Authorization"), " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		return fiber.NewError(401, "Bearer token required")
	}
	isStaff := false
	ownerID := ""
	if claims, err := utilsParseStaff(parts[1], fc); err == nil {
		isStaff = claims
	} else if uid, role, err := utilsParseApp(parts[1], fc); err == nil && role == models.RoleDriver {
		ownerID = uid
	}
	if !isStaff && ownerID == "" {
		return fiber.NewError(401, "Active owner or staff token required")
	}
	if err := fc.Store.DB().First(&doc, "id = ?", c.Params("id")).Error; err != nil {
		return fiber.NewError(404, "Document not found")
	}
	if !isStaff && doc.DriverID != ownerID {
		return fiber.NewError(403, "Document belongs to another driver")
	}
	if doc.Status == "pending_upload" {
		return fiber.NewError(409, "Document upload is not confirmed")
	}
	if doc.ObjectKey != "" {
		if isStaff {
			claims, err := utils.ParseStaffToken(parts[1], fc.Cfg.StaffJWTSecret)
			if err != nil {
				return fiber.NewError(401, "Active staff token required")
			}
			actor, _ := claims["sub"].(string)
			if err := fc.Store.CreateAuditLog(actor, "view_driver_document", "driver_document", doc.ID, c.IP(), nil); err != nil {
				return fiber.NewError(500, "Unable to record document access")
			}
		}
		return fc.objectDocument(c, doc.ObjectKey, doc.ContentType, doc.ExpectedSize)
	}
	if c.QueryBool("presign") {
		return objectStorageError(c, storage.ErrPresignUnsupported)
	}
	absolute, err := filepath.Abs(doc.FilePath)
	if err != nil {
		return fiber.NewError(400, "Invalid document path")
	}
	allowed := false
	for _, root := range []string{"uploads/documents", "uploads/licences"} {
		base, _ := filepath.Abs(root)
		rel, err := filepath.Rel(base, absolute)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			allowed = true
		}
	}
	if !allowed {
		return fiber.NewError(400, "Invalid document path")
	}
	if _, err := os.Stat(absolute); err != nil {
		return fiber.NewError(404, "Document file not found")
	}
	c.Set("Cache-Control", "private, no-store")
	c.Set("X-Content-Type-Options", "nosniff")
	if isStaff {
		claims, _ := utils.ParseStaffToken(parts[1], fc.Cfg.StaffJWTSecret)
		actor, _ := claims["sub"].(string)
		if err := fc.Store.CreateAuditLog(actor, "view_driver_document", "driver_document", doc.ID, c.IP(), nil); err != nil {
			return fiber.NewError(500, "Unable to record document access")
		}
	}
	return c.SendFile(absolute)
}

func utilsParseStaff(token string, fc *FileController) (bool, error) {
	claims, err := utils.ParseStaffToken(token, fc.Cfg.StaffJWTSecret)
	if err != nil {
		return false, err
	}
	id, _ := claims["sub"].(string)
	staff, ok := fc.Store.GetStaffUserByID(id)
	if !ok || !staff.IsActive || staff.MustChangePassword {
		return false, errors.New("staff access denied")
	}
	if fc.Cfg.StaffRequire2FAForAdmin && staff.Role == models.StaffRoleAdmin && !staff.TOTPEnabled {
		return false, errors.New("admin 2FA required")
	}
	return true, nil
}
func utilsParseApp(token string, fc *FileController) (string, models.Role, error) {
	id, role, err := utils.ParseToken(token, fc.Cfg.JWTSecret)
	if err != nil {
		return "", "", err
	}
	u, accountErr := fc.Store.ActiveAccount(id)
	if accountErr != nil || u.Role != role {
		return "", "", errors.New("inactive account")
	}
	return id, role, nil
}
