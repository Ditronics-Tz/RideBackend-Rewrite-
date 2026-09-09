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
	p := pc.Store.UpdateDriverProfile(uid, func(p *models.DriverProfile) {
		if input.LicenceNumber != "" {
			p.LicenceNumber = input.LicenceNumber
		}
		if input.VehicleCardNumber != "" {
			p.VehicleCardNumber = input.VehicleCardNumber
		}
		if input.PlateNumber != "" {
			p.PlateNumber = input.PlateNumber
		}
	})
	return c.JSON(p)
}

// UploadMyDriverImages — multipart: licence_image, profile_picture.
func (pc *ProfileController) UploadMyDriverImages(c *fiber.Ctx) error {
	uid, role := middleware.CurrentUser(c)
	if role != models.RoleDriver {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "driver role required"})
	}
	licURL, err := utils.SaveImageField(c, "licence_image", "licence_"+uid)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	profURL, err := utils.SaveImageField(c, "profile_picture", "driver_"+uid)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if licURL == "" && profURL == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "provide licence_image and/or profile_picture"})
	}
	p := pc.Store.UpdateDriverProfile(uid, func(p *models.DriverProfile) {
		if licURL != "" {
			p.LicenceImageURL = licURL
		}
		if profURL != "" {
			p.ProfilePictureURL = profURL
		}
	})
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
	p := pc.Store.UpdatePassengerProfile(uid, func(p *models.PassengerProfile) {
		if input.FullName != "" {
			p.FullName = input.FullName
		}
		if input.Phone != "" {
			p.Phone = input.Phone
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
	url, err := utils.SaveImageField(c, "profile_picture", "passenger_"+uid)
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
