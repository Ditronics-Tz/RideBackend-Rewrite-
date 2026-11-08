package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"ride-backend/models"
)

func TestAnnouncementAudienceAndRetryAreAtomic(t *testing.T) {
	for audience, want := range map[string]int{"both": 2, "passengers": 1, "drivers": 1} {
		t.Run(audience, func(t *testing.T) {
			s, _, _ := authTestStore(t)
			if err := s.db.AutoMigrate(&models.AppNotification{}); err != nil {
				t.Fatal(err)
			}
			for _, user := range []models.User{
				{ID: uuid.NewString(), Email: uuid.NewString() + "@example.test", Role: models.RoleDriver, IsActive: true, Status: "active"},
				{ID: uuid.NewString(), Email: uuid.NewString() + "@example.test", Role: models.RolePassenger, IsActive: true, Status: "suspended"},
			} {
				if err := s.db.Create(&user).Error; err != nil {
					t.Fatal(err)
				}
			}
			admin := &models.StaffUser{ID: uuid.NewString(), Role: models.StaffRoleAdmin, IsActive: true}
			input := AnnouncementInput{Audience: audience, Title: "Service update", Body: "Fixture announcement"}
			first, err := s.Announce(context.Background(), admin, "announcement-retry-fixture", input)
			if err != nil || first.NotifiedCount != want {
				t.Fatalf("unexpected audience result: %v, %v", first, err)
			}
			retry, err := s.Announce(context.Background(), admin, "announcement-retry-fixture", input)
			if err != nil || *first != *retry {
				t.Fatalf("retry changed result: %v", err)
			}
			input.Body = "Different content"
			if _, err := s.Announce(context.Background(), admin, "announcement-retry-fixture", input); err == nil {
				t.Fatal("reusing a key for different content must fail")
			}
			var count int64
			if err := s.db.Model(&models.AppNotification{}).Count(&count).Error; err != nil || count != int64(want) {
				t.Fatalf("retry duplicated inbox notifications: %d, %v", count, err)
			}
			admin.Role = models.StaffRoleSupport
			if _, err := s.Announce(context.Background(), admin, "support-fixture-key", input); err == nil {
				t.Fatal("support must not send announcements")
			}
		})
	}
}
