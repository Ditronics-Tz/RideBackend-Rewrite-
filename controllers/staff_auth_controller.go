package controllers

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"ride-backend/config"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
	"ride-backend/utils"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

// Dummy hash for constant-time comparison when email is unknown.
var dummyHash []byte

func init() {
	var err error
	dummyHash, err = bcrypt.GenerateFromPassword([]byte("dummy_password_for_timing"), 12)
	if err != nil {
		panic(err)
	}
}

// In-memory rate limiter for staff login.
type rateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{attempts: make(map[string][]time.Time)}
}

func (rl *rateLimiter) allow(key string, limit int, window time.Duration) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-window)

	var valid []time.Time
	for _, t := range rl.attempts[key] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= limit {
		rl.attempts[key] = valid
		return false
	}

	valid = append(valid, now)
	rl.attempts[key] = valid
	return true
}

var (
	loginIPLimiter    = newRateLimiter()
	loginEmailLimiter = newRateLimiter()
)

// ResetLoginRateLimiters clears rate limit buckets for testing.
func ResetLoginRateLimiters() {
	loginIPLimiter.mu.Lock()
	loginIPLimiter.attempts = make(map[string][]time.Time)
	loginIPLimiter.mu.Unlock()

	loginEmailLimiter.mu.Lock()
	loginEmailLimiter.attempts = make(map[string][]time.Time)
	loginEmailLimiter.mu.Unlock()
}

var commonPasswords = map[string]bool{
	"password1234":  true,
	"123456789012":  true,
	"admin1234567":  true,
	"qwerty123456":  true,
	"letmein12345":  true,
	"welcome12345":  true,
	"administrator": true,
	"changeme1234":  true,
}

type StaffAuthController struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewStaffAuthController(cfg *config.Config, s *store.Store) *StaffAuthController {
	return &StaffAuthController{Cfg: cfg, Store: s}
}

func (sc *StaffAuthController) now() time.Time {
	if sc.Store != nil && sc.Store.Clock != nil {
		return sc.Store.Clock()
	}
	return time.Now()
}

// Login authenticates a staff member with strict security.
// Rate limited by IP and email. Generic error for all failures.
// Dummy hash used for constant-time response on missing email.
func (sc *StaffAuthController) Login(c *fiber.Ctx) error {
	ip := c.IP()
	if !loginIPLimiter.allow("ip:"+ip, 10, 1*time.Minute) {
		c.Set("Retry-After", "60")
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "Too many requests"})
	}

	var input models.StaffLoginInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid credentials"})
	}

	email := strings.ToLower(strings.TrimSpace(input.Email))
	if email == "" || input.Password == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte("invalid"))
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid credentials"})
	}

	if !loginEmailLimiter.allow("email:"+email, 10, 1*time.Minute) {
		c.Set("Retry-After", "60")
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "Too many requests"})
	}

	staff, ok := sc.Store.GetStaffUserByEmail(email)
	if !ok {
		// Constant-time execution: compare against dummy hash
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(input.Password))
		_ = sc.Store.CreateAuditLog("anonymous", "login_failure", "staff_user", "unknown", ip, fiber.Map{"reason": "unknown_email"})
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid credentials"})
	}

	// Check if account is locked
	if staff.LockedUntil != nil && staff.LockedUntil.After(time.Now()) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(input.Password))
		_ = sc.Store.CreateAuditLog(staff.ID, "login_failure", "staff_user", staff.ID, ip, fiber.Map{"reason": "account_locked"})
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid credentials"})
	}

	// Check password
	if err := bcrypt.CompareHashAndPassword([]byte(staff.PasswordHash), []byte(input.Password)); err != nil {
		attempts, lockedUntil, _ := sc.Store.IncrementFailedLogin(staff.ID)
		middleware.InvalidateStaffCache(staff.ID)
		_ = sc.Store.CreateAuditLog(staff.ID, "login_failure", "staff_user", staff.ID, ip, fiber.Map{"attempts": attempts})
		if lockedUntil != nil {
			_ = sc.Store.CreateAuditLog(staff.ID, "account_lockout", "staff_user", staff.ID, ip, fiber.Map{"locked_until": lockedUntil})
		}
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid credentials"})
	}

	if !staff.IsActive {
		_ = sc.Store.CreateAuditLog(staff.ID, "login_failure", "staff_user", staff.ID, ip, fiber.Map{"reason": "deactivated"})
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid credentials"})
	}

	// Admin and support never receive a session from the password check alone.
	return sc.issueMFAChallenge(c, staff)
}

