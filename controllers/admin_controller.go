package controllers

import (
	"crypto/rand"
	"math/big"
	"strings"

	"github.com/google/uuid"
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
	if role != "" && role != "driver" && role != "passenger" {
		return invalidPromoBody(c, "role", "role must be driver or passenger")
	}
	p, err := operationPage(c)
	if err != nil {
		return verificationError(c, err)
	}
	users, total, err := ac.Store.SearchUsersFiltered(role, c.Query("q"), c.Query("status"), c.Query("verification_status"), p)
	if err != nil {
		return verificationError(c, err)
	}
	out := make([]models.UserResponse, 0, len(users))
	for _, u := range users {
		out = append(out, models.ToUserResponse(u))
	}
	return c.JSON(fiber.Map{"count": len(out), "total": total, "limit": p.Limit, "offset": p.Offset, "users": out})
}

// GetUser — support/admin. Includes profiles + rating summary.
func (ac *AdminController) GetUser(c *fiber.Ctx) error {
	id := c.Params("id")
	u, ok := ac.Store.GetUserByID(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
	}
	resp := fiber.Map{"user": models.ToUserResponse(u)}
	if _, ok := ac.Store.GetDriverProfile(id); ok {
		detail, err := ac.Store.VerificationDetail(id, true)
		if err != nil {
			return verificationError(c, err)
		}
		resp["driver_profile"] = detail.Profile
		resp["driver_documents"] = detail.Documents
	}
	if pp, ok := ac.Store.GetPassengerProfile(id); ok {
		resp["passenger_profile"] = pp
	}
	avg, total, err := ac.Store.RatingSummary(id)
	if err != nil {
		return verificationError(c, err)
	}
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
	return c.Status(fiber.StatusGone).JSON(fiber.Map{"error": "Boolean verification has been retired. Claim the driver and review each document through /admin/drivers/:id/verification."})
}

// ---------- Dashboard Work: Stats, Rides, Complaints, Tickets ----------

// GetStats — support/admin
func (ac *AdminController) GetStats(c *fiber.Ctx) error {
	drivers, passengers, err := ac.Store.UserCounts()
	if err != nil {
		return verificationError(c, err)
	}
	activeRides, openTickets, openComplaints, err := ac.Store.AdminOperationalCounts()
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{
		"total_drivers":                drivers,
		"total_passengers":             passengers,
		"operational_counts_available": true,
		"active_rides":                 activeRides,
		"open_tickets":                 openTickets,
		"open_complaints":              openComplaints,
	})
}

// ListRides — support/admin
func (ac *AdminController) ListRides(c *fiber.Ctx) error {
	page, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	rides, total, err := ac.Store.ListAdminRides(store.AdminRideFilter{Status: c.Query("status"), Passenger: c.Query("passenger"), Driver: c.Query("driver"), Q: c.Query("q"), From: c.Query("from"), To: c.Query("to")}, page)
	if err != nil {
		return operationError(c, err)
	}
	rows := make([]models.RideResponse, 0, len(rides))
	rideController := NewRideController(ac.Cfg, ac.Store)
	for i := range rides {
		row, err := rideController.RideResponse(c, &rides[i])
		if err != nil {
			return operationError(c, err)
		}
		rows = append(rows, *row)
	}
	return c.JSON(fiber.Map{"rides": rows, "total": total, "limit": page.Limit, "offset": page.Offset})
}

func (ac *AdminController) RideDetail(c *fiber.Ctx) error {
	var ride models.Ride
	if err := ac.Store.DB().First(&ride, "id = ?", c.Params("id")).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Ride not found"})
	}
	response, err := NewRideController(ac.Cfg, ac.Store).RideResponse(c, &ride)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(response)
}

func (ac *AdminController) CancelRide(c *fiber.Ctx) error {
	var input store.AdminRideCancelInput
	if len(c.Body()) > 2048 || c.BodyParser(&input) != nil {
		return fiber.NewError(400, "Invalid cancellation request")
	}
	if err := ac.Store.AdminCancelRide(c.Params("id"), middleware.CurrentStaff(c), input.Reason); err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"id": c.Params("id"), "status": "cancelled_by_admin"})
}

