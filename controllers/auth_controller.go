package controllers

import (
	"context"
	"errors"
	"log/slog"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
	"ride-backend/config"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
	"ride-backend/utils"
)

type AuthController struct {
	Cfg      *config.Config
	Store    *store.Store
	Limiter  store.AuthLimiter
	Google   utils.GoogleAuthenticator
	GoogleID utils.GoogleIDTokenVerifier
}

func NewAuthController(cfg *config.Config, s *store.Store) *AuthController {
	return &AuthController{Cfg: cfg, Store: s, Limiter: store.NewSQLAuthLimiter(s), Google: utils.GoogleOAuth{Config: cfg}}
}

func invalidCredentials(c *fiber.Ctx) error {
	return c.Status(401).JSON(fiber.Map{"error": "invalid credentials"})
}
func authError(c *fiber.Ctx, err error) error {
	if errors.Is(err, store.ErrAuthUnavailable) {
		return c.Status(503).JSON(fiber.Map{"error": "authentication temporarily unavailable", "code": "AUTH_TEMPORARILY_UNAVAILABLE"})
	}
	if errors.Is(err, store.ErrInvalidCredentials) {
		return invalidCredentials(c)
	}
	return operationError(c, err)
}
func (ac *AuthController) throttle(c *fiber.Ctx, scope, key string, limit int) error {
	if strings.TrimSpace(key) == "" || len(key) > 1024 {
		key = "invalid-account"
	}
	decision, err := ac.Limiter.Allow(c.UserContext(), scope, key, store.AuthRate{Limit: limit, Window: time.Minute})
	if err != nil {
		return store.ErrAuthUnavailable
	}
	if !decision.Allowed {
		return &store.OperationError{Status: 429, Message: "Too many requests", RetryAfter: decision.RetryAfter}
	}
	return nil
}
func validAuthEmail(email string) bool {
	if len(email) > 254 {
		return false
	}
	parsed, err := mail.ParseAddress(email)
	return err == nil && parsed.Address == email && strings.Contains(email, "@")
}
func passwordValid(password string, min int) bool {
	return utf8.RuneCountInString(password) >= min && len(password) <= 72 && !commonPasswords[strings.ToLower(password)] && !map[string]bool{"password123": true, "1234567890": true, "qwerty12345": true, "password12345": true, "abcdefghij": true}[strings.ToLower(password)]
}
func appDevice(c *fiber.Ctx) store.SessionDevice {
	return store.SessionDevice{DeviceID: c.Get("X-Device-ID"), Platform: c.Get("X-Device-Platform", c.Get("X-Platform")), IP: c.IP()}
}

func appAccessTTL(cfg *config.Config) time.Duration {
	if cfg.AppAccessTTL > 0 {
		return cfg.AppAccessTTL
	}
	return utils.DefaultAppTTL
}

func mobileUser(user *models.User) fiber.Map {
	var phone any
	var name any
	if strings.TrimSpace(user.Name) != "" {
		name = user.Name
	}
	if user.Phone != "" {
		phone = user.Phone
	}
	return fiber.Map{"id": user.ID, "name": user.Name, "email": user.Email,
		"full_name": name, "phone": phone, "phone_number": phone,
		"profile_completed": name != nil && phone != nil,
		"role":              user.Role, "status": user.Status,
		"is_active": user.IsActive, "phone_verified_at": user.PhoneVerifiedAt,
		"suspended_until": user.SuspendedUntil}
}

func onboardingRequired(user *models.User) []string {
	if user.Role == models.RolePassenger && (user.Phone == "" || user.PhoneVerifiedAt == nil) {
		return []string{"phone_verification"}
	}
	return []string{}
}

