package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"ride-backend/config"
	"ride-backend/models"
	"ride-backend/store"
	"ride-backend/utils"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

type AuthController struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewAuthController(cfg *config.Config, s *store.Store) *AuthController {
	return &AuthController{Cfg: cfg, Store: s}
}

// Register creates an app user (driver or passenger only).
func (ac *AuthController) Register(c *fiber.Ctx) error {
	var input models.RegisterInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Email = strings.TrimSpace(strings.ToLower(input.Email))

	if input.Name == "" || input.Email == "" || input.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name, email and password are required"})
	}
	if len(input.Password) < 6 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "password must be at least 6 characters"})
	}

	role := models.RolePassenger
	if strings.TrimSpace(input.Role) != "" {
		r, err := models.ParseRole(input.Role)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": err.Error(),
			})
		}
		role = r
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to hash password"})
	}

	user, err := ac.Store.CreateUser(input.Name, input.Email, string(hashed), role)
	if err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	}

	token, err := utils.GenerateAppToken(user.ID, user.Role, ac.Cfg.JWTSecret, utils.DefaultAppTTL)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not generate token"})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"message": "User registered successfully",
		"token":   token,
		"user":    models.ToUserResponse(user),
	})
}

// Login authenticates app realm users and returns an app token.
func (ac *AuthController) Login(c *fiber.Ctx) error {
	var input models.LoginInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	input.Email = strings.TrimSpace(strings.ToLower(input.Email))

	user, ok := ac.Store.GetUserByEmail(input.Email)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid email or password"})
	}
	if !user.IsActive {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Account is disabled"})
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)); err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid email or password"})
	}

	token, err := utils.GenerateAppToken(user.ID, user.Role, ac.Cfg.JWTSecret, utils.DefaultAppTTL)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not generate token"})
	}

	return c.JSON(fiber.Map{
		"message": "Login successful",
		"token":   token,
		"user":    models.ToUserResponse(user),
	})
}

// Me returns the logged-in app user plus their profile and rating summary.
func (ac *AuthController) Me(c *fiber.Ctx) error {
	uid, _ := c.Locals("user_id").(string)
	user, ok := ac.Store.GetUserByID(uid)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
	}
	resp := fiber.Map{"user": models.ToUserResponse(user)}
	if dp, ok := ac.Store.GetDriverProfile(uid); ok {
		resp["driver_profile"] = dp
	}
	if pp, ok := ac.Store.GetPassengerProfile(uid); ok {
		resp["passenger_profile"] = pp
	}
	_, avg, total := ac.Store.ListRatingsForUser(uid)
	resp["average_rating"] = avg
	resp["total_ratings"] = total
	return c.JSON(resp)
}

// GoogleLogin redirects to Google consent screen.
func (ac *AuthController) GoogleLogin(c *fiber.Ctx) error {
	oauthConfig := utils.GetGoogleOAuthConfig(ac.Cfg)
	url := oauthConfig.AuthCodeURL("random-state-string")
	return c.Redirect(url, fiber.StatusTemporaryRedirect)
}

// GoogleCallback upserts a Google user (defaults to passenger role).
func (ac *AuthController) GoogleCallback(c *fiber.Ctx) error {
	code := c.Query("code")
	if code == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Authorization code missing"})
	}

	oauthConfig := utils.GetGoogleOAuthConfig(ac.Cfg)
	token, err := oauthConfig.Exchange(context.Background(), code)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Failed to exchange authorization code"})
	}

	resp, err := http.Get("https://www.googleapis.com/oauth2/v2/userinfo?access_token=" + token.AccessToken)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch user info from Google"})
	}
	defer resp.Body.Close()

	var googleUser models.GoogleUser
	if err := json.NewDecoder(resp.Body).Decode(&googleUser); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to parse Google user response"})
	}

	user, ok := ac.Store.GetUserByEmail(strings.ToLower(googleUser.Email))
	if !ok {
		user, _ = ac.Store.CreateUser(googleUser.Name, strings.ToLower(googleUser.Email), "", models.RolePassenger)
		if pp, ok := ac.Store.GetPassengerProfile(user.ID); ok && googleUser.Picture != "" {
			ac.Store.UpdatePassengerProfile(user.ID, func(p *models.PassengerProfile) {
				p.ProfilePictureURL = googleUser.Picture
				_ = pp
			})
		}
	} else if !user.IsActive {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Account is disabled"})
	}

	jwtToken, err := utils.GenerateAppToken(user.ID, user.Role, ac.Cfg.JWTSecret, utils.DefaultAppTTL)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate session token"})
	}

	return c.JSON(fiber.Map{
		"message": "Google authentication successful",
		"token":   jwtToken,
		"user":    models.ToUserResponse(user),
	})
}
