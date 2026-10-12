package routes_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
	"ride-backend/utils"
)

func TestDataRequestSupportReadOnlyAdminCanHold(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg.StaffRequire2FAForAdmin = false
	user, err := s.CreateUser("Requester", "requester@example.test", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	req, _, err := s.RequestDeletion(user.ID, "user", "", 14)
	if err != nil {
		t.Fatal(err)
	}
	supportID := "support-test"
	adminID := "admin-test"
	for _, staff := range []*models.StaffUser{{ID: supportID, Name: "Support", Email: "support@test.invalid", Role: models.StaffRoleSupport, IsActive: true}, {ID: adminID, Name: "Admin", Email: "admin@test.invalid", Role: models.StaffRoleAdmin, IsActive: true, TOTPEnabled: true}} {
		if err = s.DB().Create(staff).Error; err != nil {
			t.Fatal(err)
		}
	}
	app := routes.BuildApp(cfg, s)
	supportToken, _ := utils.GenerateStaffToken(supportID, models.StaffRoleSupport, cfg.StaffJWTSecret, time.Hour)
	adminToken, _ := utils.GenerateStaffToken(adminID, models.StaffRoleAdmin, cfg.StaffJWTSecret, time.Hour)
	call := func(token, method, path, body string) int {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		res, e := app.Test(r)
		if e != nil {
			t.Fatal(e)
		}
		return res.StatusCode
	}
	if code := call(supportToken, "GET", "/api/v1/admin/data-requests?type=deletion", ""); code != 200 {
		t.Fatalf("support GET returned %d", code)
	}
	if code := call(supportToken, "POST", "/api/v1/admin/data-requests/"+req.ID+"/hold", `{"reason":"legal hold"}`); code != 403 {
		t.Fatalf("support write returned %d", code)
	}
	if code := call(adminToken, "POST", "/api/v1/admin/data-requests/"+req.ID+"/hold", `{"reason":"legal hold"}`); code != 200 {
		t.Fatalf("admin hold returned %d", code)
	}
}
