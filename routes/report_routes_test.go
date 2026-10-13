package routes_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"net/http/httptest"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
	"strings"
	"testing"
	"time"
)

func TestReportedUserCannotDiscoverReporter(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateUser("Secret reporter", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateUser("Driver", uuid.NewString()+"@test.invalid", "", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	r := models.Report{ID: uuid.NewString(), ReporterID: a.ID, ReportedID: b.ID, RideID: uuid.NewString(), Status: "open", Description: "secret evidence"}
	if err := s.DB().Create(&r).Error; err != nil {
		t.Fatal(err)
	}
	token, err := testutil.AppToken(s, b.ID, b.Role, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	for _, path := range []string{"/api/v1/reports", "/api/v1/blocks", "/api/v1/admin/reports/" + r.ID, "/api/v1/admin/users/" + b.ID + "/reports"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), a.ID) || strings.Contains(string(raw), a.Name) || strings.Contains(string(raw), r.Description) {
			t.Fatalf("reporter leaked on %s", path)
		}
	}
	admin, err := s.CreateStaffUser("Admin", uuid.NewString()+"@test.invalid", "", models.StaffRoleAdmin, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	adminToken, err := testutil.StaffToken(s, admin.ID, admin.Role, cfg.StaffJWTSecret, cfg.StaffAccessTTL)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/api/v1/admin/reports?q=secret", nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	response, err := routes.BuildApp(cfg, s).Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var page map[string]any
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || page["total"] != float64(1) {
		t.Fatalf("report text filter did not return the matching report: status=%d page=%v", response.StatusCode, page)
	}
}
