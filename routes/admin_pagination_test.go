package routes_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
	"ride-backend/utils"
)

func TestAdminPaginationSearchAndFilters(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	staff := models.StaffUser{ID: uuid.NewString(), Name: "Review Admin", Email: "admin-pagination@example.test", PasswordHash: "fixture", Role: models.StaffRoleAdmin, IsActive: true, TOTPEnabled: true, CreatedAt: time.Now()}
	if err := s.DB().Create(&staff).Error; err != nil {
		t.Fatal(err)
	}
	user, err := s.CreateUser("Search Driver", "driver-pagination@example.test", "fixture", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("Other Passenger", "passenger-pagination@example.test", "fixture", models.RolePassenger); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAuditLog(staff.ID, "pagination_fixture", "user", user.ID, "", nil); err != nil {
		t.Fatal(err)
	}
	token, err := utils.GenerateStaffToken(staff.ID, staff.Role, cfg.StaffJWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	for _, tc := range []struct {
		path, key string
		total     int
		limit     int
	}{{"/admin/users", "users", 2, 50}, {"/admin/users?q=Search&role=driver&status=active&limit=1&offset=0", "users", 1, 1}, {"/admin/staff?q=Review&role=admin&status=active", "staff", 1, 50}, {"/admin/audit-logs?action=pagination_fixture&actor=" + staff.ID, "logs", 1, 50}, {"/admin/verifications?status=pending_documents", "verifications", 1, 50}} {
		req := httptest.NewRequest("GET", "/api/v1"+tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 || body["total"] != float64(tc.total) || body["limit"] != float64(tc.limit) || body["offset"] != float64(0) || body[tc.key] == nil {
			t.Fatalf("%s: %+v (%d)", tc.path, body, resp.StatusCode)
		}
	}
	for _, query := range []string{"limit=201", "limit=0", "offset=-1", "status=unknown"} {
		req := httptest.NewRequest("GET", "/api/v1/admin/users?"+query, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != 400 || body["request_id"] == nil || body["fields"] == nil {
			t.Fatalf("invalid query %s: %d %+v", query, resp.StatusCode, body)
		}
	}
	if err := s.DB().Model(&models.User{}).Where("id = ?", user.ID).Updates(map[string]any{"status": "banned", "is_active": false}).Error; err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"banned", "inactive", "deactivated"} {
		req := httptest.NewRequest("GET", "/api/v1/admin/users?status="+status, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != 200 || body["total"] != float64(1) {
			t.Fatalf("status %s: %d %+v", status, resp.StatusCode, body)
		}
	}
}
