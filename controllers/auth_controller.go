package controllers

import (
	"context"
	"encoding/json"
	"net/http"

	"ride-backend/config"
	"ride-backend/models"
	"ride-backend/utils"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

type AuthController struct {
	Cfg *config.Config
}

func NewAuthController(cfg *config.Config) *AuthController {
	return &AuthController{Cfg: cfg}
}

// Standard Register
func (ac *AuthController) Register(c *fiber.Ctx) error {
	var input models.RegisterInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to hash password"})
	}

	// TODO: Hapa uta-save user kwenye database (Postgres/MySQL n.k.)
	_ = hashedPassword

	token, err := utils.GenerateToken("mock-user-id-123", ac.Cfg.JWTSecret)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not generate token"})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"message": "User registered successfully",
		"token":   token,
	})
}

// Standard Login
func (ac *AuthController) Login(c *fiber.Ctx) error {
	var input models.LoginInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	// TODO: Fetch user kutoka DB na u-compare password kwa bcrypt.CompareHashAndPassword

	token, err := utils.GenerateToken("mock-user-id-123", ac.Cfg.JWTSecret)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not generate token"})
	}

	return c.JSON(fiber.Map{
		"message": "Login successful",
		"token":   token,
	})
}

// Google Login Redirect
func (ac *AuthController) GoogleLogin(c *fiber.Ctx) error {
	oauthConfig := utils.GetGoogleOAuthConfig(ac.Cfg)
	url := oauthConfig.AuthCodeURL("random-state-string") // Katika production, tumia random state iliyo-store kwa session
	return c.Redirect(url, fiber.StatusTemporaryRedirect)
}

// Google Callback
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

	// Fetch profile kutoka Google API
	resp, err := http.Get("https://www.googleapis.com/oauth2/v2/userinfo?access_token=" + token.AccessToken)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch user info from Google"})
	}
	defer resp.Body.Close()

	var googleUser models.GoogleUser
	if err := json.NewDecoder(resp.Body).Decode(&googleUser); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to parse Google user response"})
	}

	// TODO: Hapa uta-check kama user yupo kwenye DB, kama hayupo unamsajili (Upsert user)

	jwtToken, err := utils.GenerateToken(googleUser.ID, ac.Cfg.JWTSecret)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate session token"})
	}

	return c.JSON(fiber.Map{
		"message": "Google authentication successful",
		"token":   jwtToken,
		"user":    googleUser,
	})
}
