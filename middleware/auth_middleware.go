package middleware

import (
	"strings"
	"sync"
	"time"

	"ride-backend/config"
	"ride-backend/models"
	"ride-backend/utils"

	"github.com/gofiber/fiber/v2"
)

// AccountRequestLimiter limits credential-sensitive deletion/export POSTs per
// authenticated account. Persistent daily export limits are also enforced by Store.
func AccountRequestLimiter() fiber.Handler {
	var mu sync.Mutex
	buckets := map[string][]time.Time{}
	lastSweep := time.Now()
	return func(c *fiber.Ctx) error {
		id, _ := CurrentUser(c)
		key := id + ":" + c.Path()
		now := time.Now()
		cutoff := now.Add(-time.Minute)
		mu.Lock()
		if now.Sub(lastSweep) >= time.Minute {
			for k, times := range buckets {
				if len(times) == 0 || !times[len(times)-1].After(cutoff) {
					delete(buckets, k)
				}
			}
			lastSweep = now
		}
		recent := buckets[key]
		start := 0
		for start < len(recent) && !recent[start].After(cutoff) {
			start++
		}
		recent = recent[start:]
		if len(recent) >= 5 {
			mu.Unlock()
			c.Set("Retry-After", "60")
			return c.Status(429).JSON(fiber.Map{"error": "Too many account requests; try again in a minute"})
		}
		buckets[key] = append(recent, now)
		mu.Unlock()
		return c.Next()
	}
}

// In-memory cache for staff users (reloaded every 30s as required).
type staffCacheEntry struct {
	staff     *models.StaffUser
	fetchedAt time.Time
}

var (
	staffCacheMu sync.RWMutex
	staffCache   = make(map[string]staffCacheEntry)
)

// InvalidateStaffCache clears cached entry for a staff ID (e.g. after role change/deactivation).
func InvalidateStaffCache(staffID string) {
	staffCacheMu.Lock()
	delete(staffCache, staffID)
	staffCacheMu.Unlock()
}

// AppProtected validates the Bearer JWT for the "app" realm (passengers and drivers).
// Rejects staff tokens or invalid/expired tokens with 401.
type AppAccountGetter interface {
	ActiveAccount(string) (*models.User, error)
}
type appRequestAccountGetter interface {
	AccountForRequest(string) (*models.User, error)
}

func AppProtected(cfg *config.Config, accounts ...AppAccountGetter) fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Missing or invalid token"})
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid token format"})
		}

		claims, err := utils.ParseAppToken(parts[1], cfg.JWTSecret)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized token"})
		}

		uid, _ := claims["sub"].(string)
		if uid == "" {
			uid, _ = claims["user_id"].(string)
		}
		if uid == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized token"})
		}

		rStr, _ := claims["role"].(string)
		role, err := models.ParseRole(rStr)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized role"})
		}

		var account *models.User
		if len(accounts) > 0 {
			var u *models.User
			if current, ok := accounts[0].(appRequestAccountGetter); ok {
				u, err = current.AccountForRequest(uid)
			} else {
				u, err = accounts[0].ActiveAccount(uid)
			}
			if err != nil || u.Role != role {
				return c.Status(403).JSON(fiber.Map{"error": "Account unavailable or token role changed"})
			}
			account = u
		}
		if account != nil && (account.DeletedAt != nil || account.Status == "deleted") {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Account is deleted"})
		}
		if account != nil && account.Status == "pending_deletion" && !pendingDeletionPathAllowed(c.Method(), c.Path(), uid) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Account deletion is pending", "code": "ACCOUNT_PENDING_DELETION"})
		}
		c.Locals("user_id", uid)
		c.Locals("user_role", role)
		if issued, ok := claims["iat"].(float64); ok {
			c.Locals("token_issued_at", time.Unix(int64(issued), 0))
		}

		return c.Next()
	}
}

func pendingDeletionPathAllowed(method, path, userID string) bool {
	switch {
	case method == "GET" && (path == "/api/v1/auth/me" || path == "/api/v1/driver/profile/me" || path == "/api/v1/passenger/profile/me" || path == "/api/v1/account/deletion-request" || path == "/api/v1/account/export-requests"):
		return true
	case method == "DELETE" && path == "/api/v1/account/deletion-request":
		return true
	case method == "POST" && path == "/api/v1/account/export-request":
		return true
	case method == "GET" && strings.HasPrefix(path, "/api/v1/account/export-requests/") && strings.HasSuffix(path, "/download"):
		return true
	default:
		return false
	}
}

// Protected is a backwards-compatible alias for AppProtected.
func Protected(secret string) fiber.Handler {
	return AppProtected(&config.Config{JWTSecret: secret})
}

