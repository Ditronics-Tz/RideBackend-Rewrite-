package controllers

import (
	"bytes"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"ride-backend/config"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/storage"
	"ride-backend/store"
	"ride-backend/utils"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
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
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "driver role required"})
	}
	p := pc.Store.EnsureDriverProfile(uid)
	p.ProfilePictureURL = profilePictureURL(pc.Cfg, p.UserID, p.ProfilePictureURL, p.ProfilePictureKey)
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
	return pc.updateProfilePicture(c, uid, true)
}

// GetDriverProfileByID — any authenticated user can view a driver's public profile.
func (pc *ProfileController) GetDriverProfileByID(c *fiber.Ctx) error {
	id := c.Params("userId")
	p, ok := pc.Store.GetDriverProfile(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Driver profile not found"})
	}
	uid, role := middleware.CurrentUser(c)
	if uid == id && role == models.RoleDriver {
		p.ProfilePictureURL = profilePictureURL(pc.Cfg, p.UserID, p.ProfilePictureURL, p.ProfilePictureKey)
		return c.JSON(p)
	}
	user, ok := pc.Store.GetUserByID(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Driver profile not found"})
	}
	return c.JSON(models.PublicDriverProfile{UserID: id, Name: user.Name, VehicleType: p.VehicleType, ProfilePictureURL: profilePictureURL(pc.Cfg, p.UserID, p.ProfilePictureURL, p.ProfilePictureKey), AverageRating: p.AverageRating, TotalRatings: p.TotalRatings})
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
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "passenger role required"})
	}
	p := pc.Store.EnsurePassengerProfile(uid, user.Name)
	p.ProfilePictureURL = profilePictureURL(pc.Cfg, p.UserID, p.ProfilePictureURL, p.ProfilePictureKey)
	p.ProfileCompleted = strings.TrimSpace(user.Name) != "" && user.Phone != ""
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
	if input.FullName != nil {
		trimmed := strings.TrimSpace(*input.FullName)
		if trimmed == "" || utf8.RuneCountInString(trimmed) > 100 {
			return operationError(c, store.FieldError("full_name", "full_name must contain 1-100 characters"))
		}
		input.FullName = &trimmed
	}
	if input.Phone != nil && input.PhoneNumber != nil {
		return operationError(c, store.FieldError("phone_number", "provide only phone_number"))
	}
	if input.PhoneNumber == nil {
		input.PhoneNumber = input.Phone
	}
	p, err := pc.Store.UpdatePassengerBasics(uid, input.FullName, input.PhoneNumber)
	if err != nil {
		return operationError(c, err)
	}
	p.ProfilePictureURL = profilePictureURL(pc.Cfg, p.UserID, p.ProfilePictureURL, p.ProfilePictureKey)
	if user, ok := pc.Store.GetUserByID(uid); ok {
		p.ProfileCompleted = strings.TrimSpace(user.Name) != "" && user.Phone != ""
	}
	return c.JSON(p)
}

// UploadMyPassengerImage — multipart: profile_picture.
func (pc *ProfileController) UploadMyPassengerImage(c *fiber.Ctx) error {
	uid, role := middleware.CurrentUser(c)
	if role != models.RolePassenger {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "passenger role required"})
	}
	return pc.updateProfilePicture(c, uid, false)
}