func (ac *AuthController) respondSession(c *fiber.Ctx, user *models.User, issued *store.IssuedAppSession, status int, message string) error {
	ttl := appAccessTTL(ac.Cfg)
	access, err := utils.GenerateAppSessionToken(user.ID, user.Role, issued.Session.ID, ac.Cfg.JWTSecret, ttl)
	if err != nil {
		return authError(c, err)
	}
	c.Locals("audit_app_user_id", user.ID)
	c.Set("Cache-Control", "no-store")
	return c.Status(status).JSON(fiber.Map{"message": message, "token": access,
		"access_token": access, "refresh_token": issued.RefreshToken,
		"expires_in": int(ttl.Seconds()), "refresh_expires_at": issued.Session.ExpiresAt,
		"accessToken": access, "refreshToken": issued.RefreshToken,
		"expiresIn": int(ttl.Seconds()), "sessionId": issued.Session.ID,
		"onboardingRequired": onboardingRequired(user),
		"restricted":         user.Status == "pending_deletion", "user": mobileUser(user)})
}
func (ac *AuthController) issue(c *fiber.Ctx, user *models.User, status int, message string) error {
	issued, err := ac.Store.IssueAppSession(c.UserContext(), user.ID, appDevice(c))
	if err != nil {
		return authError(c, err)
	}
	if err := ac.Store.ObserveDevice(user.ID, c.Get("X-Device-ID"), c.Get("X-Device-Platform", c.Get("X-Platform")), c.IP()); err != nil {
		_ = ac.Store.RevokeAppSession(c.UserContext(), user.ID, issued.Session.ID)
		return authError(c, err)
	}
	return ac.respondSession(c, user, issued, status, message)
}

func (ac *AuthController) Register(c *fiber.Ctx) error {
	if err := ac.throttle(c, "app_register_ip", c.IP(), 5); err != nil {
		return authError(c, err)
	}
	var input models.RegisterInput
	if len(c.Body()) > 4096 || c.BodyParser(&input) != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if err := ac.throttle(c, "app_register_account", input.Email, 3); err != nil {
		return authError(c, err)
	}
	if !validAuthEmail(input.Email) {
		return operationError(c, store.FieldError("email", "a valid email address is required"))
	}
	if !passwordValid(input.Password, 10) {
		return operationError(c, store.FieldError("password", "password must contain at least 10 characters, at most 72 bytes and not be common"))
	}
	role := models.RolePassenger
	if input.Role != "" {
		parsed, err := models.ParseRole(input.Role)
		if err != nil {
			return operationError(c, store.FieldError("role", "role must be driver or passenger"))
		}
		role = parsed
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
	if err != nil {
		return authError(c, store.ErrAuthUnavailable)
	}
	user, err := ac.Store.CreateUser("", input.Email, string(hash), role)
	if err != nil {
		var sqlState interface{ SQLState() string }
		if err.Error() == "email already registered" || errors.As(err, &sqlState) && sqlState.SQLState() == "23505" {
			return c.Status(409).JSON(fiber.Map{"error": "Unable to register this account"})
		}
		slog.Error("app_registration_failed", "request_id", middleware.RequestID(c))
		return authError(c, store.ErrAuthUnavailable)
	}
	return ac.issue(c, user, 201, "User registered successfully")
}

func (ac *AuthController) Login(c *fiber.Ctx) error {
	if err := ac.throttle(c, "app_login_ip", c.IP(), 20); err != nil {
		return authError(c, err)
	}
	var input models.LoginInput
	if len(c.Body()) > 4096 || c.BodyParser(&input) != nil {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte("invalid"))
		return invalidCredentials(c)
	}
	identifier := strings.ToLower(strings.TrimSpace(input.Email))
	if !validAuthEmail(identifier) || input.Password == "" || len(input.Password) > 72 {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(input.Password))
		return invalidCredentials(c)
	}
	if err := ac.throttle(c, "app_login_account", identifier, 10); err != nil {
		return authError(c, err)
	}
	user, lookupErr := ac.Store.FindUserForLogin(c.UserContext(), identifier)
	if lookupErr != nil {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(input.Password))
		if errors.Is(lookupErr, store.ErrAuthUnavailable) {
			return authError(c, lookupErr)
		}
		return invalidCredentials(c)
	}
	hash := []byte(user.PasswordHash)
	if user.PasswordHash == "" {
		hash = dummyHash
	}
	passwordErr := bcrypt.CompareHashAndPassword(hash, []byte(input.Password))
	locked, lockErr := ac.Store.IsAppLoginLocked(c.UserContext(), user.ID)
	if lockErr != nil {
		return authError(c, lockErr)
	}
	if locked {
		return invalidCredentials(c)
	}
	if passwordErr != nil || user.PasswordHash == "" {
		locked, err := ac.Store.RecordAppLoginFailure(c.UserContext(), user.ID)
		if err != nil {
			return authError(c, err)
		}
		if err := ac.Store.RecordAppSecurityEvent(c.UserContext(), user.ID, "login_failed", c.IP()); err != nil {
			return authError(c, err)
		}
		if locked {
			if err := ac.Store.RecordAppSecurityEvent(c.UserContext(), user.ID, "login_locked", c.IP()); err != nil {
				return authError(c, err)
			}
		}
		return invalidCredentials(c)
	}
	current, err := ac.Store.AccountForRequest(user.ID)
	if err != nil || current.DeletedAt != nil || current.Status == "deleted" {
		return invalidCredentials(c)
	}
	if err := ac.Store.ResetAppLoginFailures(c.UserContext(), user.ID); err != nil {
		return authError(c, err)
	}
	if err := ac.Store.RecordAppSecurityEvent(c.UserContext(), user.ID, "login_success", c.IP()); err != nil {
		return authError(c, err)
	}
	return ac.issue(c, current, 200, "Login successful")
}

