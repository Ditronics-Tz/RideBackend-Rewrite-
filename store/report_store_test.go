package store_test

import (
	"errors"
	"github.com/google/uuid"
	"ride-backend/models"
	"ride-backend/store"
	"ride-backend/testutil"
	"testing"
	"time"
)

func TestReportsBlocksAndAutoFlag(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	driver, err := s.CreateUser("Driver", "report-driver@test.invalid", "", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DB().Model(&models.DriverProfile{}).Where("user_id = ?", driver.ID).Update("verification_status", models.Approved).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	s.Clock = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		passenger, err := s.CreateUser("Passenger", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.BlockUser(passenger.ID, driver.ID); err == nil {
			t.Fatal("non-participants can block")
		}
		ride := models.Ride{ID: uuid.NewString(), PassengerID: passenger.ID, DriverID: driver.ID, Status: "in_progress", Fare: 5000, CreatedAt: now}
		if err := s.DB().Create(&ride).Error; err != nil {
			t.Fatal(err)
		}
		if err := s.BlockUser(passenger.ID, driver.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.CheckRideEligibility(passenger.ID, driver.ID); err == nil {
			t.Fatal("blocked pair matched")
		}
		var saved models.Ride
		if err := s.DB().First(&saved, "id = ?", ride.ID).Error; err != nil {
			t.Fatal(err)
		}
		if saved.Status != "in_progress" {
			t.Fatal("blocking cancelled a ride")
		}
		r := models.Report{ReportedID: driver.ID, RideID: ride.ID, Category: "unsafe_driving", Description: "Please investigate"}
		if err := s.SubmitReport(passenger.ID, &r, 3, 30); err != nil {
			t.Fatal(err)
		}
		duplicate := models.Report{ReportedID: driver.ID, RideID: ride.ID, Category: "other", Description: "Duplicate"}
		if err := s.SubmitReport(passenger.ID, &duplicate, 3, 30); err == nil {
			t.Fatal("duplicate accepted")
		}
	}
	var flags int64
	if err := s.DB().Model(&models.AccountFlag{}).Count(&flags).Error; err != nil {
		t.Fatal(err)
	}
	if flags != 1 {
		t.Fatalf("flags=%d", flags)
	}
	var u models.User
	if err := s.DB().First(&u, "id = ?", driver.ID).Error; err != nil {
		t.Fatal(err)
	}
	if u.Status != "active" {
		t.Fatal("auto suspended")
	}
}
func TestReportPermissionsAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		role    models.StaffRole
		outcome string
		days    int
		allowed bool
	}{{"support warning", models.StaffRoleSupport, "warning", 0, true}, {"support dismiss", models.StaffRoleSupport, "dismissed", 0, true}, {"support 6 days", models.StaffRoleSupport, "suspended", 6, true}, {"support 8 days", models.StaffRoleSupport, "suspended", 8, false}, {"support indefinite", models.StaffRoleSupport, "suspended", 0, false}, {"support ban", models.StaffRoleSupport, "banned", 0, false}, {"admin ban", models.StaffRoleAdmin, "banned", 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, err := testutil.SetupTestStore()
			if err != nil {
				t.Fatal(err)
			}
			a, err := s.CreateUser("a", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.CreateUser("b", uuid.NewString()+"@test.invalid", "", models.RoleDriver)
			if err != nil {
				t.Fatal(err)
			}
			r := models.Report{ID: uuid.NewString(), ReporterID: a.ID, ReportedID: b.ID, RideID: uuid.NewString(), Status: "open", CreatedAt: time.Now()}
			if err := s.DB().Create(&r).Error; err != nil {
				t.Fatal(err)
			}
			in := store.ResolveReportInput{Outcome: tc.outcome, Note: "Reviewed carefully"}
			if tc.days > 0 {
				until := time.Now().Add(time.Duration(tc.days) * 24 * time.Hour)
				in.Until = &until
			}
			err = s.WorkReport(r.ID, "resolve", &models.StaffUser{ID: uuid.NewString(), Role: tc.role}, "", "", in)
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed %v, err %v", tc.allowed, err)
			}
		})
	}
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateUser("a", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateUser("b", uuid.NewString()+"@test.invalid", "", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 11; i++ {
		ride := models.Ride{ID: uuid.NewString(), PassengerID: a.ID, DriverID: b.ID, Status: "completed"}
		if err := s.DB().Create(&ride).Error; err != nil {
			t.Fatal(err)
		}
		r := models.Report{ReportedID: b.ID, RideID: ride.ID, Category: "other", Description: "Reported"}
		err = s.SubmitReport(a.ID, &r, 3, 30)
		if i < 10 && err != nil {
			t.Fatal(err)
		}
		if i == 10 {
			var e *store.OperationError
			if !errors.As(err, &e) || e.Status != 429 {
				t.Fatalf("expected 429, got %v", err)
			}
		}
	}
}
