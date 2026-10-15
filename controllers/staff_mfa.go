package controllers

import (
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
	"ride-backend/utils"
)

const mfaChallengeTTL = 5 * time.Minute

func (sc *StaffAuthController) issueMFAChallenge(c *fiber.Ctx, staff *models.StaffUser) error {
	if staff.TOTPEnabled {
		if staff.TOTPSecretEnc == nil || strings.TrimSpace(*staff.TOTPSecretEnc) == "" {
			_ = sc.Store.CreateAuditLog(staff.ID, "login_failure", "staff_user", staff.ID, c.IP(), fiber.Map{"reason": "totp_secret_unavailable"})
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid credentials"})
		}
		if _, err := utils.DecryptAESGCM(*staff.TOTPSecretEnc, sc.Cfg.StaffTOTPKey); err != nil {
			_ = sc.Store.CreateAuditLog(staff.ID, "login_failure", "staff_user", staff.ID, c.IP(), fiber.Map{"reason": "totp_secret_unavailable"})
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid credentials"})
		}
	}
	purpose := store.MFAPurposeVerify
	if !staff.TOTPEnabled {
		purpose = store.MFAPurposeEnroll
	}
	row, err := sc.Store.CreateStaffMFAChallenge(c.UserContext(), staff.ID, purpose, sc.now().Add(mfaChallengeTTL))
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "authentication temporarily unavailable"})
	}
	token, err := utils.GenerateStaffMFAChallengeToken(staff.ID, purpose, row.ID, staff.AuthVersion, sc.Cfg.StaffJWTSecret, mfaChallengeTTL)
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "authentication temporarily unavailable"})
	}
	_ = sc.Store.CreateAuditLog(staff.ID, "mfa_challenge_issued", "staff_user", staff.ID, c.IP(), fiber.Map{"purpose": purpose})
	c.Set("Cache-Control", "no-store")
	c.Set("Pragma", "no-cache")
	return c.JSON(fiber.Map{
		"mfa_required":         true,
		"enrollment_required":  purpose == store.MFAPurposeEnroll,
		"challenge_token":      token,
		"challenge_expires_in": int(mfaChallengeTTL.Seconds()),
	})
}

func (sc *StaffAuthController) allowMFA(c *fiber.Ctx, scope, key string, limit int) bool {
	if len(c.Body()) > 4096 {
		_ = c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
		return false
	}
	decision, err := store.NewSQLAuthLimiter(sc.Store).Allow(c.UserContext(), scope, key, store.AuthRate{Limit: limit, Window: 5 * time.Minute})
	if err != nil {
		_ = c.Status(503).JSON(fiber.Map{"error": "authentication temporarily unavailable"})
		return false
	}
	if !decision.Allowed {
		c.Set("Retry-After", "60")
		_ = c.Status(429).JSON(fiber.Map{"error": "Too many requests"})
		return false
	}
	return true
}

type mfaChallenge struct {
	staff   *models.StaffUser
	id      string
	purpose string
}

func (sc *StaffAuthController) parseMFAChallenge(c *fiber.Ctx, token, purpose string) (*mfaChallenge, error) {
	claims, err := utils.ParseStaffMFAChallengeToken(strings.TrimSpace(token), sc.Cfg.StaffJWTSecret)
	if err != nil {
		return nil, store.ErrMFAExpired
	}
	staffID, _ := claims["sub"].(string)
	pur, _ := claims["pur"].(string)
	jti, _ := claims["jti"].(string)
	version, ok := claims["ver"].(float64)
	if staffID == "" || jti == "" || pur != purpose || !ok {
		return nil, store.ErrMFAInvalid
	}
	staff, found := sc.Store.GetStaffUserByID(staffID)
	if !found || !staff.IsActive || version != float64(staff.AuthVersion) {
		return nil, store.ErrMFAInvalid
	}
	if staff.LockedUntil != nil && staff.LockedUntil.After(sc.now()) {
		return nil, store.ErrMFAInvalid
	}
	return &mfaChallenge{staff: staff, id: jti, purpose: purpose}, nil
}

