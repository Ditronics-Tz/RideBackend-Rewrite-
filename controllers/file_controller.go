package controllers

import (
	"os"
	"path/filepath"
	"strings"

	"ride-backend/config"
	"ride-backend/models"
	"ride-backend/store"
	"ride-backend/utils"

	"github.com/gofiber/fiber/v2"
)

type FileController struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewFileController(cfg *config.Config, s *store.Store) *FileController {
	return &FileController{Cfg: cfg, Store: s}
}

// GetLicence serves private driver licence documents.
// Only the owning driver or staff (admin/support) may read.
func (fc *FileController) GetLicence(c *fiber.Ctx) error {
	idParam := c.Params("id")
	if idParam == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Licence ID or filename is required"})
	}

	authHeader := c.Get("Authorization")
	if authHeader == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Missing or invalid token"})
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid token format"})
	}
	tokenStr := parts[1]

	isStaff := false
	var appUserID string
	var appRole models.Role

	// Check if token is a valid staff token
	if claims, err := utils.ParseStaffToken(tokenStr, fc.Cfg.StaffJWTSecret); err == nil {
		staffID, _ := claims["sub"].(string)
		if staff, ok := fc.Store.GetStaffUserByID(staffID); ok && staff.IsActive {
			isStaff = true
		}
	}

	// If not staff, check if token is a valid app token
	if !isStaff {
		if uid, role, err := utils.ParseToken(tokenStr, fc.Cfg.JWTSecret); err == nil {
			appUserID = uid
			appRole = role
		}
	}

	if !isStaff && appUserID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized token"})
	}

	// Resolve the filename and owning driver ID
	filename := filepath.Base(idParam)
	var owningDriverID string

	// 1. Check if idParam is a driver user_id
	if dp, ok := fc.Store.GetDriverProfile(idParam); ok && dp.LicenceImageURL != "" {
		owningDriverID = dp.UserID
		filename = filepath.Base(dp.LicenceImageURL)
	} else {
		// 2. Check if any driver profile matches this licence filename
		var dp models.DriverProfile
		if err := fc.Store.DB().Where("licence_image_url LIKE ?", "%"+filename).First(&dp).Error; err == nil {
			owningDriverID = dp.UserID
		} else if strings.HasPrefix(filename, "licence_") {
			parts := strings.Split(filename, "_")
			if len(parts) >= 2 {
				potentialUID := parts[1]
				if dp2, ok := fc.Store.GetDriverProfile(potentialUID); ok {
					owningDriverID = dp2.UserID
				}
			}
		}
	}

	// Authorization check:
	// Only staff or owning driver (with role driver) may access
	if !isStaff {
		if appRole != models.RoleDriver || owningDriverID == "" || owningDriverID != appUserID {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: access to licence document denied"})
		}
	}

	// Verify file path within ./uploads/licences
	cleanPath := filepath.Clean(filepath.Join("./uploads/licences", filename))
	rel, err := filepath.Rel("./uploads/licences", cleanPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid file path"})
	}

	if _, err := os.Stat(cleanPath); os.IsNotExist(err) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Licence document not found"})
	}

	return c.SendFile(cleanPath)
}
