package controllers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ride-backend/controllers"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
	"ride-backend/utils"

	"golang.org/x/crypto/bcrypt"
)

func TestStaffAuthAndAdminEndpoints(t *testing.T) {
	appStore, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatalf("Failed to setup test store: %v", err)
	}

	app := routes.BuildApp(cfg, appStore)

	// Seed an initial active admin
	adminPwd := "InitialAdminPassword123!"
	adminHash, _ := bcrypt.GenerateFromPassword([]byte(adminPwd), 12)
	admin, err := appStore.CreateStaffUser("Master Admin", "admin@ride.tz", string(adminHash), models.StaffRoleAdmin, nil, false)
	if err != nil {
		t.Fatalf("Failed to seed admin: %v", err)
	}

	// Enable 2FA on admin
	adminSecret, _ := utils.GenerateTOTPSecret()
	encSecret, _ := utils.EncryptAESGCM(adminSecret, cfg.StaffTOTPKey)
	_, _ = appStore.UpdateStaffUser(admin.ID, func(u *models.StaffUser) {
		u.TOTPSecretEnc = &encSecret
		u.TOTPEnabled = true
	})

	// Seed a support user
	supportPwd := "SupportPassword123!"
	supportHash, _ := bcrypt.GenerateFromPassword([]byte(supportPwd), 12)
	support, err := appStore.CreateStaffUser("Support Staff", "support@ride.tz", string(supportHash), models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatalf("Failed to seed support: %v", err)
	}

	t.Run("Staff Login: generic error on unknown email", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{
			"email":    "nonexistent@ride.tz",
			"password": "wrongpassword123",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/staff/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req)

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected 401, got %d", resp.StatusCode)
		}
	})

	t.Run("Staff Login: generic error on wrong password", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{
			"email":    "support@ride.tz",
			"password": "wrongpassword123",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/staff/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req)

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected 401, got %d", resp.StatusCode)
		}
	})

	t.Run("Staff Login: lockout after 5 failed attempts", func(t *testing.T) {
		// Create a separate staff user for lockout test
		h, _ := bcrypt.GenerateFromPassword([]byte("password123456"), 12)
		u, _ := appStore.CreateStaffUser("Lockout Target", "lockout@ride.tz", string(h), models.StaffRoleSupport, nil, false)

		for i := 1; i <= 5; i++ {
			body, _ := json.Marshal(map[string]string{
				"email":    "lockout@ride.tz",
				"password": "wrongpassword",
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/staff/auth/login", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp, _ := app.Test(req)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("Attempt %d: expected 401, got %d", i, resp.StatusCode)
			}
		}

		// Verify account is now locked
		refreshed, ok := appStore.GetStaffUserByID(u.ID)
		if !ok || refreshed.LockedUntil == nil || !refreshed.LockedUntil.After(time.Now()) {
			t.Fatalf("Expected account to be locked until future time")
		}

		// Even with correct password, login must fail when locked
		body, _ := json.Marshal(map[string]string{
			"email":    "lockout@ride.tz",
			"password": "password123456",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/staff/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected 401 for locked account, got %d", resp.StatusCode)
		}
	})

	t.Run("Staff Login: TOTP required when enabled", func(t *testing.T) {
		// Missing TOTP code fails
		body, _ := json.Marshal(map[string]string{
			"email":    "admin@ride.tz",
			"password": adminPwd,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/staff/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected 401 for missing TOTP, got %d", resp.StatusCode)
		}

		// Valid TOTP code succeeds
		code, _ := utils.GenerateTOTPCode(adminSecret, time.Now())
		body2, _ := json.Marshal(map[string]string{
			"email":     "admin@ride.tz",
			"password":  adminPwd,
			"totp_code": code,
		})
		req2 := httptest.NewRequest(http.MethodPost, "/api/v1/staff/auth/login", bytes.NewReader(body2))
		req2.Header.Set("Content-Type", "application/json")
		resp2, err := app.Test(req2)
		if err != nil || resp2.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200 for valid login + TOTP, got %d", resp2.StatusCode)
		}

		var res map[string]interface{}
		_ = json.NewDecoder(resp2.Body).Decode(&res)
		if res["access_token"] == nil || res["refresh_token"] == nil {
			t.Fatalf("Expected access and refresh tokens, got %v", res)
		}
	})

	t.Run("Staff Login: rate limiting triggers 429", func(t *testing.T) {
		controllers.ResetLoginRateLimiters()
		for i := 0; i < 10; i++ {
			body, _ := json.Marshal(map[string]string{
				"email":    "rate@ride.tz",
				"password": "wrong",
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/staff/auth/login", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			_, _ = app.Test(req)
		}

		// 11th request must receive 429
		body, _ := json.Marshal(map[string]string{
			"email":    "rate@ride.tz",
			"password": "wrong",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/staff/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req)
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("Expected 429 after 10 requests, got %d", resp.StatusCode)
		}
	})

	t.Run("Refresh token rotation and reuse detection", func(t *testing.T) {
		controllers.ResetLoginRateLimiters()
		// Login support user to obtain refresh token
		loginBody, _ := json.Marshal(map[string]string{
			"email":    "support@ride.tz",
			"password": supportPwd,
		})
		loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/staff/auth/login", bytes.NewReader(loginBody))
		loginReq.Header.Set("Content-Type", "application/json")
		loginResp, _ := app.Test(loginReq)

		var loginData map[string]interface{}
		_ = json.NewDecoder(loginResp.Body).Decode(&loginData)
		rt1, ok := loginData["refresh_token"].(string)
		if !ok || rt1 == "" {
			t.Fatalf("Failed to obtain refresh token, loginData: %v", loginData)
		}

		// 1. First refresh with rt1 works and issues rt2
		refreshBody, _ := json.Marshal(map[string]string{"refresh_token": rt1})
		refReq := httptest.NewRequest(http.MethodPost, "/api/v1/staff/auth/refresh", bytes.NewReader(refreshBody))
		refReq.Header.Set("Content-Type", "application/json")
		refResp, err := app.Test(refReq)
		if err != nil || refResp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200 on refresh, got %d", refResp.StatusCode)
		}

		var refData map[string]interface{}
		_ = json.NewDecoder(refResp.Body).Decode(&refData)
		rt2 := refData["refresh_token"].(string)
		if rt2 == rt1 {
			t.Fatalf("Refresh token was not rotated!")
		}

		// 2. Reuse detection: presenting rt1 again must fail AND revoke rt2 (entire family)
		refReqReplay := httptest.NewRequest(http.MethodPost, "/api/v1/staff/auth/refresh", bytes.NewReader(refreshBody))
		refReqReplay.Header.Set("Content-Type", "application/json")
		refRespReplay, _ := app.Test(refReqReplay)
		if refRespReplay.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected 401 on reused refresh token, got %d", refRespReplay.StatusCode)
		}

		// 3. Verifying that rt2 is now also revoked due to family compromise
		body2, _ := json.Marshal(map[string]string{"refresh_token": rt2})
		refReq2 := httptest.NewRequest(http.MethodPost, "/api/v1/staff/auth/refresh", bytes.NewReader(body2))
		refReq2.Header.Set("Content-Type", "application/json")
		refResp2, _ := app.Test(refReq2)
		if refResp2.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected 401 for compromised token family, got %d", refResp2.StatusCode)
		}
	})

	t.Run("Permission matrix: support vs admin", func(t *testing.T) {
		adminTok, _ := utils.GenerateStaffToken(admin.ID, models.StaffRoleAdmin, cfg.StaffJWTSecret, 1*time.Hour)
		supportTok, _ := utils.GenerateStaffToken(support.ID, models.StaffRoleSupport, cfg.StaffJWTSecret, 1*time.Hour)

		// Shared routes (support + admin allowed)
		sharedRoutes := []struct {
			method string
			path   string
		}{
			{http.MethodGet, "/api/v1/admin/users"},
			{http.MethodGet, "/api/v1/admin/stats"},
			{http.MethodGet, "/api/v1/admin/rides"},
			{http.MethodGet, "/api/v1/admin/complaints"},
			{http.MethodGet, "/api/v1/admin/tickets"},
		}

		for _, sr := range sharedRoutes {
			// Support must succeed (200)
			reqS := httptest.NewRequest(sr.method, sr.path, nil)
			reqS.Header.Set("Authorization", "Bearer "+supportTok)
			respS, err := app.Test(reqS)
			if err != nil || respS.StatusCode != http.StatusOK {
				var b []byte
				if respS != nil {
					b, _ = io.ReadAll(respS.Body)
				}
				t.Errorf("Support failed on shared route %s %s: expected 200, got %d, body: %s", sr.method, sr.path, respS.StatusCode, string(b))
			}

			// Admin must succeed (200)
			reqA := httptest.NewRequest(sr.method, sr.path, nil)
			reqA.Header.Set("Authorization", "Bearer "+adminTok)
			respA, err := app.Test(reqA)
			if err != nil || respA.StatusCode != http.StatusOK {
				t.Errorf("Admin failed on shared route %s %s: expected 200, got %d", sr.method, sr.path, respA.StatusCode)
			}
		}

		// Admin-only routes (support must be forbidden 403, admin allowed)
		adminOnlyRoutes := []struct {
			method string
			path   string
		}{
			{http.MethodGet, "/api/v1/admin/staff"},
			{http.MethodGet, "/api/v1/admin/audit-logs"},
		}

		for _, ar := range adminOnlyRoutes {
			// Support must be forbidden (403)
			reqS := httptest.NewRequest(ar.method, ar.path, nil)
			reqS.Header.Set("Authorization", "Bearer "+supportTok)
			respS, err := app.Test(reqS)
			if err != nil || respS.StatusCode != http.StatusForbidden {
				t.Errorf("Support should be forbidden on %s %s: expected 403, got %d", ar.method, ar.path, respS.StatusCode)
			}

			// Admin must succeed (200)
			reqA := httptest.NewRequest(ar.method, ar.path, nil)
			reqA.Header.Set("Authorization", "Bearer "+adminTok)
			respA, err := app.Test(reqA)
			if err != nil || respA.StatusCode != http.StatusOK {
				t.Errorf("Admin should succeed on %s %s: expected 200, got %d", ar.method, ar.path, respA.StatusCode)
			}
		}
	})

	t.Run("Last admin and self-action guards", func(t *testing.T) {
		adminTok, _ := utils.GenerateStaffToken(admin.ID, models.StaffRoleAdmin, cfg.StaffJWTSecret, 1*time.Hour)

		// 1. Admin cannot deactivate themselves (400)
		bodySelfDeact, _ := json.Marshal(map[string]interface{}{"is_active": false})
		reqSelfDeact := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/staff/"+admin.ID, bytes.NewReader(bodySelfDeact))
		reqSelfDeact.Header.Set("Authorization", "Bearer "+adminTok)
		reqSelfDeact.Header.Set("Content-Type", "application/json")
		respSelfDeact, _ := app.Test(reqSelfDeact)
		if respSelfDeact.StatusCode != http.StatusBadRequest {
			t.Fatalf("Expected 400 for self-deactivation, got %d", respSelfDeact.StatusCode)
		}

		// 2. Admin cannot change their own role (400)
		bodySelfRole, _ := json.Marshal(map[string]string{"role": "support"})
		reqSelfRole := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/staff/"+admin.ID+"/role", bytes.NewReader(bodySelfRole))
		reqSelfRole.Header.Set("Authorization", "Bearer "+adminTok)
		reqSelfRole.Header.Set("Content-Type", "application/json")
		respSelfRole, _ := app.Test(reqSelfRole)
		if respSelfRole.StatusCode != http.StatusBadRequest {
			t.Fatalf("Expected 400 for self-role change, got %d", respSelfRole.StatusCode)
		}

		// 3. Create a second admin
		secondH, _ := bcrypt.GenerateFromPassword([]byte("SecAdminPass123!"), 12)
		admin2, _ := appStore.CreateStaffUser("Admin Two", "admin2@ride.tz", string(secondH), models.StaffRoleAdmin, &admin.ID, false)
		// Enable 2FA on admin2 as well
		_, _ = appStore.UpdateStaffUser(admin2.ID, func(u *models.StaffUser) {
			u.TOTPEnabled = true
		})

		// Now we have 2 active admins. Admin 1 can deactivate Admin 2
		reqDeact2 := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/staff/"+admin2.ID, bytes.NewReader(bodySelfDeact))
		reqDeact2.Header.Set("Authorization", "Bearer "+adminTok)
		reqDeact2.Header.Set("Content-Type", "application/json")
		respDeact2, _ := app.Test(reqDeact2)
		if respDeact2.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200 when deactivating non-last admin, got %d", respDeact2.StatusCode)
		}

		// Now active admins count is back to 1. Trying to demote or deactivate the last admin must fail
		// (Reacting admin2 so active count is 1, and testing demoting admin)
		reqDemoteLast := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/staff/"+admin.ID+"/role", bytes.NewReader(bodySelfRole))
		reqDemoteLast.Header.Set("Authorization", "Bearer "+adminTok)
		reqDemoteLast.Header.Set("Content-Type", "application/json")
		respDemoteLast, _ := app.Test(reqDemoteLast)
		if respDemoteLast.StatusCode != http.StatusBadRequest {
			t.Fatalf("Expected 400 when attempting to demote last active admin, got %d", respDemoteLast.StatusCode)
		}
	})

	t.Run("Create staff generates one-time password and never logs it", func(t *testing.T) {
		adminTok, _ := utils.GenerateStaffToken(admin.ID, models.StaffRoleAdmin, cfg.StaffJWTSecret, 1*time.Hour)

		body, _ := json.Marshal(map[string]string{
			"name":  "New Officer",
			"email": "officer@ride.tz",
			"role":  "support",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/staff", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminTok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil || resp.StatusCode != http.StatusCreated {
			t.Fatalf("Expected 201, got %d", resp.StatusCode)
		}

		var res map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&res)
		otp, ok := res["one_time_password"].(string)
		if !ok || len(otp) < 12 {
			t.Fatalf("Expected one_time_password returned, got %v", res["one_time_password"])
		}

		// Verify audit logs did NOT log the password
		logs, _, _ := appStore.ListAuditLogs(10, 0)
		for _, l := range logs {
			if l.Action == "create_staff" {
				if bytes.Contains([]byte(l.Meta), []byte(otp)) {
					t.Fatalf("SECURITY VIOLATION: One time password found in audit logs!")
				}
			}
		}
	})
}