func (sc *StaffAuthController) writeMFAError(c *fiber.Ctx, staffID, challengeID string, err error) error {
	if staffID != "" && challengeID != "" && (errors.Is(err, store.ErrMFAInvalid) || errors.Is(err, store.ErrMFAReplay)) {
		_ = sc.Store.FailStaffMFAChallenge(c.UserContext(), challengeID, staffID)
		attempts, lockedUntil, _ := sc.Store.IncrementFailedLogin(staffID)
		_ = sc.Store.CreateAuditLog(staffID, "mfa_verify_failure", "staff_user", staffID, c.IP(), fiber.Map{"attempts": attempts})
		if lockedUntil != nil {
			_ = sc.Store.CreateAuditLog(staffID, "account_lockout", "staff_user", staffID, c.IP(), fiber.Map{"locked_until": lockedUntil})
		}
	}
	c.Set("Cache-Control", "no-store")
	switch {
	case errors.Is(err, store.ErrMFAExpired):
		return c.Status(401).JSON(fiber.Map{"error": "Verification expired", "code": "mfa_challenge_expired"})
	case errors.Is(err, store.ErrMFALocked):
		return c.Status(401).JSON(fiber.Map{"error": "Verification expired", "code": "mfa_challenge_expired"})
	default:
		return c.Status(401).JSON(fiber.Map{"error": "Invalid verification code"})
	}
}

func (sc *StaffAuthController) finishMFALogin(c *fiber.Ctx, staff *models.StaffUser, recovery []string) error {
	_ = sc.Store.ResetFailedLogin(staff.ID)
	fresh, ok := sc.Store.GetStaffUserByID(staff.ID)
	if !ok {
		return c.Status(401).JSON(fiber.Map{"error": "Invalid verification code"})
	}
	refreshToken := generateRandomToken()
	session, err := sc.Store.IssueStaffSession(c.UserContext(), fresh, utils.HashSHA256(refreshToken), uuid.NewString(), c.Get("User-Agent"), c.IP(), sc.now().Add(sc.Cfg.StaffRefreshTTL))
	if err != nil {
		return c.Status(401).JSON(fiber.Map{"error": "Invalid verification code"})
	}
	access, err := utils.GenerateStaffSessionToken(fresh.ID, fresh.Role, session.ID, fresh.AuthVersion, sc.Cfg.StaffJWTSecret, sc.Cfg.StaffAccessTTL)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to generate token"})
	}
	_ = sc.Store.CreateAuditLog(fresh.ID, "login_success", "staff_user", fresh.ID, c.IP(), nil)
	c.Set("Cache-Control", "no-store")
	body := fiber.Map{
		"access_token":         access,
		"refresh_token":        refreshToken,
		"must_change_password": fresh.MustChangePassword,
		"user":                 models.ToStaffUserResponse(fresh),
	}
	if len(recovery) > 0 {
		body["recovery_codes"] = recovery
	}
	return c.JSON(body)
}

func (sc *StaffAuthController) VerifyMFA(c *fiber.Ctx) error {
	var input models.StaffMFAChallengeInput
	if c.BodyParser(&input) != nil || strings.TrimSpace(input.ChallengeToken) == "" {
		return c.Status(401).JSON(fiber.Map{"error": "Invalid verification code"})
	}
	if !sc.allowMFA(c, "staff_mfa_ip", c.IP(), 30) {
		return nil
	}
	parsed, err := sc.parseMFAChallenge(c, input.ChallengeToken, store.MFAPurposeVerify)
	if err != nil {
		return sc.writeMFAError(c, "", "", err)
	}
	if !sc.allowMFA(c, "staff_mfa_user", parsed.staff.ID, 20) {
		return nil
	}
	if parsed.staff.TOTPSecretEnc == nil {
		return sc.writeMFAError(c, parsed.staff.ID, parsed.id, store.ErrMFAInvalid)
	}
	secret, decErr := utils.DecryptAESGCM(*parsed.staff.TOTPSecretEnc, sc.Cfg.StaffTOTPKey)
	step, ok := utils.MatchTOTPStep(secret, input.Code, sc.now())
	if decErr != nil || !ok {
		return sc.writeMFAError(c, parsed.staff.ID, parsed.id, store.ErrMFAInvalid)
	}
	if err := sc.Store.RecordAcceptedTOTPStep(c.UserContext(), parsed.staff.ID, step); err != nil {
		return sc.writeMFAError(c, parsed.staff.ID, parsed.id, err)
	}
	if err := sc.Store.ConsumeStaffMFAChallenge(c.UserContext(), parsed.id, parsed.staff.ID, store.MFAPurposeVerify); err != nil {
		return sc.writeMFAError(c, parsed.staff.ID, parsed.id, err)
	}
	return sc.finishMFALogin(c, parsed.staff, nil)
}

