package controllers

import (
	"strings"

	"ride-backend/config"
	"ride-backend/models"
	"ride-backend/store"

	"github.com/gofiber/fiber/v2"
)

type AdminController struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewAdminController(cfg *config.Config, s *store.Store) *AdminController {
	return &AdminController{Cfg: cfg, Store: s}
}

// ListUsers — support/mzee/admin. Optional ?role=driver|passenger|...
func (ac *AdminController) ListUsers(c *fiber.Ctx) error {
	role := strings.ToLower(c.Query("role"))
	users := ac.Store.ListUsers(role)
	out := make([]models.UserResponse, 0, len(users))
	for _, u := range users {
		out = append(out, models.ToUserResponse(u))
	}
	return c.JSON(fiber.Map{"count": len(out), "users": out})
}

// GetUser — support/mzee/admin. Includes profiles + rating summary.
func (ac *AdminController) GetUser(c *fiber.Ctx) error {
	id := c.Params("id")
	u, ok := ac.Store.GetUserByID(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
	}
	resp := fiber.Map{"user": models.ToUserResponse(u)}
	if dp, ok := ac.Store.GetDriverProfile(id); ok {
		resp["driver_profile"] = dp
	}
	if pp, ok := ac.Store.GetPassengerProfile(id); ok {
		resp["passenger_profile"] = pp
	}
	_, avg, total := ac.Store.ListRatingsForUser(id)
	resp["average_rating"] = avg
	resp["total_ratings"] = total
	return c.JSON(resp)
}

// UpdateUserRole — admin + mzee only. Body: {"role": "support"|...}
func (ac *AdminController) UpdateUserRole(c *fiber.Ctx) error {
	id := c.Params("id")
	var input models.UpdateRoleInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	role := models.NormalizeRole(strings.ToLower(strings.TrimSpace(input.Role)))
	if !models.IsValidRole(role) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid role, use one of: mzee, driver, passenger, support, admin",
		})
	}
	u, err := ac.Store.UpdateUserRole(id, role)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"message": "role updated", "user": models.ToUserResponse(u)})
}

// VerifyDriver — admin/mzee/support verify a driver's documents.
func (ac *AdminController) VerifyDriver(c *fiber.Ctx) error {
	id := c.Params("id")
	var input models.VerifyDriverInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	p, err := ac.Store.SetDriverVerified(id, input.Verified)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"message": "driver verification updated", "driver_profile": p})
}
