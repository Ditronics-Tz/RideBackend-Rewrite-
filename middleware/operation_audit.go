package middleware

import (
	"errors"
	"log/slog"
	"strings"

	"ride-backend/models"
	"ride-backend/store"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// OperationAudit records API mutations without reading request or response
// bodies. High-volume location updates and quote calculations are telemetry,
// not business operations, and are intentionally excluded.
func OperationAudit(s *store.Store, log *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		method := c.Method()
		if method != fiber.MethodPost && method != fiber.MethodPut && method != fiber.MethodPatch && method != fiber.MethodDelete && c.Path() != "/api/v1/auth/google/callback" {
			return c.Next()
		}
		err := c.Next()
		route := c.Route().Path
		if !strings.HasPrefix(route, "/api/v1/") || operationTelemetryRoute(route) {
			return err
		}
		status := c.Response().StatusCode()
		if err != nil {
			status = fiber.StatusInternalServerError
			var fe *fiber.Error
			if errors.As(err, &fe) {
				status = fe.Code
			}
		}
		// A rate-limited client can make unlimited rejected requests. Keep those
		// in bounded HTTP logs instead of allowing them to fill the audit table.
		if status == fiber.StatusTooManyRequests {
			return err
		}
		actorType, actorID := "anonymous", ""
		if id, _ := CurrentUser(c); id != "" {
			actorType, actorID = "app", id
		} else if staff := CurrentStaff(c); staff != nil {
			actorType, actorID = "staff", staff.ID
		} else if id, ok := c.Locals("audit_app_user_id").(string); ok && id != "" && status < 400 {
			actorType, actorID = "app", id
		} else if id, ok := c.Locals("audit_staff_user_id").(string); ok && id != "" && status < 400 {
			actorType, actorID = "staff", id
		}
		targetID := c.Params("id")
		if _, parseErr := uuid.Parse(targetID); parseErr != nil {
			targetID = ""
		}
		entry := models.OperationEvent{ActorType: actorType, ActorID: actorID, Action: operationAction(method, route), Method: method, Route: route, Status: status, RequestID: RequestID(c), TargetID: targetID}
		if auditErr := s.RecordOperationEvent(c.UserContext(), entry); auditErr != nil && log != nil {
			log.Error("operation_audit_write_failed", "request_id", RequestID(c), "route", route, "method", method)
		}
		return err
	}
}

func operationTelemetryRoute(route string) bool {
	switch route {
	case "/api/v1/driver/location", "/api/v1/rides/quote", "/api/v1/auth/google/challenge":
		return true
	}
	return strings.HasSuffix(route, "/location") || strings.HasSuffix(route, "/location-ping")
}

func operationAction(method, route string) string {
	switch route {
	case "/api/v1/auth/register":
		return "user_registration"
	case "/api/v1/auth/login":
		return "user_login"
	case "/api/v1/auth/google", "/api/v1/auth/google/callback":
		return "google_login"
	case "/api/v1/auth/refresh", "/api/v1/staff/auth/refresh":
		return "session_refresh"
	case "/api/v1/auth/logout", "/api/v1/staff/auth/logout":
		return "logout"
	case "/api/v1/auth/logout-all", "/api/v1/staff/auth/logout-all":
		return "logout_all"
	case "/api/v1/staff/auth/login":
		return "staff_password_login"
	case "/api/v1/staff/auth/mfa/verify", "/api/v1/staff/auth/mfa/recovery/verify", "/api/v1/staff/auth/mfa/enroll/confirm":
		return "staff_login"
	case "/api/v1/passenger/profile/me", "/api/v1/driver/profile/me":
		return "profile_updated"
	case "/api/v1/rides":
		return "ride_requested"
	case "/api/v1/rides/:id/accept":
		return "ride_accepted"
	case "/api/v1/rides/:id/start":
		return "ride_started"
	case "/api/v1/rides/:id/complete":
		return "ride_completed"
	case "/api/v1/rides/:id/cancel":
		return "ride_cancelled"
	case "/api/v1/rides/:id/rating":
		return "ride_rated"
	}
	return "api_write"
}