func (sc *StaffAuthController) VerifyRecoveryCode(c *fiber.Ctx) error {
	var input models.StaffMFARecoveryInput
	if c.BodyParser(&input) != nil || strings.TrimSpace(input.ChallengeToken) == "" {
		return c.Status(401).JSON(fiber.Map{"error": "Invalid verification code"})
	}
	if !sc.allowMFA(c, "staff_mfa_ip", c.IP(), 30) {
		return nil
	}
	parsed, err := sc.parseMFAChallenge(c, input.ChallengeToken, store.MFAPurposeVerify)
	if err != nil {
		return sc.writeMFAError(c, "", "", err)
	}
	if !sc.allowMFA(c, "staff_mfa_user", parsed.staff.ID, 20) {
		return nil
	}
	normalized := utils.NormalizeRecoveryCode(input.RecoveryCode)
	if len(normalized) < 8 {
		return sc.writeMFAError(c, parsed.staff.ID, parsed.id, store.ErrMFAInvalid)
	}
	if err := sc.Store.RedeemStaffRecoveryCode(c.UserContext(), parsed.staff.ID, utils.HashSHA256(normalized)); err != nil {
		return sc.writeMFAError(c, parsed.staff.ID, parsed.id, err)
	}
	if err := sc.Store.ConsumeStaffMFAChallenge(c.UserContext(), parsed.id, parsed.staff.ID, store.MFAPurposeVerify); err != nil {
		return sc.writeMFAError(c, parsed.staff.ID, parsed.id, err)
	}
	_ = sc.Store.CreateAuditLog(parsed.staff.ID, "mfa_recovery_used", "staff_user", parsed.staff.ID, c.IP(), nil)
	return sc.finishMFALogin(c, parsed.staff, nil)
}

func (sc *StaffAuthController) EnrollMFA(c *fiber.Ctx) error {
	var input models.StaffMFAChallengeInput
	if c.BodyParser(&input) != nil {
		return c.Status(401).JSON(fiber.Map{"error": "Verification expired", "code": "mfa_challenge_expired"})
	}
	if !sc.allowMFA(c, "staff_mfa_ip", c.IP(), 30) {
		return nil
	}
	parsed, err := sc.parseMFAChallenge(c, input.ChallengeToken, store.MFAPurposeEnroll)
	if err != nil {
		return sc.writeMFAError(c, "", "", err)
	}
	if !sc.allowMFA(c, "staff_mfa_user", parsed.staff.ID, 20) {
		return nil
	}
	if parsed.staff.TOTPEnabled {
		return sc.writeMFAError(c, parsed.staff.ID, parsed.id, store.ErrMFAInvalid)
	}
	secret, err := utils.GenerateTOTPSecret()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Unable to create authenticator"})
	}
	encrypted, err := utils.EncryptAESGCM(secret, sc.Cfg.StaffTOTPKey)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Unable to create authenticator"})
	}
	stored, expires, err := sc.Store.PendingOrSetStaffTOTP(c.UserContext(), parsed.staff.ID, parsed.staff.AuthVersion, encrypted)
	if err != nil {
		return c.Status(409).JSON(fiber.Map{"error": "Verification expired", "code": "mfa_challenge_expired"})
	}
	if stored != encrypted {
		secret, err = utils.DecryptAESGCM(stored, sc.Cfg.StaffTOTPKey)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Unable to create authenticator"})
		}
	}
	_ = sc.Store.CreateAuditLog(parsed.staff.ID, "2fa_setup_pending", "staff_user", parsed.staff.ID, c.IP(), nil)
	c.Set("Cache-Control", "no-store")
	c.Set("Pragma", "no-cache")
	return c.JSON(fiber.Map{
		"secret":             secret,
		"qr_code_url":        utils.TOTPAuthURL(parsed.staff.Email, secret, "Rida"),
		"pending_expires_at": expires,
		"issuer":             "Rida",
		"algorithm":          "SHA1",
		"digits":             6,
		"period":             30,
	})
}

