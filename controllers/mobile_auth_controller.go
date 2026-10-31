package controllers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
)

func (ac *AuthController) GoogleMobileChallenge(c *fiber.Ctx) error {
	if ac.Cfg.GoogleWebClientID == "" {
		return c.Status(503).JSON(fiber.Map{"error": "Google sign-in is not configured", "code": "GOOGLE_NOT_CONFIGURED"})
	}
	if err := ac.throttle(c, "app_google_challenge_ip", c.IP(), 20); err != nil {
		return authError(c, err)
	}
	if len(c.Body()) > 0 {
		var input struct {
			Role string `json:"role"`
		}
		if len(c.Body()) > 4096 || c.BodyParser(&input) != nil {
			return c.Status(400).JSON(fiber.Map{"error": "invalid Google challenge request", "code": "INVALID_REQUEST"})
		}
		if input.Role != "" {
			if _, err := models.ParseRole(input.Role); err != nil {
				return operationError(c, store.FieldError("role", "role must be driver or passenger"))
			}
		}
	}
	id, nonce, expiry, err := ac.Store.CreateGoogleChallenge(c.UserContext())
	if err != nil {
		return authError(c, err)
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"challengeId": id, "nonce": nonce, "expiresAt": expiry})
}

func (ac *AuthController) GoogleMobileExchange(c *fiber.Ctx) error {
	if ac.Cfg.GoogleWebClientID == "" {
		return c.Status(503).JSON(fiber.Map{"error": "Google sign-in is not configured", "code": "GOOGLE_NOT_CONFIGURED"})
	}
	if err := ac.throttle(c, "app_google_exchange_ip", c.IP(), 20); err != nil {
		return authError(c, err)
	}
	var input struct {
		IDToken     string `json:"idToken"`
		ChallengeID string `json:"challengeId"`
		Role        string `json:"role"`
	}
	if len(c.Body()) > 20000 || c.BodyParser(&input) != nil || input.ChallengeID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "invalid Google sign-in request", "code": "INVALID_REQUEST"})
	}
	role := models.RolePassenger
	if input.Role != "" {
		parsed, err := models.ParseRole(input.Role)
		if err != nil {
			return operationError(c, store.FieldError("role", "role must be driver or passenger"))
		}
		role = parsed
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 10*time.Second)
	defer cancel()
	identity, nonce, err := ac.GoogleID.Verify(ctx, input.IDToken, ac.Cfg.GoogleWebClientID)
	if err != nil {
		return c.Status(401).JSON(fiber.Map{"error": "Google identity could not be verified", "code": "GOOGLE_TOKEN_INVALID"})
	}
	email := strings.ToLower(strings.TrimSpace(identity.Email))
	if !validAuthEmail(email) || !identity.VerifiedEmail {
		return c.Status(401).JSON(fiber.Map{"error": "Google identity could not be verified", "code": "GOOGLE_TOKEN_INVALID"})
	}
	if err := ac.Store.ConsumeGoogleChallenge(ctx, input.ChallengeID, nonce); err != nil {
		if errors.Is(err, store.ErrInvalidCredentials) {
			return c.Status(401).JSON(fiber.Map{"error": "Google challenge expired or already used", "code": "GOOGLE_CHALLENGE_INVALID"})
		}
		return authError(c, err)
	}
	user, err := ac.Store.ResolveGoogleIdentity(ctx, identity.Subject, identity.Name, email, role)
	if err != nil {
		var op *store.OperationError
		if errors.As(err, &op) && op.Status == 409 {
			return c.Status(409).JSON(fiber.Map{"error": "Sign in to the existing account before linking Google", "code": "ACCOUNT_LINK_REQUIRED"})
		}
		return authError(c, err)
	}
	return ac.issue(c, user, 200, "Google authentication successful")
}

func (ac *AuthController) MobileMe(c *fiber.Ctx) error {
	id, _ := middleware.CurrentUser(c)
	user, ok := ac.Store.GetUserByID(id)
	if !ok {
		return authError(c, store.ErrAuthUnavailable)
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"user": mobileUser(user), "onboardingRequired": onboardingRequired(user)})
}