// Refresh rotates the refresh token and detects token reuse.
func (sc *StaffAuthController) Refresh(c *fiber.Ctx) error {
	var input models.StaffRefreshInput
	if err := c.BodyParser(&input); err != nil || strings.TrimSpace(input.RefreshToken) == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid refresh token"})
	}

	tokenHash := utils.HashSHA256(input.RefreshToken)
	session, ok := sc.Store.GetStaffSessionByHash(tokenHash)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid refresh token"})
	}

	ip := c.IP()

	// Reuse detection: revoked or already replaced session presented
	if session.RevokedAt != nil || session.ReplacedBy != nil {
		_ = sc.Store.RevokeStaffSessionFamily(session.FamilyID)
		_ = sc.Store.CreateAuditLog(session.StaffID, "session_family_revocation_reuse_detected", "staff_session", session.ID, ip, fiber.Map{
			"family_id": session.FamilyID,
		})
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid refresh token: session compromised"})
	}

	// Expiration check
	if session.ExpiresAt.Before(time.Now()) {
		_ = sc.Store.RevokeStaffSession(session.ID)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Refresh token expired"})
	}

	staff, ok := sc.Store.GetStaffUserByID(session.StaffID)
	if !ok || !staff.IsActive {
		_ = sc.Store.RevokeStaffSession(session.ID)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Staff account deactivated"})
	}
	if (staff.Role == models.StaffRoleAdmin || staff.Role == models.StaffRoleSupport) && !staff.TOTPEnabled {
		_ = sc.Store.RevokeStaffSession(session.ID)
		_ = sc.Store.CreateAuditLog(staff.ID, "mfa_refresh_blocked", "staff_user", staff.ID, ip, nil)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid refresh token"})
	}

	// Rotate refresh token
	newRefreshToken := generateRandomToken()
	newTokenHash := utils.HashSHA256(newRefreshToken)
	newExpiresAt := time.Now().Add(sc.Cfg.StaffRefreshTTL)

	rotated, err := sc.Store.RotateStaffSession(
		session,
		newTokenHash,
		newExpiresAt,
		c.Get("User-Agent"),
		ip,
	)
	if err != nil {
		_ = sc.Store.RevokeStaffSessionFamily(session.FamilyID)
		return c.Status(401).JSON(fiber.Map{"error": "Invalid refresh token"})
	}

	newAccessToken, err := utils.GenerateStaffSessionToken(staff.ID, staff.Role, rotated.ID, staff.AuthVersion, sc.Cfg.StaffJWTSecret, sc.Cfg.StaffAccessTTL)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate access token"})
	}
	c.Locals("audit_staff_user_id", staff.ID)

	return c.JSON(fiber.Map{
		"access_token":  newAccessToken,
		"refresh_token": newRefreshToken,
		"user":          models.ToStaffUserResponse(staff),
	})
}

