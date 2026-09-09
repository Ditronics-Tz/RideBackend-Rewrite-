package middleware

import (
	"strings"

	"ride-backend/models"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

// Protected validates the Bearer JWT and stores user_id + user_role in Locals.
func Protected(secret string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Missing or invalid token"})
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid token format"})
		}

		tokenString := parts[1]

		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fiber.ErrUnauthorized
			}
			return []byte(secret), nil
		})

		if err != nil || !token.Valid {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized token"})
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok || !token.Valid {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized token"})
		}

		uid, _ := claims["user_id"].(string)
		if uid == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized token"})
		}
		c.Locals("user_id", uid)
		if r, ok := claims["role"].(string); ok {
			c.Locals("user_role", r)
		} else {
			c.Locals("user_role", "")
		}

		return c.Next()
	}
}

// RequireRoles allows only the given roles through.
// Must be placed AFTER Protected.
func RequireRoles(allowed ...models.Role) fiber.Handler {
	set := make(map[string]struct{}, len(allowed))
	for _, r := range allowed {
		set[string(r)] = struct{}{}
	}
	return func(c *fiber.Ctx) error {
		role, _ := c.Locals("user_role").(string)
		if _, ok := set[role]; !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Forbidden: insufficient role",
			})
		}
		return c.Next()
	}
}

// CurrentUser returns (userID, role) from Locals.
func CurrentUser(c *fiber.Ctx) (string, models.Role) {
	uid, _ := c.Locals("user_id").(string)
	role, _ := c.Locals("user_role").(string)
	return uid, models.Role(role)
}