func (ac *AdminController) ReassignRide(c *fiber.Ctx) error {
	var input struct {
		DriverID string `json:"driver_id"`
		Reason   string `json:"reason"`
	}
	if len(c.Body()) > 2048 || c.BodyParser(&input) != nil {
		return fiber.NewError(400, "Invalid reassignment request")
	}
	if _, err := uuid.Parse(input.DriverID); err != nil {
		return operationError(c, store.FieldError("driver_id", "valid driver UUID required"))
	}
	if err := ac.Store.AdminReassignRide(c.Params("id"), input.DriverID, middleware.CurrentStaff(c), input.Reason); err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"id": c.Params("id"), "driver_id": input.DriverID, "status": "assigned"})
}

// ListComplaints — support/admin
func (ac *AdminController) ListComplaints(c *fiber.Ctx) error {
	return NewOperationsController(ac.Cfg, ac.Store).Reports(c)
}

// UpdateComplaint — support/admin
func (ac *AdminController) UpdateComplaint(c *fiber.Ctx) error {
	return NewOperationsController(ac.Cfg, ac.Store).ReportAction(c)
}

// ListTickets — support/admin
func (ac *AdminController) ListTickets(c *fiber.Ctx) error {
	page, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	tickets, total, err := ac.Store.ListSupportTickets(store.SupportTicketFilter{Status: c.Query("status"), Category: c.Query("category"), Priority: c.Query("priority"), Assignee: c.Query("assignee"), User: c.Query("user"), Q: c.Query("q"), From: c.Query("from"), To: c.Query("to")}, page)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"tickets": tickets, "total": total, "limit": page.Limit, "offset": page.Offset})
}

func (ac *AdminController) TicketDetail(c *fiber.Ctx) error {
	page, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	out, err := ac.Store.SupportTicketDetail(c.Params("id"), page)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(out)
}

// UpdateTicket — support/admin
func (ac *AdminController) UpdateTicket(c *fiber.Ctx) error {
	var input store.WorkSupportTicketInput
	if len(c.Body()) > 8192 || c.BodyParser(&input) != nil {
		return fiber.NewError(400, "Invalid ticket update")
	}
	actor := middleware.CurrentStaff(c)
	if err := ac.Store.WorkSupportTicket(c.Params("id"), actor, input); err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"id": c.Params("id"), "action": input.Action, "updated": true})
}

func (ac *AdminController) ListRatings(c *fiber.Ctx) error {
	page, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	hidden := c.Query("hidden", "false")
	if hidden != "true" && hidden != "false" && hidden != "all" {
		return operationError(c, store.FieldError("hidden", "hidden must be true, false or all"))
	}
	rows, total, err := ac.Store.AdminRatings(hidden, page)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"ratings": rows, "total": total, "limit": page.Limit, "offset": page.Offset})
}

func (ac *AdminController) ModerateRating(c *fiber.Ctx) error {
	actor := middleware.CurrentStaff(c)
	action := "restore"
	if strings.HasSuffix(c.Route().Path, "/hide") {
		action = "hide"
	}
	hidden := action == "hide"
	if err := ac.Store.ModerateRating(c.Params("id"), actor.ID, hidden); err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"id": c.Params("id"), "hidden": hidden})
}

// ---------- Audit Logs (Admin only) ----------

// ListAuditLogs — Admin only
func (ac *AdminController) ListAuditLogs(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return verificationError(c, err)
	}
	logs, total, err := ac.Store.SearchAuditLogs(store.AuditFilter{Q: c.Query("q"), Action: c.Query("action"), Actor: c.Query("actor", c.Query("actor_id")), From: c.Query("from"), To: c.Query("to")}, p)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch audit logs"})
	}
	return c.JSON(fiber.Map{
		"total":  total,
		"limit":  p.Limit,
		"offset": p.Offset,
		"logs":   logs,
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
		"role": staff.Role,
	})

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"message":           "Staff account created successfully",
		"staff":             models.ToStaffUserResponse(staff),
		"one_time_password": otp,
	})
}

// ListStaff lists all staff members.
func (ac *AdminController) ListStaff(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return verificationError(c, err)
	}
	staffList, total, err := ac.Store.SearchStaff(c.Query("q"), c.Query("role"), c.Query("status"), p)
	if err != nil {
		return verificationError(c, err)
	}
	out := make([]models.StaffUserResponse, 0, len(staffList))
	for _, s := range staffList {
		out = append(out, models.ToStaffUserResponse(s))
	}
	return c.JSON(fiber.Map{"count": len(out), "staff": out, "total": total, "limit": p.Limit, "offset": p.Offset})
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
		"message":           "Password reset successfully",
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