// Logout revokes the current session or all active sessions.
func (sc *StaffAuthController) Logout(c *fiber.Ctx) error {
	staff := middleware.CurrentStaff(c)
	var input models.StaffRefreshInput
	_ = c.BodyParser(&input)

	if input.RefreshToken != "" {
		hash := utils.HashSHA256(input.RefreshToken)
		if sess, ok := sc.Store.GetStaffSessionByHash(hash); ok && staff != nil && sess.StaffID == staff.ID {
			if err := sc.Store.RevokeStaffSession(sess.ID); err != nil {
				return fiber.NewError(500, "Unable to revoke session")
			}
		} else {
			return fiber.NewError(400, "Invalid refresh token")
		}
	} else if staff != nil {
		if err := sc.Store.RevokeAllStaffSessions(staff.ID); err != nil {
			return fiber.NewError(500, "Unable to revoke sessions")
		}
	}

	if staff != nil {
		_ = sc.Store.CreateAuditLog(staff.ID, "session_revocation", "staff_user", staff.ID, c.IP(), nil)
	}
	return c.JSON(fiber.Map{"message": "Logged out successfully"})
}

// LogoutAll revokes every active session of this staff member.
func (sc *StaffAuthController) LogoutAll(c *fiber.Ctx) error {
	staff := middleware.CurrentStaff(c)
	if staff == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}

	if err := sc.Store.RevokeAllStaffSessions(staff.ID); err != nil {
		return fiber.NewError(500, "Unable to revoke sessions")
	}
	_ = sc.Store.CreateAuditLog(staff.ID, "all_sessions_revocation", "staff_user", staff.ID, c.IP(), nil)
	return c.JSON(fiber.Map{"message": "All sessions revoked"})
}

// Me returns the authenticated staff profile.
func (sc *StaffAuthController) Me(c *fiber.Ctx) error {
	staff := middleware.CurrentStaff(c)
	if staff == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	return c.JSON(fiber.Map{"user": models.ToStaffUserResponse(staff)})
}

// ChangePassword changes the staff member's password and revokes existing sessions.
func (sc *StaffAuthController) ChangePassword(c *fiber.Ctx) error {
	staff := middleware.CurrentStaff(c)
	if staff == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}

	var input models.StaffChangePasswordInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if err := bcrypt.CompareHashAndPassword([]byte(staff.PasswordHash), []byte(input.CurrentPassword)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Incorrect current password"})
	}

	if !passwordValid(input.NewPassword, 12) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "New password must be at least 12 characters"})
	}

	if commonPasswords[strings.ToLower(input.NewPassword)] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "New password is too common, choose a stronger password"})
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), 12)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to hash password"})
	}

	if err := sc.Store.ChangeStaffPassword(c.UserContext(), staff, string(hashed), c.IP()); err != nil {
		return fiber.NewError(409, "Credentials changed or password update failed")
	}

	return c.JSON(fiber.Map{"message": "Password changed successfully"})
}

