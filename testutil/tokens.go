package testutil

import (
	"context"
	"time"

	"github.com/google/uuid"
	"ride-backend/models"
	"ride-backend/store"
	"ride-backend/utils"
)

func AppToken(s *store.Store, userID string, role models.Role, secret string, ttl time.Duration) (string, error) {
	issued, err := s.IssueAppSession(context.Background(), userID, store.SessionDevice{})
	if err != nil {
		return "", err
	}
	return utils.GenerateAppSessionToken(userID, role, issued.Session.ID, secret, ttl)
}

func StaffToken(s *store.Store, userID string, role models.StaffRole, secret string, ttl time.Duration) (string, error) {
	u, ok := s.GetStaffUserByID(userID)
	if !ok {
		return "", store.ErrStaffSessionInvalid
	}
	// Fixture tokens represent staff who already finished mandatory enrollment.
	// Sign-in tests cover the unenrolled challenge path directly.
	if !u.TOTPEnabled && (u.Role == models.StaffRoleAdmin || u.Role == models.StaffRoleSupport) {
		updated, err := s.UpdateStaffUser(userID, func(user *models.StaffUser) { user.TOTPEnabled = true })
		if err != nil {
			return "", err
		}
		u = updated
	}
	now := time.Now()
	if s.Clock != nil {
		now = s.Clock()
	}
	row, err := s.CreateStaffSession(userID, utils.HashSHA256(uuid.NewString()), uuid.NewString(), "fixture", "", now.Add(time.Hour))
	if err != nil {
		return "", err
	}
	return utils.GenerateStaffSessionToken(userID, role, row.ID, u.AuthVersion, secret, ttl)
}
