package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
	"ride-backend/utils"
)

func TestStaffCannotSelfRegister(t *testing.T) {
	appStore, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, appStore)
	defer app.Shutdown()

	for _, path := range []string{"/api/v1/staff/auth/register", "/api/v1/staff/auth/signup", "/api/v1/admin/register"} {
		t.Run(path, func(t *testing.T) {
			for _, route := range app.GetRoutes() {
				if route.Method == http.MethodPost && route.Path == path {
					t.Fatal("Public staff registration route must not be registered")
				}
			}
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"name":"Staff","email":"registration-policy@example.test","password":"ExamplePassword123!","role":"admin"}`))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			// The admin group may reject anonymous requests before its 404 handler.
			if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("Public staff registration must not exist: got %d", resp.StatusCode)
			}
		})
	}

	for _, role := range []string{"admin", "support", " ADMIN ", " SUPPORT "} {
		t.Run("app registration rejects "+role, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{
				"name": "Staff", "email": "registration-policy@example.test", "password": "ExamplePassword123!", "role": role,
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("Staff role must be rejected by app registration: got %d", resp.StatusCode)
			}
			if _, exists := appStore.GetUserByEmail("registration-policy@example.test"); exists {
				t.Fatal("Rejected registration created an app account")
			}
			if _, exists := appStore.GetStaffUserByEmail("registration-policy@example.test"); exists {
				t.Fatal("Rejected registration created a staff account")
			}
		})
	}

	support, err := appStore.CreateStaffUser("Policy Support", "registration-policy-support@example.test", "unused-password-hash", models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	token, err := utils.GenerateStaffToken(support.ID, support.Role, cfg.StaffJWTSecret, cfg.StaffAccessTTL)
	if err != nil {
		t.Fatal(err)
	}
	for _, caller := range []struct {
		name   string
		token  string
		status int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"support", token, http.StatusForbidden},
	} {
		t.Run(caller.name+" cannot provision staff", func(t *testing.T) {
			before := len(appStore.ListStaffUsers())
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/staff", strings.NewReader(`{"name":"Staff","email":"registration-policy@example.test","role":"admin"}`))
			req.Header.Set("Content-Type", "application/json")
			if caller.token != "" {
				req.Header.Set("Authorization", "Bearer "+caller.token)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != caller.status {
				t.Fatalf("Expected %d, got %d", caller.status, resp.StatusCode)
			}
			if len(appStore.ListStaffUsers()) != before {
				t.Fatal("Unauthorized staff provisioning created an account")
			}
		})
	}
}