func (ac *AuthController) Refresh(c *fiber.Ctx) error {
	if err := ac.throttle(c, "app_refresh_ip", c.IP(), 60); err != nil {
		return authError(c, err)
	}
	var input struct {
		RefreshToken       string `json:"refresh_token"`
		MobileRefreshToken string `json:"refreshToken"`
	}
	if len(c.Body()) > 4096 || c.BodyParser(&input) != nil {
		return c.Status(401).JSON(fiber.Map{"error": "invalid refresh credential", "code": "INVALID_REFRESH_TOKEN"})
	}
	if input.RefreshToken == "" {
		input.RefreshToken = input.MobileRefreshToken
	}
	issued, err := ac.Store.RotateAppSession(c.UserContext(), input.RefreshToken, appDevice(c))
	if err != nil {
		if errors.Is(err, store.ErrInvalidCredentials) {
			return c.Status(401).JSON(fiber.Map{"error": "invalid refresh credential", "code": "INVALID_REFRESH_TOKEN"})
		}
		return authError(c, err)
	}
	user, err := ac.Store.AppUserForSession(c.UserContext(), issued.Session.UserID)
	if err != nil {
		if errors.Is(err, store.ErrInvalidCredentials) {
			return c.Status(401).JSON(fiber.Map{"error": "invalid refresh credential", "code": "INVALID_REFRESH_TOKEN"})
		}
		return authError(c, err)
	}
	return ac.respondSession(c, user, issued, 200, "Session refreshed")
}
func (ac *AuthController) Logout(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	sid, _ := c.Locals("app_session_id").(string)
	if err := ac.Store.RevokeAppSession(c.UserContext(), uid, sid); err != nil {
		return authError(c, err)
	}
	return c.JSON(fiber.Map{"message": "Logged out"})
}
func (ac *AuthController) LogoutAll(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	if err := ac.Store.RevokeAllAppSessions(c.UserContext(), uid); err != nil {
		return authError(c, err)
	}
	return c.JSON(fiber.Map{"message": "All sessions revoked"})
}
func (ac *AuthController) Sessions(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	p, err := operationPage(c)
	if err != nil {
		return authError(c, err)
	}
	rows, total, err := ac.Store.ListAppSessions(c.UserContext(), uid, p)
	if err != nil {
		return authError(c, err)
	}
	return c.JSON(fiber.Map{"sessions": rows, "total": total, "limit": p.Limit, "offset": p.Offset})
}
func (ac *AuthController) DeleteSession(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	if err := ac.Store.RevokeAppSession(c.UserContext(), uid, c.Params("id")); err != nil {
		return authError(c, err)
	}
	return c.SendStatus(204)
}
func (ac *AuthController) Me(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	user, ok := ac.Store.GetUserByID(uid)
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "User not found"})
	}
	response := fiber.Map{"user": models.ToUserResponse(user)}
	if profile, ok := ac.Store.GetDriverProfile(uid); ok {
		profile.ProfilePictureURL = profilePictureURL(ac.Cfg, profile.UserID, profile.ProfilePictureURL, profile.ProfilePictureKey)
		response["driver_profile"] = profile
	}
	if profile, ok := ac.Store.GetPassengerProfile(uid); ok {
		profile.ProfilePictureURL = profilePictureURL(ac.Cfg, profile.UserID, profile.ProfilePictureURL, profile.ProfilePictureKey)
		profile.ProfileCompleted = strings.TrimSpace(user.Name) != "" && user.Phone != ""
		response["passenger_profile"] = profile
	}
	avg, total, err := ac.Store.RatingSummary(uid)
	if err != nil {
		return operationError(c, err)
	}
	response["average_rating"] = avg
	response["total_ratings"] = total
	return c.JSON(response)
}