func (pc *ProfileController) updateProfilePicture(c *fiber.Ctx, uid string, driver bool) error {
	mediaType, _, err := mime.ParseMediaType(c.Get(fiber.HeaderContentType))
	if err != nil || mediaType != "multipart/form-data" {
		return operationError(c, store.OpError(415, "multipart/form-data is required"))
	}
	form, err := c.MultipartForm()
	if err != nil || form == nil || len(form.File) != 1 || len(form.File["profile_picture"]) != 1 {
		return operationError(c, &store.OperationError{Status: 422, Message: "exactly one profile_picture file is required", Field: "profile_picture"})
	}
	data, err := utils.ReadProfileImage(c, "profile_picture")
	if err != nil {
		status := 422
		if errors.Is(err, utils.ErrProfileImageTooLarge) {
			status = 413
		} else if errors.Is(err, utils.ErrProfileImageUnsupported) {
			status = 415
		}
		return operationError(c, &store.OperationError{Status: status, Message: err.Error(), Field: "profile_picture"})
	}
	if pc.Cfg.StorageDriver == "r2" && pc.Store.Storage == nil {
		return operationError(c, store.OpError(503, "object storage is not configured"))
	}
	limiter := store.NewSQLAuthLimiter(pc.Store)
	decision, err := limiter.Allow(c.UserContext(), "profile_image_user", uid, store.AuthRate{Limit: 3, Window: time.Hour})
	if err != nil {
		return operationError(c, store.OpError(503, "profile image upload temporarily unavailable"))
	}
	if !decision.Allowed {
		c.Set("Retry-After", fmt.Sprint(decision.RetryAfter))
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "profile image upload quota exceeded"})
	}

	url := ""
	key := ""
	if pc.Cfg.StorageDriver == "r2" {
		key = "profile/" + uid + "/" + uuid.NewString() + ".png"
		if _, err := pc.Store.Storage.Put(c.UserContext(), storage.Public, key, storage.Upload{Body: bytes.NewReader(data), Size: int64(len(data)), ContentType: "image/png"}); err != nil {
			return objectStorageError(c, err)
		}
	} else {
		url, err = utils.SaveNormalizedPublicImage(data, map[bool]string{true: "driver_", false: "passenger_"}[driver]+uid)
		if err != nil {
			return operationError(c, store.FieldError("profile_picture", "unable to save image"))
		}
	}
	// Retain the previous reference until the replacement is safely stored.
	oldURL, oldKey := "", ""
	if driver {
		if previous, ok := pc.Store.GetDriverProfile(uid); ok {
			oldURL, oldKey = previous.ProfilePictureURL, previous.ProfilePictureKey
		}
	} else if previous, ok := pc.Store.GetPassengerProfile(uid); ok {
		oldURL, oldKey = previous.ProfilePictureURL, previous.ProfilePictureKey
	}
	model := any(&models.PassengerProfile{})
	if driver {
		pc.Store.EnsureDriverProfile(uid)
		model = &models.DriverProfile{}
	} else {
		user, _ := pc.Store.GetUserByID(uid)
		name := ""
		if user != nil {
			name = user.Name
		}
		pc.Store.EnsurePassengerProfile(uid, name)
	}
	values := map[string]any{"profile_picture_url": url}
	if key != "" {
		values = map[string]any{"profile_picture_key": key, "profile_picture_url": ""}
	}
	removeNewImage := func() {
		if key != "" && pc.Store.Storage != nil {
			_ = pc.Store.Storage.Delete(c.UserContext(), storage.Public, key)
		} else if strings.HasPrefix(url, "/api/v1/uploads/") {
			_ = os.Remove(filepath.Join("uploads", "public", strings.TrimPrefix(url, "/api/v1/uploads/")))
		}
	}
	result := pc.Store.DB().Model(model).Where("user_id = ? AND COALESCE(profile_picture_url, '') = ? AND COALESCE(profile_picture_key, '') = ?", uid, oldURL, oldKey).Updates(values)
	if result.Error != nil {
		removeNewImage()
		return verificationError(c, result.Error)
	}
	if result.RowsAffected != 1 {
		removeNewImage()
		return fiber.NewError(409, "Profile picture could not be updated")
	}
	if oldKey != "" && oldKey != key && pc.Store.Storage != nil {
		_ = pc.Store.Storage.Delete(c.UserContext(), storage.Public, oldKey)
	} else if oldURL != "" && oldURL != url && strings.HasPrefix(oldURL, "/api/v1/uploads/") {
		oldName := strings.TrimPrefix(oldURL, "/api/v1/uploads/")
		if filepath.Base(oldName) == oldName && strings.HasPrefix(oldName, map[bool]string{true: "driver_", false: "passenger_"}[driver]+uid+"_") {
			_ = os.Remove(filepath.Join("uploads", "public", oldName))
		}
	}
	if driver {
		profile, ok := pc.Store.GetDriverProfile(uid)
		if !ok {
			return fiber.NewError(404, "Driver profile not found")
		}
		profile.ProfilePictureURL = profilePictureURL(pc.Cfg, profile.UserID, profile.ProfilePictureURL, profile.ProfilePictureKey)
		return c.JSON(profile)
	}
	profile, ok := pc.Store.GetPassengerProfile(uid)
	if !ok {
		return fiber.NewError(404, "Passenger profile not found")
	}
	profile.ProfilePictureURL = profilePictureURL(pc.Cfg, profile.UserID, profile.ProfilePictureURL, profile.ProfilePictureKey)
	if user, ok := pc.Store.GetUserByID(uid); ok {
		profile.ProfileCompleted = strings.TrimSpace(user.Name) != "" && user.Phone != ""
	}
	return c.JSON(profile)
}