// Setup2FA initiates TOTP setup (returns secret + qr_code_url).
func (sc *StaffAuthController) Setup2FA(c *fiber.Ctx) error {
	if err := sc.stepUpRate(c); err != nil {
		return operationError(c, err)
	}
	staff := middleware.CurrentStaff(c)
	if staff == nil {
		return c.Status(401).JSON(fiber.Map{"error": "Unauthorized"})
	}
	var proof models.Staff2FADisableInput
	if c.BodyParser(&proof) != nil || bcrypt.CompareHashAndPassword([]byte(staff.PasswordHash), []byte(proof.Password)) != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid authenticator proof"})
	}
	if staff.TOTPEnabled && !sc.staffTOTPProof(c, staff, proof.Code) {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid authenticator proof"})
	}
	secret, err := utils.GenerateTOTPSecret()
	if err != nil {
		return fiber.NewError(500, "Unable to create authenticator")
	}
	encrypted, err := utils.EncryptAESGCM(secret, sc.Cfg.StaffTOTPKey)
	if err != nil {
		return fiber.NewError(500, "Unable to create authenticator")
	}
	expires, err := sc.Store.SetPendingStaffTOTP(c.UserContext(), staff.ID, staff.AuthVersion, encrypted)
	if err != nil {
		return fiber.NewError(409, "Credentials changed; sign in again")
	}
	if err := sc.Store.CreateAuditLog(staff.ID, "2fa_setup_pending", "staff_user", staff.ID, c.IP(), nil); err != nil {
		return fiber.NewError(500, "Unable to record authenticator setup")
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"secret": secret, "qr_code_url": utils.TOTPAuthURL(staff.Email, secret, "Rida"), "pending_expires_at": expires})
}
func (sc *StaffAuthController) staffTOTPProof(c *fiber.Ctx, staff *models.StaffUser, code string) bool {
	if !staff.TOTPEnabled || staff.TOTPSecretEnc == nil {
		return false
	}
	secret, err := utils.DecryptAESGCM(*staff.TOTPSecretEnc, sc.Cfg.StaffTOTPKey)
	if err != nil {
		return false
	}
	step, ok := utils.MatchTOTPStep(secret, code, sc.now())
	if !ok {
		return false
	}
	if err := sc.Store.RecordAcceptedTOTPStep(c.UserContext(), staff.ID, step); err != nil {
		return false
	}
	return true
}
func (sc *StaffAuthController) totpResponse(c *fiber.Ctx, updated *models.StaffUser, recovery []string) error {
	sid, _ := c.Locals("staff_session_id").(string)
	access, err := utils.GenerateStaffSessionToken(updated.ID, updated.Role, sid, updated.AuthVersion, sc.Cfg.StaffJWTSecret, sc.Cfg.StaffAccessTTL)
	if err != nil {
		return fiber.NewError(500, "Unable to generate access token")
	}
	c.Set("Cache-Control", "no-store")
	body := fiber.Map{"message": "Authenticator updated", "totp_enabled": updated.TOTPEnabled, "access_token": access, "user": models.ToStaffUserResponse(updated)}
	if len(recovery) > 0 {
		body["recovery_codes"] = recovery
	}
	return c.JSON(body)
}
func (sc *StaffAuthController) Verify2FA(c *fiber.Ctx) error {
	if err := sc.stepUpRate(c); err != nil {
		return operationError(c, err)
	}
	staff := middleware.CurrentStaff(c)
	if staff == nil {
		return fiber.NewError(401, "Unauthorized")
	}
	var input models.Staff2FAVerifyInput
	if c.BodyParser(&input) != nil || staff.PendingTOTPSecretEnc == nil || staff.PendingTOTPExpiresAt == nil || !staff.PendingTOTPExpiresAt.After(sc.now()) {
		return fiber.NewError(400, "Run authenticator setup first")
	}
	secret, err := utils.DecryptAESGCM(*staff.PendingTOTPSecretEnc, sc.Cfg.StaffTOTPKey)
	step, ok := utils.MatchTOTPStep(secret, input.Code, sc.now())
	if err != nil || !ok {
		return fiber.NewError(400, "Invalid new authenticator code")
	}
	sid, _ := c.Locals("staff_session_id").(string)
	updated, err := sc.Store.ChangeStaffTOTP(c.UserContext(), staff, sid, c.IP(), true, step)
	if err != nil {
		return fiber.NewError(409, "Authenticator setup expired or credentials changed")
	}
	display, hashes, genErr := utils.GenerateRecoveryCodes(8)
	if genErr != nil || sc.Store.ReplaceStaffRecoveryCodes(c.UserContext(), updated.ID, hashes) != nil {
		return fiber.NewError(500, "Unable to create recovery codes")
	}
	return sc.totpResponse(c, updated, display)
}
func (sc *StaffAuthController) Disable2FA(c *fiber.Ctx) error {
	staff := middleware.CurrentStaff(c)
	if staff == nil {
		return fiber.NewError(401, "Unauthorized")
	}
	if staff.Role == models.StaffRoleAdmin || staff.Role == models.StaffRoleSupport {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Two-factor authentication is required for this account"})
	}
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Two-factor authentication is required for this account"})
}

func generateRandomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
