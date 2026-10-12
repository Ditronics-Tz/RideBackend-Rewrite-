package controllers

import (
	"github.com/gofiber/fiber/v2"
	"ride-backend/middleware"
)

func (ac *AuthController) RequestPhoneOTP(c *fiber.Ctx) error {
	if ac.Store.SMS == nil {
		return fiber.NewError(503, "SMS provider is not configured")
	}
	var input struct {
		Phone string `json:"phone"`
	}
	if err := c.BodyParser(&input); err != nil {
		return fiber.NewError(400, "phone is required")
	}
	id, _ := middleware.CurrentUser(c)
	if err := ac.Store.RequestPhoneOTP(c.UserContext(), id, input.Phone, ac.Cfg.JWTSecret); err != nil {
		return verificationError(c, err)
	}
	return c.JSON(fiber.Map{"message": "Verification code sent", "expires_in": 300})
}
func (ac *AuthController) VerifyPhoneOTP(c *fiber.Ctx) error {
	var input struct {
		Code string `json:"code"`
	}
	if err := c.BodyParser(&input); err != nil {
		return fiber.NewError(400, "code is required")
	}
	id, _ := middleware.CurrentUser(c)
	if err := ac.Store.VerifyPhoneOTP(id, input.Code, ac.Cfg.JWTSecret); err != nil {
		return verificationError(c, err)
	}
	return ac.Me(c)
}