func TestLicenceFilePermissions(t *testing.T) {
	appStore, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatalf("Failed to setup test store: %v", err)
	}

	app := routes.BuildApp(cfg, appStore)

	// Create test file in ./uploads/licences
	_ = os.MkdirAll("./uploads/licences", 0o755)
	testFilename := "licence_driver1_test.png"
	testFilePath := filepath.Join("./uploads/licences", testFilename)
	_ = os.WriteFile(testFilePath, []byte("fake_image_content"), 0o644)
	defer os.Remove(testFilePath)

	// Seed driver 1
	d1, _ := appStore.CreateUser("Driver One", "driver1@ride.tz", "hash", models.RoleDriver)
	appStore.UpdateDriverProfile(d1.ID, func(p *models.DriverProfile) {
		p.LicenceImageURL = "/api/v1/files/licences/" + testFilename
	})

	// Seed driver 2
	d2, _ := appStore.CreateUser("Driver Two", "driver2@ride.tz", "hash", models.RoleDriver)

	// Seed staff member
	staff, _ := appStore.CreateStaffUser("Support One", "sup1@ride.tz", "hash", models.StaffRoleSupport, nil, false)

	tokD1, _ := utils.GenerateAppToken(d1.ID, models.RoleDriver, cfg.JWTSecret, 1*time.Hour)
	tokD2, _ := utils.GenerateAppToken(d2.ID, models.RoleDriver, cfg.JWTSecret, 1*time.Hour)
	tokStaff, _ := utils.GenerateStaffToken(staff.ID, models.StaffRoleSupport, cfg.StaffJWTSecret, 1*time.Hour)

	// 1. Unauthenticated -> 401
	reqAnon := httptest.NewRequest(http.MethodGet, "/api/v1/files/licences/"+testFilename, nil)
	respAnon, _ := app.Test(reqAnon)
	if respAnon.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Expected 401 for anonymous access to licence, got %d", respAnon.StatusCode)
	}

	// 2. Other driver -> 403
	reqD2 := httptest.NewRequest(http.MethodGet, "/api/v1/files/licences/"+testFilename, nil)
	reqD2.Header.Set("Authorization", "Bearer "+tokD2)
	respD2, _ := app.Test(reqD2)
	if respD2.StatusCode != http.StatusForbidden {
		t.Fatalf("Expected 403 for non-owning driver, got %d", respD2.StatusCode)
	}

	// 3. Owning driver -> 200
	reqD1 := httptest.NewRequest(http.MethodGet, "/api/v1/files/licences/"+testFilename, nil)
	reqD1.Header.Set("Authorization", "Bearer "+tokD1)
	respD1, _ := app.Test(reqD1)
	if respD1.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 for owning driver, got %d", respD1.StatusCode)
	}

	// 4. Staff member -> 200
	reqStaff := httptest.NewRequest(http.MethodGet, "/api/v1/files/licences/"+testFilename, nil)
	reqStaff.Header.Set("Authorization", "Bearer "+tokStaff)
	respStaff, _ := app.Test(reqStaff)
	if respStaff.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 for staff access to licence, got %d", respStaff.StatusCode)
	}
}
