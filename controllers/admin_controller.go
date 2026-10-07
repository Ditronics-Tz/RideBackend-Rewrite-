package controllers

import (
	"crypto/rand"
	"math/big"
	"strconv"
	"strings"

	"ride-backend/config"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

type AdminController struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewAdminController(cfg *config.Config, s *store.Store) *AdminController {
	return &AdminController{Cfg: cfg, Store: s}
}

// ---------- App User Management (Support + Admin) ----------

// ListUsers — support/admin. Optional ?role=driver|passenger
func (ac *AdminController) ListUsers(c *fiber.Ctx) error {
	role := strings.ToLower(c.Query("role"))
	users := ac.Store.ListUsers(role)
	out := make([]models.UserResponse, 0, len(users))
	for _, u := range users {
		out = append(out, models.ToUserResponse(u))
	}
	return c.JSON(fiber.Map{"count": len(out), "users": out})
}

// GetUser — support/admin. Includes profiles + rating summary.
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

// UpdateUserRole — Admin only. Body: {"role": "driver"|"passenger"}
func (ac *AdminController) UpdateUserRole(c *fiber.Ctx) error {
	id := c.Params("id")
	var input models.UpdateRoleInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	role, err := models.ParseRole(input.Role)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	u, err := ac.Store.UpdateUserRole(id, role)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	staff := middleware.CurrentStaff(c)
	if staff != nil {
		_ = ac.Store.CreateAuditLog(staff.ID, "update_app_user_role", "user", id, c.IP(), fiber.Map{
			"new_role": role,
		})
	}
	return c.JSON(fiber.Map{"message": "role updated", "user": models.ToUserResponse(u)})
}

// VerifyDriver — support/admin verify a driver's documents.
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
	staff := middleware.CurrentStaff(c)
	if staff != nil {
		_ = ac.Store.CreateAuditLog(staff.ID, "verify_driver", "driver_profile", id, c.IP(), fiber.Map{
			"verified": input.Verified,
		})
	}
	return c.JSON(fiber.Map{"message": "driver verification updated", "driver_profile": p})
}

// ---------- Dashboard Work: Stats, Rides, Complaints, Tickets ----------

// GetStats — support/admin
func (ac *AdminController) GetStats(c *fiber.Ctx) error {
	drivers := len(ac.Store.ListUsers("driver"))
	passengers := len(ac.Store.ListUsers("passenger"))
	return c.JSON(fiber.Map{
		"total_drivers":    drivers,
		"total_passengers": passengers,
		"active_rides":     0,
		"open_tickets":     0,
		"open_complaints":  0,
	})
}

// ListRides — support/admin
func (ac *AdminController) ListRides(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"count": 0, "rides": []interface{}{}})
}

// ListComplaints — support/admin
func (ac *AdminController) ListComplaints(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"count": 0, "complaints": []interface{}{}})
}

// UpdateComplaint — support/admin
func (ac *AdminController) UpdateComplaint(c *fiber.Ctx) error {
	id := c.Params("id")
	staff := middleware.CurrentStaff(c)
	if staff != nil {
		_ = ac.Store.CreateAuditLog(staff.ID, "work_complaint", "complaint", id, c.IP(), nil)
	}
	return c.JSON(fiber.Map{"message": "complaint updated", "id": id, "status": "resolved"})
}

// ListTickets — support/admin
func (ac *AdminController) ListTickets(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"count": 0, "tickets": []interface{}{}})
}

// UpdateTicket — support/admin
func (ac *AdminController) UpdateTicket(c *fiber.Ctx) error {
	id := c.Params("id")
	staff := middleware.CurrentStaff(c)
	if staff != nil {
		_ = ac.Store.CreateAuditLog(staff.ID, "work_ticket", "ticket", id, c.IP(), nil)
	}
	return c.JSON(fiber.Map{"message": "ticket updated", "id": id, "status": "in_progress"})
}

// ---------- Audit Logs (Admin only) ----------

// ListAuditLogs — Admin only
func (ac *AdminController) ListAuditLogs(c *fiber.Ctx) error {
	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))

	logs, total, err := ac.Store.ListAuditLogs(limit, offset)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch audit logs"})
	}
	return c.JSON(fiber.Map{
		"total": total,
		"limit": limit,
		"logs":  logs,
	})
}

// ---------- Staff Management (Admin only) ----------

// CreateStaff creates a new staff account with a generated one-time password.
func (ac *AdminController) CreateStaff(c *fiber.Ctx) error {
	caller := middleware.CurrentStaff(c)
	if caller == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}

	var input models.CreateStaffInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	name := strings.TrimSpace(input.Name)
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if name == "" || email == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name and email are required"})
	}

	role, err := models.ParseStaffRole(input.Role)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	otp := generateSecurePassword(16)
	hashed, err := bcrypt.GenerateFromPassword([]byte(otp), 12)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to hash password"})
	}

	staff, err := ac.Store.CreateStaffUser(name, email, string(hashed), role, &caller.ID, true)
	if err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	}

	// Never log the one-time password!
	_ = ac.Store.CreateAuditLog(caller.ID, "create_staff", "staff_user", staff.ID, c.IP(), fiber.Map{
		"email": staff.Email,
		"role":  staff.Role,
	})

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"message":            "Staff account created successfully",
		"staff":              models.ToStaffUserResponse(staff),
		"one_time_password": otp,
	})
}

