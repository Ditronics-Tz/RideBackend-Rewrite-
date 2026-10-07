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
	"github.com/google/uuid"
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
	"password1234": true,
	"123456789012": true,
	"admin1234567":  true,
	"qwerty123456": true,
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

// Login authenticates a staff member with strict security.
// Rate limited by IP and email. Generic error for all failures.
// Dummy hash used for constant-time response on missing email.
func (sc *StaffAuthController) Login(c *fiber.Ctx) error {
	ip := c.IP()
	if !loginIPLimiter.allow("ip:"+ip, 10, 1*time.Minute) {
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
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "Too many requests"})
	}

	staff, ok := sc.Store.GetStaffUserByEmail(email)
	if !ok {
		// Constant-time execution: compare against dummy hash
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(input.Password))
		_ = sc.Store.CreateAuditLog("anonymous", "login_failure", "staff_user", email, ip, fiber.Map{"reason": "unknown_email"})
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

	// Check 2FA if enabled
	if staff.TOTPEnabled {
		if staff.TOTPSecretEnc == nil || strings.TrimSpace(input.TOTPCode) == "" {
			attempts, lockedUntil, _ := sc.Store.IncrementFailedLogin(staff.ID)
			middleware.InvalidateStaffCache(staff.ID)
			_ = sc.Store.CreateAuditLog(staff.ID, "login_failure", "staff_user", staff.ID, ip, fiber.Map{"reason": "missing_totp", "attempts": attempts})
			if lockedUntil != nil {
				_ = sc.Store.CreateAuditLog(staff.ID, "account_lockout", "staff_user", staff.ID, ip, fiber.Map{"locked_until": lockedUntil})
			}
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid credentials"})
		}

		secret, err := utils.DecryptAESGCM(*staff.TOTPSecretEnc, sc.Cfg.StaffTOTPKey)
		if err != nil || !utils.ValidateTOTPCode(secret, input.TOTPCode) {
			attempts, lockedUntil, _ := sc.Store.IncrementFailedLogin(staff.ID)
			middleware.InvalidateStaffCache(staff.ID)
			_ = sc.Store.CreateAuditLog(staff.ID, "login_failure", "staff_user", staff.ID, ip, fiber.Map{"reason": "invalid_totp", "attempts": attempts})
			if lockedUntil != nil {
				_ = sc.Store.CreateAuditLog(staff.ID, "account_lockout", "staff_user", staff.ID, ip, fiber.Map{"locked_until": lockedUntil})
			}
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid credentials"})
		}
	}

	if !staff.IsActive {
		_ = sc.Store.CreateAuditLog(staff.ID, "login_failure", "staff_user", staff.ID, ip, fiber.Map{"reason": "deactivated"})
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid credentials"})
	}

	// Reset failed attempts upon successful authentication
	_ = sc.Store.ResetFailedLogin(staff.ID)
	middleware.InvalidateStaffCache(staff.ID)

	// Generate access token
	accessToken, err := utils.GenerateStaffToken(staff.ID, staff.Role, sc.Cfg.StaffJWTSecret, sc.Cfg.StaffAccessTTL)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate token"})
	}

	// Generate refresh token and session
	refreshToken := generateRandomToken()
	tokenHash := utils.HashSHA256(refreshToken)
	familyID := uuid.NewString()

	_, err = sc.Store.CreateStaffSession(
		staff.ID,
		tokenHash,
		familyID,
		c.Get("User-Agent"),
		ip,
		time.Now().Add(sc.Cfg.StaffRefreshTTL),
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create session"})
	}

	_ = sc.Store.CreateAuditLog(staff.ID, "login_success", "staff_user", staff.ID, ip, nil)

	return c.JSON(fiber.Map{
		"access_token":          accessToken,
		"refresh_token":         refreshToken,
		"must_change_password": staff.MustChangePassword,
		"user":                  models.ToStaffUserResponse(staff),
	})
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

	// Rotate refresh token
	newRefreshToken := generateRandomToken()
	newTokenHash := utils.HashSHA256(newRefreshToken)
	newExpiresAt := time.Now().Add(sc.Cfg.StaffRefreshTTL)

	_, err := sc.Store.RotateStaffSession(
		session,
		newTokenHash,
		newExpiresAt,
		c.Get("User-Agent"),
		ip,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to rotate session"})
	}

	newAccessToken, err := utils.GenerateStaffToken(staff.ID, staff.Role, sc.Cfg.StaffJWTSecret, sc.Cfg.StaffAccessTTL)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate access token"})
	}

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
		if sess, ok := sc.Store.GetStaffSessionByHash(hash); ok {
			_ = sc.Store.RevokeStaffSession(sess.ID)
		}
	} else if staff != nil {
		_ = sc.Store.RevokeAllStaffSessions(staff.ID)
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

	_ = sc.Store.RevokeAllStaffSessions(staff.ID)
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

	if len(input.NewPassword) < 12 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "New password must be at least 12 characters"})
	}

	if commonPasswords[strings.ToLower(input.NewPassword)] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "New password is too common, choose a stronger password"})
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), 12)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to hash password"})
	}

	_, err = sc.Store.UpdateStaffUser(staff.ID, func(u *models.StaffUser) {
		u.PasswordHash = string(hashed)
		u.MustChangePassword = false
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update password"})
	}

	middleware.InvalidateStaffCache(staff.ID)
	_ = sc.Store.RevokeAllStaffSessions(staff.ID)
	_ = sc.Store.CreateAuditLog(staff.ID, "password_change", "staff_user", staff.ID, c.IP(), nil)

	return c.JSON(fiber.Map{"message": "Password changed successfully"})
}

