package controllers

import (
	"ride-backend/config"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
	"ride-backend/utils"

	"github.com/gofiber/fiber/v2"
)

type ProfileController struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewProfileController(cfg *config.Config, s *store.Store) *ProfileController {
	return &ProfileController{Cfg: cfg, Store: s}
}

// ---------- Driver ----------

// GetMyDriverProfile — driver views own profile.
func (pc *ProfileController) GetMyDriverProfile(c *fiber.Ctx) error {
	uid, role := middleware.CurrentUser(c)
	if role != models.RoleDriver {
		// admins/support/mzee can still have no driver profile — return 404 style message
		if p, ok := pc.Store.GetDriverProfile(uid); ok {
			return c.JSON(p)
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "driver role required"})
	}
	p := pc.Store.EnsureDriverProfile(uid)
	return c.JSON(p)
}

// UpdateMyDriverProfile — driver updates licence / kadi ya gari / plate number.
func (pc *ProfileController) UpdateMyDriverProfile(c *fiber.Ctx) error {
	uid, role := middleware.CurrentUser(c)
	if role != models.RoleDriver {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "driver role required"})
	}
	var input models.DriverProfileInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	p, err := pc.Store.UpdateDriverIdentifiers(uid, input)
	if err != nil {
		return verificationError(c, err)
	}
	return c.JSON(p)
}

// UploadMyDriverImages — multipart: profile_picture. Documents use their own route.
func (pc *ProfileController) UploadMyDriverImages(c *fiber.Ctx) error {
	uid, role := middleware.CurrentUser(c)
	if role != models.RoleDriver {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "driver role required"})
	}
	if _, err := c.FormFile("licence_image"); err == nil {
		return c.Status(410).JSON(fiber.Map{"error": "Upload each document through /driver/documents with type, file and expires_at. Profile pictures still use this endpoint."})
	}
	if pc.Cfg.StorageDriver == "r2" {
		return pc.objectProfileImage(c, uid, true)
	}
	profURL, err := utils.SavePublicImage(c, "profile_picture", "driver_"+uid)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if profURL == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "provide profile_picture"})
	}
	pc.Store.EnsureDriverProfile(uid)
	// A picture update must not overwrite a concurrent verification decision.
	if err := pc.Store.DB().Model(&models.DriverProfile{}).Where("user_id = ?", uid).Update("profile_picture_url", profURL).Error; err != nil {
		return verificationError(c, err)
	}
	p, _ := pc.Store.GetDriverProfile(uid)
	return c.JSON(p)
}

// GetDriverProfileByID — any authenticated user can view a driver's public profile.
func (pc *ProfileController) GetDriverProfileByID(c *fiber.Ctx) error {
	id := c.Params("userId")
	p, ok := pc.Store.GetDriverProfile(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Driver profile not found"})
	}
	return c.JSON(p)
}

// ---------- Passenger ----------

// GetMyPassengerProfile — passenger views own profile.
func (pc *ProfileController) GetMyPassengerProfile(c *fiber.Ctx) error {
	uid, role := middleware.CurrentUser(c)
	user, ok := pc.Store.GetUserByID(uid)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
	}
	if role != models.RolePassenger {
		if p, ok := pc.Store.GetPassengerProfile(uid); ok {
			return c.JSON(p)
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "passenger role required"})
	}
	p := pc.Store.EnsurePassengerProfile(uid, user.Name)
	return c.JSON(p)
}

// UpdateMyPassengerProfile — passenger updates name/phone.
func (pc *ProfileController) UpdateMyPassengerProfile(c *fiber.Ctx) error {
	uid, role := middleware.CurrentUser(c)
	if role != models.RolePassenger {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "passenger role required"})
	}
	var input models.PassengerProfileInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	if input.Phone != "" {
		return fiber.NewError(400, "Verify phone through /auth/phone/request-otp and /auth/phone/verify-otp")
	}
	p := pc.Store.UpdatePassengerProfile(uid, func(p *models.PassengerProfile) {
		if input.FullName != "" {
			p.FullName = input.FullName
		}
	})
	return c.JSON(p)
}

// UploadMyPassengerImage — multipart: profile_picture.
func (pc *ProfileController) UploadMyPassengerImage(c *fiber.Ctx) error {
	uid, role := middleware.CurrentUser(c)
	if role != models.RolePassenger {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "passenger role required"})
	}
	if pc.Cfg.StorageDriver == "r2" {
		return pc.objectProfileImage(c, uid, false)
	}
	url, err := utils.SavePublicImage(c, "profile_picture", "passenger_"+uid)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if url == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "provide profile_picture file"})
	}
	p := pc.Store.UpdatePassengerProfile(uid, func(p *models.PassengerProfile) {
		p.ProfilePictureURL = url
	})
	return c.JSON(p)
}

// GetPassengerProfileByID — any authenticated user can view a passenger's public profile.
func (pc *ProfileController) GetPassengerProfileByID(c *fiber.Ctx) error {
	id := c.Params("userId")
	p, ok := pc.Store.GetPassengerProfile(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Passenger profile not found"})
	}
	return c.JSON(p)
}