// ListStaff lists all staff members.
func (ac *AdminController) ListStaff(c *fiber.Ctx) error {
	staffList := ac.Store.ListStaffUsers()
	out := make([]models.StaffUserResponse, 0, len(staffList))
	for _, s := range staffList {
		out = append(out, models.ToStaffUserResponse(s))
	}
	return c.JSON(fiber.Map{"count": len(out), "staff": out})
}

// UpdateStaff updates staff name or is_active status.
func (ac *AdminController) UpdateStaff(c *fiber.Ctx) error {
	caller := middleware.CurrentStaff(c)
	if caller == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}

	id := c.Params("id")
	target, ok := ac.Store.GetStaffUserByID(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Staff member not found"})
	}

	var input models.UpdateStaffInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if input.IsActive != nil && !*input.IsActive {
		// An admin cannot deactivate themselves
		if id == caller.ID {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot deactivate your own account"})
		}
		// The last active admin can never be removed or demoted
		if target.Role == models.StaffRoleAdmin && ac.Store.CountActiveAdmins() <= 1 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot deactivate the last active admin"})
		}
	}

	updated, err := ac.Store.UpdateStaffUser(id, func(u *models.StaffUser) {
		if input.Name != nil && strings.TrimSpace(*input.Name) != "" {
			u.Name = strings.TrimSpace(*input.Name)
		}
		if input.IsActive != nil {
			u.IsActive = *input.IsActive
		}
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update staff"})
	}

	middleware.InvalidateStaffCache(id)

	if input.IsActive != nil && !*input.IsActive {
		_ = ac.Store.RevokeAllStaffSessions(id)
		_ = ac.Store.CreateAuditLog(caller.ID, "deactivate_staff", "staff_user", id, c.IP(), nil)
	} else {
		_ = ac.Store.CreateAuditLog(caller.ID, "update_staff", "staff_user", id, c.IP(), nil)
	}

	return c.JSON(fiber.Map{"message": "Staff updated successfully", "staff": models.ToStaffUserResponse(updated)})
}

// UpdateStaffRole updates a staff member's role.
func (ac *AdminController) UpdateStaffRole(c *fiber.Ctx) error {
	caller := middleware.CurrentStaff(c)
	if caller == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}

	id := c.Params("id")
	target, ok := ac.Store.GetStaffUserByID(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Staff member not found"})
	}

	var input models.UpdateStaffRoleInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	newRole, err := models.ParseStaffRole(input.Role)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	// An admin cannot change their own role
	if id == caller.ID {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot change your own role"})
	}

	// The last active admin can never be demoted
	if target.Role == models.StaffRoleAdmin && newRole != models.StaffRoleAdmin && ac.Store.CountActiveAdmins() <= 1 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot demote the last active admin"})
	}

	updated, err := ac.Store.UpdateStaffUser(id, func(u *models.StaffUser) {
		u.Role = newRole
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update role"})
	}

	middleware.InvalidateStaffCache(id)
	_ = ac.Store.CreateAuditLog(caller.ID, "role_change", "staff_user", id, c.IP(), fiber.Map{
		"old_role": target.Role,
		"new_role": newRole,
	})

	return c.JSON(fiber.Map{"message": "Staff role updated", "staff": models.ToStaffUserResponse(updated)})
}

// ResetStaffPassword generates a new one-time password and revokes all sessions.
func (ac *AdminController) ResetStaffPassword(c *fiber.Ctx) error {
	caller := middleware.CurrentStaff(c)
	if caller == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}

	id := c.Params("id")
	_, ok := ac.Store.GetStaffUserByID(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Staff member not found"})
	}

	otp := generateSecurePassword(16)
	hashed, err := bcrypt.GenerateFromPassword([]byte(otp), 12)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to hash password"})
	}

	_, err = ac.Store.UpdateStaffUser(id, func(u *models.StaffUser) {
		u.PasswordHash = string(hashed)
		u.MustChangePassword = true
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to reset password"})
	}

	middleware.InvalidateStaffCache(id)
	_ = ac.Store.RevokeAllStaffSessions(id)
	_ = ac.Store.CreateAuditLog(caller.ID, "reset_password", "staff_user", id, c.IP(), nil)

	return c.JSON(fiber.Map{
		"message":            "Password reset successfully",
		"one_time_password": otp,
	})
}

// RevokeStaffSessions revokes all sessions for a staff member.
func (ac *AdminController) RevokeStaffSessions(c *fiber.Ctx) error {
	caller := middleware.CurrentStaff(c)
	if caller == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}

	id := c.Params("id")
	_, ok := ac.Store.GetStaffUserByID(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Staff member not found"})
	}

	_ = ac.Store.RevokeAllStaffSessions(id)
	_ = ac.Store.CreateAuditLog(caller.ID, "session_revocation", "staff_user", id, c.IP(), nil)

	return c.JSON(fiber.Map{"message": "All sessions revoked"})
}

func generateSecurePassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
	res := make([]byte, length)
	for i := range res {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		res[i] = charset[n.Int64()]
	}
	return string(res)
}