// Setup2FA initiates TOTP setup (returns secret + qr_code_url).
func (sc *StaffAuthController) Setup2FA(c *fiber.Ctx) error {
	staff := middleware.CurrentStaff(c)
	if staff == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}

	secret, err := utils.GenerateTOTPSecret()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate 2FA secret"})
	}

	enc, err := utils.EncryptAESGCM(secret, sc.Cfg.StaffTOTPKey)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to encrypt 2FA secret"})
	}

	_, err = sc.Store.UpdateStaffUser(staff.ID, func(u *models.StaffUser) {
		u.TOTPSecretEnc = &enc
		u.TOTPEnabled = false
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to save 2FA secret"})
	}
	middleware.InvalidateStaffCache(staff.ID)

	qrURL := utils.TOTPAuthURL(staff.Email, secret, "RideBackend")
	return c.JSON(fiber.Map{
		"secret":      secret,
		"qr_code_url": qrURL,
	})
}

// Verify2FA validates the 6-digit TOTP code and activates 2FA.
func (sc *StaffAuthController) Verify2FA(c *fiber.Ctx) error {
	staff := middleware.CurrentStaff(c)
	if staff == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}

	var input models.Staff2FAVerifyInput
	if err := c.BodyParser(&input); err != nil || strings.TrimSpace(input.Code) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Code is required"})
	}

	if staff.TOTPSecretEnc == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Run 2FA setup first"})
	}

	secret, err := utils.DecryptAESGCM(*staff.TOTPSecretEnc, sc.Cfg.StaffTOTPKey)
	if err != nil || !utils.ValidateTOTPCode(secret, input.Code) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid 2FA code"})
	}

	_, err = sc.Store.UpdateStaffUser(staff.ID, func(u *models.StaffUser) {
		u.TOTPEnabled = true
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to activate 2FA"})
	}

	middleware.InvalidateStaffCache(staff.ID)
	_ = sc.Store.CreateAuditLog(staff.ID, "2fa_enabled", "staff_user", staff.ID, c.IP(), nil)

	return c.JSON(fiber.Map{
		"message":      "2FA enabled successfully",
		"totp_enabled": true,
	})
}

// Disable2FA disables 2FA after password and TOTP code verification.
func (sc *StaffAuthController) Disable2FA(c *fiber.Ctx) error {
	staff := middleware.CurrentStaff(c)
	if staff == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}

	var input models.Staff2FADisableInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if err := bcrypt.CompareHashAndPassword([]byte(staff.PasswordHash), []byte(input.Password)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid password"})
	}

	if staff.TOTPSecretEnc != nil {
		secret, err := utils.DecryptAESGCM(*staff.TOTPSecretEnc, sc.Cfg.StaffTOTPKey)
		if err != nil || !utils.ValidateTOTPCode(secret, input.Code) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid 2FA code"})
		}
	}

	_, err := sc.Store.UpdateStaffUser(staff.ID, func(u *models.StaffUser) {
		u.TOTPEnabled = false
		u.TOTPSecretEnc = nil
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to disable 2FA"})
	}

	middleware.InvalidateStaffCache(staff.ID)
	_ = sc.Store.CreateAuditLog(staff.ID, "2fa_disabled", "staff_user", staff.ID, c.IP(), nil)

	return c.JSON(fiber.Map{"message": "2FA disabled successfully"})
}

func generateRandomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