func (ac *AuthController) GoogleLogin(c *fiber.Ctx) error {
	if err := ac.throttle(c, "app_google_ip", c.IP(), 20); err != nil {
		return authError(c, err)
	}
	if !ac.Cfg.GoogleRedirectAllowed() || ac.Cfg.GoogleClientID == "" || ac.Cfg.GoogleClientSecret == "" {
		return c.Status(503).JSON(fiber.Map{"error": "Google authentication is not configured"})
	}
	state, browser, err := ac.Store.CreateOAuthState(c.UserContext(), ac.Cfg.GoogleRedirectURL)
	if err != nil {
		return authError(c, store.ErrAuthUnavailable)
	}
	c.Cookie(&fiber.Cookie{Name: "rida_oauth_browser", Value: browser, HTTPOnly: true, Secure: ac.Cfg.AppEnv == "production", SameSite: "Lax", Path: "/api/v1/auth/google", MaxAge: 600, Expires: time.Now().Add(10 * time.Minute)})
	c.Set("Cache-Control", "no-store")
	return c.Redirect(utils.GetGoogleOAuthConfig(ac.Cfg).AuthCodeURL(state), 307)
}
func (ac *AuthController) GoogleCallback(c *fiber.Ctx) error {
	if !ac.Cfg.GoogleRedirectAllowed() {
		return c.Status(503).JSON(fiber.Map{"error": "Google authentication is not configured"})
	}
	if err := ac.throttle(c, "app_google_callback_ip", c.IP(), 20); err != nil {
		return authError(c, err)
	}
	if err := ac.Store.ConsumeOAuthState(c.UserContext(), c.Query("state"), c.Cookies("rida_oauth_browser"), ac.Cfg.GoogleRedirectURL); err != nil {
		return operationError(c, err)
	}
	c.Cookie(&fiber.Cookie{Name: "rida_oauth_browser", Value: "", HTTPOnly: true, Secure: ac.Cfg.AppEnv == "production", SameSite: "Lax", Path: "/api/v1/auth/google", MaxAge: -1, Expires: time.Unix(1, 0)})
	code := c.Query("code")
	if code == "" || len(code) > 2048 {
		return c.Status(400).JSON(fiber.Map{"error": "Authorization code missing or invalid"})
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 10*time.Second)
	defer cancel()
	identity, err := ac.Google.Authenticate(ctx, code)
	if err != nil {
		return c.Status(401).JSON(fiber.Map{"error": "Google authentication failed"})
	}
	email := strings.ToLower(strings.TrimSpace(identity.Email))
	if !identity.VerifiedEmail || identity.Subject == "" || !validAuthEmail(email) {
		return c.Status(401).JSON(fiber.Map{"error": "Google authentication failed"})
	}
	user, err := ac.Store.ResolveGoogleIdentity(c.UserContext(), identity.Subject, identity.Name, email)
	if err != nil {
		return operationError(c, err)
	}
	return ac.issue(c, user, 200, "Google authentication successful")
}
