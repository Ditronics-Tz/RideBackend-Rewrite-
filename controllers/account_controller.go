package controllers

import (
	"github.com/gofiber/fiber/v2"
	"ride-backend/middleware"
	"ride-backend/models"
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
	if err := ac.Store.DB().Where("user_id = ?", id).Order("created_at DESC").Find(&notes).Error; err != nil {
		return verificationError(c, err)
	}
	return c.JSON(fiber.Map{"notes": notes})
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
	if err := ac.Store.DB().Where("user_id = ?", id).Order("created_at DESC").Find(&events).Error; err != nil {
		return verificationError(c, err)
	}
	if err := ac.Store.DB().Where("driver_id = ?", id).Order("created_at DESC").Find(&reviews).Error; err != nil {
		return verificationError(c, err)
	}
	return c.JSON(fiber.Map{"status_events": events, "verification_reviews": reviews})
}
func (ac *AdminController) VerificationQueue(c *fiber.Ctx) error {
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
	if err := q.Select("driver_profiles.*").Find(&profiles).Error; err != nil {
		return verificationError(c, err)
	}
	return c.JSON(fiber.Map{"verifications": profiles, "count": len(profiles)})
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
