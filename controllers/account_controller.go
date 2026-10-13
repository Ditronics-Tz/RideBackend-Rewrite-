package controllers

import (
	"github.com/gofiber/fiber/v2"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
	"strings"
	"time"
)

func (ac *AdminController) ChangeAccountStatus(c *fiber.Ctx) error {
	var input struct {
		Reason string     `json:"reason"`
		Until  *time.Time `json:"until"`
	}
	if err := c.BodyParser(&input); err != nil {
		return fiber.NewError(400, "reason and optional RFC3339 until are required")
	}
	status := "suspended"
	if strings.HasSuffix(c.Path(), "/reactivate") {
		status = "active"
	}
	if strings.HasSuffix(c.Path(), "/ban") {
		status = "banned"
	}
	if err := ac.Store.ChangeAccountStatus(c.Params("id"), middleware.CurrentStaff(c), status, input.Reason, input.Until); err != nil {
		return verificationError(c, err)
	}
	return ac.GetUser(c)
}
func (ac *AdminController) UserNotes(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, ok := ac.Store.GetUserByID(id); !ok {
		return fiber.NewError(404, "User not found")
	}
	if c.Method() == "POST" {
		var input struct {
			Body string `json:"body"`
		}
		if err := c.BodyParser(&input); err != nil {
			return fiber.NewError(400, "body is required")
		}
		note, err := ac.Store.AddSupportNote(id, middleware.CurrentStaff(c), input.Body)
		if err != nil {
			return verificationError(c, err)
		}
		return c.Status(201).JSON(note)
	}
	notes := []models.SupportNote{}
	p, err := operationPage(c)
	if err != nil {
		return verificationError(c, err)
	}
	total, err := ac.Store.PageRows(ac.Store.DB().Model(&models.SupportNote{}).Where("user_id = ?", id), p, &notes)
	if err != nil {
		return verificationError(c, err)
	}
	return c.JSON(fiber.Map{"notes": notes, "total": total, "limit": p.Limit, "offset": p.Offset})
}
func (ac *AdminController) DeleteUserNote(c *fiber.Ctx) error {
	result := ac.Store.DB().Where("id = ? AND user_id = ?", c.Params("noteId"), c.Params("id")).Delete(&models.SupportNote{})
	if result.Error != nil {
		return verificationError(c, result.Error)
	}
	if result.RowsAffected == 0 {
		return fiber.NewError(404, "Note not found")
	}
	return c.SendStatus(204)
}
func (ac *AdminController) UserHistory(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, ok := ac.Store.GetUserByID(id); !ok {
		return fiber.NewError(404, "User not found")
	}
	events := []models.UserStatusEvent{}
	reviews := []models.VerificationReview{}
	p, err := operationPage(c)
	if err != nil {
		return verificationError(c, err)
	}
	eventTotal, err := ac.Store.PageRows(ac.Store.DB().Model(&models.UserStatusEvent{}).Where("user_id = ?", id), p, &events)
	if err != nil {
		return verificationError(c, err)
	}
	reviewTotal, err := ac.Store.PageRows(ac.Store.DB().Model(&models.VerificationReview{}).Where("driver_id = ?", id), p, &reviews)
	if err != nil {
		return verificationError(c, err)
	}
	return c.JSON(fiber.Map{"status_events": events, "verification_reviews": reviews, "total": max(eventTotal, reviewTotal), "totals": fiber.Map{"status_events": eventTotal, "verification_reviews": reviewTotal}, "limit": p.Limit, "offset": p.Offset})
}
func (ac *AdminController) VerificationQueue(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return verificationError(c, err)
	}
	if err := ac.Store.ExpireDriverDocuments(time.Now(), ""); err != nil {
		return verificationError(c, err)
	}
	status := c.Query("status")
	profiles := []models.DriverProfile{}
	q := ac.Store.DB().Model(&models.DriverProfile{}).Joins("JOIN users ON users.id = driver_profiles.user_id AND users.role = ?", models.RoleDriver).Order("driver_profiles.updated_at ASC")
	if status != "" {
		switch models.VerificationStatus(status) {
		case models.PendingDocuments, models.Submitted, models.UnderReview, models.Approved, models.Rejected, models.Suspended, models.Expired:
		default:
			return fiber.NewError(400, "Invalid verification status")
		}
		q = q.Where("verification_status = ?", status)
	} else {
		q = q.Where("verification_status IN ?", []models.VerificationStatus{models.Submitted, models.UnderReview})
	}
	if search := strings.ToLower(strings.TrimSpace(c.Query("q"))); search != "" {
		q = q.Where("LOWER(users.name) LIKE ? OR LOWER(users.email) LIKE ? OR users.phone LIKE ? OR driver_profiles.plate_number LIKE ?", "%"+search+"%", "%"+search+"%", "%"+search+"%", "%"+store.CanonicalIdentifier(search)+"%")
	}
	if c.Query("expiring_soon") == "true" {
		q = q.Where("driver_profiles.user_id IN (?)", ac.Store.DB().Model(&models.DriverDocument{}).Select("driver_id").Where("is_current = ? AND expires_at > ? AND expires_at <= ?", true, time.Now().UTC(), time.Now().UTC().AddDate(0, 0, 30)))
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return verificationError(c, err)
	}
	if err := q.Select("driver_profiles.*").Limit(p.Limit).Offset(p.Offset).Find(&profiles).Error; err != nil {
		return verificationError(c, err)
	}
	return c.JSON(fiber.Map{"verifications": profiles, "count": len(profiles), "total": total, "limit": p.Limit, "offset": p.Offset})
}
func (ac *AdminController) ReviewDocumentByID(c *fiber.Ctx) error {
	var doc models.DriverDocument
	if err := ac.Store.DB().First(&doc, "id = ?", c.Params("documentId")).Error; err != nil {
		return verificationError(c, err)
	}
	c.Locals("review_driver_id", doc.DriverID)
	return ac.ReviewDriverDocument(c)
}
func (ac *AdminController) FinalApproveDriver(c *fiber.Ctx) error {
	if err := ac.Store.ApproveDriver(c.Params("id"), middleware.CurrentStaff(c)); err != nil {
		return verificationError(c, err)
	}
	return ac.GetDriverVerification(c)
}