func (sc *StaffAuthController) ConfirmMFAEnrollment(c *fiber.Ctx) error {
	var input models.StaffMFAChallengeInput
	if c.BodyParser(&input) != nil {
		return c.Status(401).JSON(fiber.Map{"error": "Invalid verification code"})
	}
	if !sc.allowMFA(c, "staff_mfa_ip", c.IP(), 30) {
		return nil
	}
	parsed, err := sc.parseMFAChallenge(c, input.ChallengeToken, store.MFAPurposeEnroll)
	if err != nil {
		return sc.writeMFAError(c, "", "", err)
	}
	if !sc.allowMFA(c, "staff_mfa_user", parsed.staff.ID, 20) {
		return nil
	}
	fresh, ok := sc.Store.GetStaffUserByID(parsed.staff.ID)
	if !ok || fresh.PendingTOTPSecretEnc == nil || fresh.PendingTOTPExpiresAt == nil || !fresh.PendingTOTPExpiresAt.After(sc.now()) {
		return sc.writeMFAError(c, parsed.staff.ID, parsed.id, store.ErrMFAExpired)
	}
	secret, decErr := utils.DecryptAESGCM(*fresh.PendingTOTPSecretEnc, sc.Cfg.StaffTOTPKey)
	step, matched := utils.MatchTOTPStep(secret, input.Code, sc.now())
	if decErr != nil || !matched {
		return sc.writeMFAError(c, parsed.staff.ID, parsed.id, store.ErrMFAInvalid)
	}
	updated, err := sc.Store.ChangeStaffTOTP(c.UserContext(), fresh, "", c.IP(), true, step)
	if err != nil {
		return sc.writeMFAError(c, parsed.staff.ID, parsed.id, store.ErrMFAExpired)
	}
	if err := sc.Store.ConsumeStaffMFAChallenge(c.UserContext(), parsed.id, parsed.staff.ID, store.MFAPurposeEnroll); err != nil {
		return sc.writeMFAError(c, parsed.staff.ID, parsed.id, err)
	}
	display, hashes, genErr := utils.GenerateRecoveryCodes(8)
	if genErr != nil || sc.Store.ReplaceStaffRecoveryCodes(c.UserContext(), updated.ID, hashes) != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Unable to create recovery codes"})
	}
	return sc.finishMFALogin(c, updated, display)
}

func (sc *StaffAuthController) MFAStatus(c *fiber.Ctx) error {
	staff := middleware.CurrentStaff(c)
	if staff == nil {
		return c.Status(401).JSON(fiber.Map{"error": "Unauthorized"})
	}
	remaining, err := sc.Store.CountUnusedRecoveryCodes(c.UserContext(), staff.ID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Unable to read authenticator status"})
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{
		"mfa_required":             true,
		"totp_enabled":             staff.TOTPEnabled,
		"enrollment_required":      !staff.TOTPEnabled,
		"recovery_codes_remaining": remaining,
		"totp_verified_at":         staff.TOTPVerifiedAt,
	})
}

func (sc *StaffAuthController) MFAActivity(c *fiber.Ctx) error {
	staff := middleware.CurrentStaff(c)
	if staff == nil {
		return c.Status(401).JSON(fiber.Map{"error": "Unauthorized"})
	}
	rows, err := sc.Store.ListStaffMFAAudit(c.UserContext(), staff.ID, 20)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Unable to read security activity"})
	}
	events := make([]fiber.Map, 0, len(rows))
	for _, row := range rows {
		events = append(events, fiber.Map{"action": row.Action, "ip": row.IP, "created_at": row.CreatedAt})
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"events": events})
}

func (sc *StaffAuthController) RegenerateRecoveryCodes(c *fiber.Ctx) error {
	if err := sc.stepUpRate(c); err != nil {
		return operationError(c, err)
	}
	staff := middleware.CurrentStaff(c)
	if staff == nil || !staff.TOTPEnabled {
		return c.Status(403).JSON(fiber.Map{"error": "Two-factor authentication is required", "require_2fa": true})
	}
	var input models.Staff2FAVerifyInput
	if c.BodyParser(&input) != nil || !sc.staffTOTPProof(c, staff, input.Code) {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid authenticator proof"})
	}
	display, hashes, err := utils.GenerateRecoveryCodes(8)
	if err != nil || sc.Store.ReplaceStaffRecoveryCodes(c.UserContext(), staff.ID, hashes) != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Unable to create recovery codes"})
	}
	_ = sc.Store.CreateAuditLog(staff.ID, "mfa_recovery_regenerated", "staff_user", staff.ID, c.IP(), nil)
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"recovery_codes": display})
}

func (sc *StaffAuthController) ReplaceAuthenticator(c *fiber.Ctx) error {
	return sc.Setup2FA(c)
}

func (sc *StaffAuthController) ConfirmReplaceAuthenticator(c *fiber.Ctx) error {
	return sc.Verify2FA(c)
}