// GetPassengerProfileByID — any authenticated user can view a passenger's public profile.
func (pc *ProfileController) GetPassengerProfileByID(c *fiber.Ctx) error {
	id := c.Params("userId")
	p, ok := pc.Store.GetPassengerProfile(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Passenger profile not found"})
	}
	uid, role := middleware.CurrentUser(c)
	if uid == id && role == models.RolePassenger {
		p.ProfilePictureURL = profilePictureURL(pc.Cfg, p.UserID, p.ProfilePictureURL, p.ProfilePictureKey)
		if user, ok := pc.Store.GetUserByID(id); ok {
			p.ProfileCompleted = strings.TrimSpace(user.Name) != "" && user.Phone != ""
		}
		return c.JSON(p)
	}
	user, ok := pc.Store.GetUserByID(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Passenger profile not found"})
	}
	return c.JSON(models.PublicPassengerProfile{UserID: id, Name: user.Name, ProfilePictureURL: profilePictureURL(pc.Cfg, p.UserID, p.ProfilePictureURL, p.ProfilePictureKey), AverageRating: p.AverageRating, TotalRatings: p.TotalRatings})
}

func profilePictureURL(cfg *config.Config, userID, currentURL, key string) string {
	if key != "" {
		return strings.TrimRight(cfg.R2PublicBaseURL, "/") + "/" + key
	}
	if strings.HasPrefix(currentURL, "/api/v1/uploads/") {
		return strings.TrimRight(cfg.PublicBaseURL, "/") + currentURL
	}
	if parsed, err := url.Parse(currentURL); err == nil && parsed.Scheme == "https" && parsed.Host != "" {
		return currentURL
	}
	if currentURL != "" && cfg.AppEnv != "production" {
		return currentURL
	}
	return ""
}

// GetFullProfileForStaff is mounted only behind StaffProtected and the support/admin role check.
func (pc *ProfileController) GetFullProfileForStaff(c *fiber.Ctx) error {
	if middleware.CurrentStaff(c) == nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Unauthorized")
	}
	user, ok := pc.Store.GetUserByID(c.Params("id"))
	if !ok {
		return fiber.NewError(fiber.StatusNotFound, "User not found")
	}
	response := fiber.Map{"user": models.ToUserResponse(user)}
	if user.Role == models.RoleDriver {
		profile, exists := pc.Store.GetDriverProfile(user.ID)
		if exists {
			profile.ProfilePictureURL = profilePictureURL(pc.Cfg, profile.UserID, profile.ProfilePictureURL, profile.ProfilePictureKey)
			response["driver_profile"] = profile
		}
	}
	if user.Role == models.RolePassenger {
		profile, exists := pc.Store.GetPassengerProfile(user.ID)
		if exists {
			profile.ProfilePictureURL = profilePictureURL(pc.Cfg, profile.UserID, profile.ProfilePictureURL, profile.ProfilePictureKey)
			response["passenger_profile"] = profile
		}
	}
	return c.JSON(response)
}
