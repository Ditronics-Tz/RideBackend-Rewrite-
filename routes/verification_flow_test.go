package routes_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/testutil"
)

func TestDriverVerificationLifecycle(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	app := routes.BuildApp(cfg, s)
	defer app.Shutdown()
	u, err := s.CreateUser("Review Driver", uuid.NewString()+"@example.test", "unused", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser("Other Driver", uuid.NewString()+"@example.test", "unused", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateStaffUser("Reviewer A", uuid.NewString()+"@example.test", "unused", models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateStaffUser("Reviewer B", uuid.NewString()+"@example.test", "unused", models.StaffRoleSupport, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	aToken, _ := testutil.StaffToken(s, a.ID, a.Role, cfg.StaffJWTSecret, cfg.StaffAccessTTL)
	bToken, _ := testutil.StaffToken(s, b.ID, b.Role, cfg.StaffJWTSecret, cfg.StaffAccessTTL)
	uToken, _ := testutil.AppToken(s, u.ID, u.Role, cfg.JWTSecret, time.Hour)
	otherToken, _ := testutil.AppToken(s, other.ID, other.Role, cfg.JWTSecret, time.Hour)
	request := func(method, path, token string, payload interface{}, want int) {
		t.Helper()
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			var data interface{}
			_ = json.NewDecoder(resp.Body).Decode(&data)
			t.Fatalf("%s %s: expected %d, got %d: %v", method, path, want, resp.StatusCode, data)
		}
	}
	adminBase := "/admin/drivers/" + u.ID
	request("GET", "/admin/verifications?status=not_valid", aToken, nil, 400)
	request("PATCH", "/driver/availability", uToken, map[string]bool{"online": true}, 400)
	request("POST", "/driver/verification/me/submit", uToken, nil, 400)
	_, err = s.UpdateDriverIdentifiers(u.ID, models.DriverProfileInput{LicenceNumber: " lic-" + u.ID, PlateNumber: " T-" + u.ID, VehicleCardNumber: "CARD"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateDriverIdentifiers(other.ID, models.DriverProfileInput{LicenceNumber: "LIC" + u.ID}); err == nil {
		t.Fatal("Duplicate licence accepted")
	}
	flagged, err := s.VerificationDetail(u.ID, true)
	if err != nil || len(flagged.DuplicateFlags) != 1 {
		t.Fatalf("Duplicate was not flagged: %v", err)
	}
	ownerDetail, _ := s.VerificationDetail(u.ID, false)
	if len(ownerDetail.DuplicateFlags) != 0 {
		t.Fatal("Owner response exposes another account")
	}
	if err := os.MkdirAll("uploads/documents", 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("uploads", "documents", uuid.NewString()+".png")
	if err := os.WriteFile(path, []byte("private-document-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	expires := time.Now().Add(48 * time.Hour)
	docs := map[string]*models.DriverDocument{}
	for _, kind := range models.RequiredDocumentTypes {
		var exp *time.Time
		if models.RequiresExpiry(kind) {
			exp = &expires
		}
		doc, err := s.AddDriverDocument(u.ID, kind, path, exp)
		if err != nil {
			t.Fatal(err)
		}
		docs[kind] = doc
	}
	licence := docs["driving_licence"]
	request("GET", "/files/documents/"+licence.ID, "", nil, 401)
	request("GET", "/files/documents/"+licence.ID, otherToken, nil, 403)
	request("GET", "/files/documents/"+licence.ID, uToken, nil, 200)
	request("GET", "/files/documents/"+licence.ID, aToken, nil, 200)
	request("POST", "/driver/verification/me/submit", uToken, nil, 200)
	request("POST", adminBase+"/documents/"+licence.ID+"/reviews", aToken, map[string]string{"decision": "approved"}, 409)
	request("GET", "/admin/verifications?status=submitted", aToken, nil, 200)
	request("POST", "/admin/verifications/"+u.ID+"/claim", aToken, nil, 200)
	request("POST", adminBase+"/verification/claim", bToken, nil, 409)
	request("POST", adminBase+"/verification/release", bToken, nil, 409)
	if _, err := s.AddDriverDocument(u.ID, "driving_licence", path, &expires); err == nil {
		t.Fatal("Upload changed a claimed review")
	}
	request("POST", adminBase+"/documents/"+licence.ID+"/reviews", bToken, map[string]string{"decision": "approved"}, 409)
	request("POST", adminBase+"/documents/"+licence.ID+"/reviews", aToken, map[string]string{"decision": "rejected"}, 400)
	for kind, doc := range docs {
		decision, code, note := "approved", "", ""
		if kind == "driving_licence" {
			decision = "rejected"
			code = "blurry_image"
			note = "Please upload a clear image"
		}
		request("POST", adminBase+"/documents/"+doc.ID+"/reviews", aToken, map[string]string{"decision": decision, "reason_code": code, "note": note}, 200)
	}
	detail, _ := s.VerificationDetail(u.ID, true)
	if detail.Profile.VerificationStatus != models.Rejected {
		t.Fatalf("Expected rejected, got %s", detail.Profile.VerificationStatus)
	}
	replacement, err := s.AddDriverDocument(u.ID, "driving_licence", path, &expires)
	if err != nil {
		t.Fatal(err)
	}
	detail, _ = s.VerificationDetail(u.ID, true)
	if len(detail.Documents) != len(models.RequiredDocumentTypes) {
		t.Fatal("Replacement changed unrelated documents")
	}
	request("GET", "/files/documents/"+licence.ID, uToken, nil, 200)
	request("POST", "/driver/verification/me/submit", uToken, nil, 200)
	request("POST", adminBase+"/verification/claim", bToken, nil, 200)
	request("POST", adminBase+"/documents/"+licence.ID+"/reviews", bToken, map[string]string{"decision": "approved"}, 403)
	request("PATCH", "/admin/documents/"+replacement.ID+"/review", bToken, map[string]string{"decision": "approved"}, 200)
	request("PATCH", "/driver/availability", uToken, map[string]bool{"online": true}, 400)
	request("POST", adminBase+"/approve", bToken, nil, 200)
	request("PATCH", "/driver/availability", uToken, map[string]bool{"online": true}, 200)
	if err := s.RequireDriverApproval(u.ID); err != nil {
		t.Fatal(err)
	}
	// Support can review new submissions but only an admin can reverse decisions.
	request("PATCH", "/admin/documents/"+replacement.ID+"/review", bToken, map[string]string{"decision": "rejected", "reason_code": "suspected_fraud", "note": "Investigate identity"}, 403)
	admin, err := s.CreateStaffUser("Admin reviewer", uuid.NewString()+"@example.test", "unused", models.StaffRoleAdmin, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg.StaffRequire2FAForAdmin = false
	adminToken, _ := testutil.StaffToken(s, admin.ID, admin.Role, cfg.StaffJWTSecret, cfg.StaffAccessTTL)
	request("PATCH", "/admin/documents/"+replacement.ID+"/review", adminToken, map[string]string{"decision": "rejected", "reason_code": "suspected_fraud", "note": "Investigate identity"}, 200)
	request("PATCH", "/driver/availability", uToken, map[string]bool{"online": true}, 400)
	request("PATCH", "/admin/documents/"+replacement.ID+"/review", adminToken, map[string]string{"decision": "approved", "note": "Identity confirmed after appeal"}, 200)
	request("POST", adminBase+"/approve", adminToken, nil, 200)
	request("PATCH", "/driver/availability", uToken, map[string]bool{"online": true}, 200)
	detail, _ = s.VerificationDetail(u.ID, true)
	var rejectionFound bool
	for _, review := range detail.Reviews {
		if review.DocumentID != nil && *review.DocumentID == licence.ID && review.Decision == "rejected" && review.ReasonCode == "blurry_image" && review.ReviewerID != nil && *review.ReviewerID == a.ID {
			rejectionFound = true
		}
	}
	if !rejectionFound {
		t.Fatal("Permanent rejection history was lost")
	}
	request("PATCH", adminBase+"/suspension", aToken, map[string]interface{}{"suspended": true, "note": "Safety investigation"}, 200)
	request("PATCH", "/driver/availability", uToken, map[string]bool{"online": true}, 403)
	request("PATCH", adminBase+"/suspension", aToken, map[string]interface{}{"suspended": false, "note": "Investigation complete"}, 200)
	request("PATCH", "/driver/availability", uToken, map[string]bool{"online": true}, 401)
	uToken, err = testutil.AppToken(s, u.ID, u.Role, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request("PATCH", "/driver/availability", uToken, map[string]bool{"online": true}, 200)
	// A missed daily sweep must not allow an expired approval to go online.
	past := time.Now().Add(-time.Hour)
	if err := s.DB().Model(&models.DriverDocument{}).Where("id = ?", replacement.ID).Update("expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	request("PATCH", "/driver/availability", uToken, map[string]bool{"online": true}, 400)
	if err := s.RequireDriverApproval(u.ID); err == nil {
		t.Fatal("Expired driver passed dispatch gate")
	}
	if err := s.ExpireDriverDocuments(time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	detail, _ = s.VerificationDetail(u.ID, true)
	if detail.Profile.VerificationStatus != models.Expired || detail.Profile.IsOnline {
		t.Fatal("Expiry failed to block driving")
	}
	var count int64
	s.DB().Model(&models.DriverNotification{}).Where("document_id = ?", replacement.ID).Count(&count)
	if count != 1 {
		t.Fatalf("Expected one expiry notification, got %d", count)
	}
	request("GET", "/driver/notifications", uToken, nil, 200)
	request("PATCH", adminBase+"/verify", aToken, map[string]bool{"verified": true}, 410)
}
