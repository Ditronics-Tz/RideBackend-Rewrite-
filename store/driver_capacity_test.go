package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"ride-backend/models"
	"ride-backend/testutil"
)

func TestSeatAndVehicleUpdatesKeepDocumentApproval(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	driver, err := s.CreateUser("Capacity Driver", uuid.NewString()+"@example.test", "unused", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateDriverIdentifiers(driver.ID, models.DriverProfileInput{LicenceNumber: "LIC" + driver.ID, PlateNumber: "PL" + driver.ID, VehicleCardNumber: "VC" + driver.ID, VehicleType: "bajaji"}); err != nil {
		t.Fatal(err)
	}
	if err = s.DB().Model(&models.DriverProfile{}).Where("user_id = ?", driver.ID).Updates(map[string]any{"verification_status": models.Approved, "is_online": true, "passenger_seats": 1}).Error; err != nil {
		t.Fatal(err)
	}
	doc := models.DriverDocument{ID: uuid.NewString(), DriverID: driver.ID, Type: "driving_licence", FilePath: "private/test", Status: "approved", IsCurrent: true, CreatedAt: time.Now().UTC()}
	if err = s.DB().Create(&doc).Error; err != nil {
		t.Fatal(err)
	}

	seats := 3
	updated, err := s.UpdateDriverIdentifiers(driver.ID, models.DriverProfileInput{PassengerSeats: &seats})
	if err != nil {
		t.Fatal(err)
	}
	if updated.PassengerSeats != 3 || updated.VerificationStatus != models.Approved || !updated.IsOnline {
		t.Fatalf("seat update changed approval: seats=%d status=%s online=%v", updated.PassengerSeats, updated.VerificationStatus, updated.IsOnline)
	}
	if err = s.DB().First(&doc, "id = ?", doc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if doc.Status != "approved" {
		t.Fatalf("seat update reopened document: %s", doc.Status)
	}

	updated, err = s.UpdateDriverIdentifiers(driver.ID, models.DriverProfileInput{VehicleType: "car"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.VehicleType != "car" || updated.VerificationStatus != models.Approved || !updated.IsOnline {
		t.Fatalf("vehicle update changed approval: type=%s status=%s online=%v", updated.VehicleType, updated.VerificationStatus, updated.IsOnline)
	}
	if err = s.DB().First(&doc, "id = ?", doc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if doc.Status != "approved" {
		t.Fatalf("vehicle update reopened document: %s", doc.Status)
	}

	updated, err = s.UpdateDriverIdentifiers(driver.ID, models.DriverProfileInput{LicenceNumber: "NEW" + driver.ID})
	if err != nil {
		t.Fatal(err)
	}
	if updated.VerificationStatus != models.PendingDocuments || updated.IsOnline {
		t.Fatalf("licence change kept approval: status=%s online=%v", updated.VerificationStatus, updated.IsOnline)
	}
	if err = s.DB().First(&doc, "id = ?", doc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if doc.Status != "submitted" {
		t.Fatalf("licence change left document %s", doc.Status)
	}
}
