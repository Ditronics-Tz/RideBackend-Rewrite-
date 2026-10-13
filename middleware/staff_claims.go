package middleware

import (
	"context"
	"errors"

	"github.com/golang-jwt/jwt/v5"
	"ride-backend/config"
	"ride-backend/models"
)

func ValidateStaffClaims(ctx context.Context, cfg *config.Config, s StaffGetter, claims jwt.MapClaims) (*models.StaffUser, error) {
	id, _ := claims["sub"].(string)
	role, _ := claims["role"].(string)
	version, ok := claims["ver"].(float64)
	staff, found := s.GetStaffUserByID(id)
	if !found || !staff.IsActive || role != string(staff.Role) || !ok || version != float64(staff.AuthVersion) {
		return nil, errors.New("invalid staff credentials")
	}
	sid, _ := claims["sid"].(string)
	if sid == "" && cfg.AppEnv == "production" {
		return nil, errors.New("staff session required")
	}
	if sid != "" {
		checker, ok := s.(interface {
			StaffSessionActive(context.Context, string, string) error
		})
		if !ok || checker.StaffSessionActive(ctx, id, sid) != nil {
			return nil, errors.New("invalid staff session")
		}
	}
	return staff, nil
}