// StaffGetter retrieves a staff user by ID (implemented by *store.Store).
type StaffGetter interface {
	GetStaffUserByID(id string) (*models.StaffUser, bool)
}

// StaffProtected validates the Bearer JWT for the "staff" realm (admin and support).
// Rejects app tokens or inactive staff.
// Reloads staff member and role from database (cached for at most 30s).
// Enforces must_change_password gate (allowing only /change-password and /me).
func StaffProtected(cfg *config.Config, s StaffGetter) fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Missing or invalid token"})
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid token format"})
		}

		claims, err := utils.ParseStaffToken(parts[1], cfg.StaffJWTSecret)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized staff token"})
		}

		staffID, _ := claims["sub"].(string)
		if staffID == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized staff token"})
		}

		// Reload staff member (cache at most 30s)
		var staff *models.StaffUser
		staffCacheMu.RLock()
		cached, exists := staffCache[staffID]
		if exists && time.Since(cached.fetchedAt) < 30*time.Second {
			staff = cached.staff
		}
		staffCacheMu.RUnlock()

		if staff == nil {
			var ok bool
			staff, ok = s.GetStaffUserByID(staffID)
			if !ok {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Staff member not found"})
			}
			staffCacheMu.Lock()
			staffCache[staffID] = staffCacheEntry{staff: staff, fetchedAt: time.Now()}
			staffCacheMu.Unlock()
		}

		if !staff.IsActive {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Staff account is deactivated"})
		}

		// Enforce must_change_password gate:
		// Until must_change_password is cleared, only /change-password and /me may be called.
		if staff.MustChangePassword {
			path := c.Path()
			if path != "/api/v1/staff/auth/change-password" && path != "/api/v1/staff/auth/me" {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"error":                "Password change required before accessing other endpoints",
					"must_change_password": true,
				})
			}
		}

		c.Locals("staff_user", staff)
		c.Locals("staff_id", staff.ID)
		c.Locals("staff_role", staff.Role)
		c.Locals("staff_email", staff.Email)

		return c.Next()
	}
}

// RequireAppRoles allows only specified app roles through.
// A staff role can never satisfy this check.
func RequireAppRoles(allowed ...models.Role) fiber.Handler {
	set := make(map[models.Role]struct{}, len(allowed))
	for _, r := range allowed {
		set[r] = struct{}{}
	}
	return func(c *fiber.Ctx) error {
		roleVal := c.Locals("user_role")
		if roleVal == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Forbidden: insufficient role",
			})
		}
		role, ok := roleVal.(models.Role)
		if !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Forbidden: insufficient role",
			})
		}
		if _, allowed := set[role]; !allowed {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Forbidden: insufficient role",
			})
		}
		return c.Next()
	}
}

// RequireRoles is backwards-compatible wrapper for RequireAppRoles.
func RequireRoles(allowed ...models.Role) fiber.Handler {
	return RequireAppRoles(allowed...)
}

// RequireStaffRoles allows only specified staff roles through.
// An app role can never satisfy this check.
// If an admin-only endpoint is targeted and 2FA is required by config,
// requires TOTP to be enabled on the admin account.
func RequireStaffRoles(cfg *config.Config, allowed ...models.StaffRole) fiber.Handler {
	set := make(map[models.StaffRole]struct{}, len(allowed))
	isAdminOnly := len(allowed) == 1 && allowed[0] == models.StaffRoleAdmin
	for _, r := range allowed {
		set[r] = struct{}{}
	}
	return func(c *fiber.Ctx) error {
		roleVal := c.Locals("staff_role")
		if roleVal == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Forbidden: insufficient staff role",
			})
		}
		role, ok := roleVal.(models.StaffRole)
		if !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Forbidden: insufficient staff role",
			})
		}
		if _, allowed := set[role]; !allowed {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Forbidden: insufficient staff role",
			})
		}

		// Enforce 2FA for admin if required
		if isAdminOnly && cfg != nil && cfg.StaffRequire2FAForAdmin {
			staff, _ := c.Locals("staff_user").(*models.StaffUser)
			if staff != nil && !staff.TOTPEnabled {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"error":       "2FA must be enabled for admin accounts before accessing this endpoint",
					"require_2fa": true,
				})
			}
		}

		return c.Next()
	}
}

// CurrentUser returns (userID, role) from Locals for app users.
func CurrentUser(c *fiber.Ctx) (string, models.Role) {
	uid, _ := c.Locals("user_id").(string)
	role, _ := c.Locals("user_role").(models.Role)
	return uid, role
}

// CurrentStaff returns the authenticated StaffUser from Locals.
func CurrentStaff(c *fiber.Ctx) *models.StaffUser {
	staff, _ := c.Locals("staff_user").(*models.StaffUser)
	return staff
}
