package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"ride-backend/models"
	"ride-backend/notifications"
	"ride-backend/testutil"
)

type selectiveDelivery struct{ attempts []string }

func (d *selectiveDelivery) Send(_ context.Context, m notifications.Message) error {
	d.attempts = append(d.attempts, m.ID)
	if m.Recipient == "unavailable" {
		return errors.New("offline")
	}
	return nil
}

func TestNotificationFailureDoesNotBlockOtherRecipients(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	delivery := &selectiveDelivery{}
	s.Delivery = delivery
	now := time.Now().UTC()
	failed := models.NotificationDelivery{ID: uuid.NewString(), UserID: "unavailable", Template: "sos_created", Language: "sw", Status: "pending", CreatedAt: now.Add(-time.Second)}
	contact := models.NotificationDelivery{ID: uuid.NewString(), UserID: "contact", Phone: "+255712345678", Template: "sos_contact", Language: "sw", Status: "pending", CreatedAt: now}
	for _, row := range []*models.NotificationDelivery{&failed, &contact} {
		if err := s.DB().Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ProcessNotifications(); err == nil {
		t.Fatal("delivery failures must still be reported")
	}
	if len(delivery.attempts) != 2 {
		t.Fatalf("attempted %d recipients, want 2", len(delivery.attempts))
	}
	if err := s.DB().First(&contact, "id = ?", contact.ID).Error; err != nil {
		t.Fatal(err)
	}
	if contact.Status != "delivered" {
		t.Fatalf("contact status = %s", contact.Status)
	}
	if err := s.DB().First(&failed, "id = ?", failed.ID).Error; err != nil {
		t.Fatal(err)
	}
	if failed.Status != "pending" || failed.Attempts != 1 {
		t.Fatalf("failed delivery lost its retry: %+v", failed)
	}
}

func TestSevereIncidentRaisesExistingOpenReportPriority(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	user := uuid.NewString()
	ride := models.Ride{ID: uuid.NewString(), PassengerID: user, DriverID: uuid.NewString(), Status: "completed"}
	if err := s.DB().Create(&ride).Error; err != nil {
		t.Fatal(err)
	}
	report := models.Report{ID: uuid.NewString(), ReporterID: user, ReportedID: ride.DriverID, RideID: ride.ID, Category: "other", Description: "Original concern", Status: "open", Priority: "normal"}
	if err := s.DB().Create(&report).Error; err != nil {
		t.Fatal(err)
	}
	incident := models.IncidentReport{RideID: ride.ID, Category: "accident", Description: "Accident details", OccurredAt: time.Now().UTC().Add(-time.Minute)}
	if err := s.CreateIncident(user, &incident); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().First(&report, "id = ?", report.ID).Error; err != nil {
		t.Fatal(err)
	}
	if report.Priority != "high" {
		t.Fatalf("existing report priority = %s, want high", report.Priority)
	}
	if report.Description != "Original concern" {
		t.Fatal("original report evidence overwritten")
	}
}

func TestDiscountedPartialRefundsKeepPositiveBalancedLedgerEntries(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	passenger, err := s.CreateUser("Passenger", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := s.CreateUser("Driver", uuid.NewString()+"@test.invalid", "", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	admin := &models.StaffUser{ID: uuid.NewString(), Role: models.StaffRoleAdmin}
	if err := s.AdjustWallet(driver.ID, admin.ID, uuid.NewString(), "Opening earned balance", 1); err != nil {
		t.Fatal(err)
	}
	rideID := uuid.NewString()
	payment := models.PaymentTransaction{ID: uuid.NewString(), RideID: &rideID, PayerID: passenger.ID, DriverID: &driver.ID, Method: "wallet", Fare: 8, Amount: 5, DiscountAmount: 3, Commission: 7, Status: "succeeded", IdempotencyKey: uuid.NewString()}
	if err := s.DB().Create(&payment).Error; err != nil {
		t.Fatal(err)
	}
	for _, amount := range []int64{2, 1, 2} {
		refund, err := s.RequestRefund(payment.ID, uuid.NewString(), "Partial return", amount, admin)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WorkRefund(refund.ID, "approve", "", admin); err != nil {
			t.Fatalf("refund of %d failed: %v", amount, err)
		}
		var net int64
		if err := s.DB().Model(&models.LedgerEntry{}).Where("ref_type = ? AND ref_id = ?", "refund", refund.ID).Select("COALESCE(SUM(CASE WHEN direction = 'credit' THEN amount ELSE -amount END),0)").Scan(&net).Error; err != nil {
			t.Fatal(err)
		}
		if net != 0 {
			t.Fatalf("refund ledger unbalanced: %d", net)
		}
	}
	wallet, err := s.GetWallet(driver.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Balance != 0 {
		t.Fatalf("driver refund reversal = %d, want 0", wallet.Balance)
	}
	wallet, err = s.GetWallet(passenger.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Balance != 5 {
		t.Fatalf("passenger refunded balance = %d, want 5", wallet.Balance)
	}
	if err := s.DB().First(&payment, "id = ?", payment.ID).Error; err != nil {
		t.Fatal(err)
	}
	if payment.Status != "refunded" || payment.RefundedAmount != 5 {
		t.Fatal("full refund status not reached")
	}
	var commission, subsidy int64
	if err := s.DB().Model(&models.LedgerEntry{}).Where("ref_type = ? AND account = ?", "refund", "platform_revenue").Select("COALESCE(SUM(amount),0)").Scan(&commission).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.DB().Model(&models.LedgerEntry{}).Where("ref_type = ? AND account = ?", "refund", "promo_subsidy").Select("COALESCE(SUM(amount),0)").Scan(&subsidy).Error; err != nil {
		t.Fatal(err)
	}
	if commission != 7 || subsidy != 3 {
		t.Fatalf("reversals: commission=%d subsidy=%d", commission, subsidy)
	}
}

func TestPartialRefundRoundingPreservesPreviouslyProcessedRefunds(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	passenger, err := s.CreateUser("Passenger", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := s.CreateUser("Driver", uuid.NewString()+"@test.invalid", "", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	admin := &models.StaffUser{ID: uuid.NewString(), Role: models.StaffRoleAdmin}
	if err := s.AdjustWallet(driver.ID, admin.ID, uuid.NewString(), "Existing earned balance", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.AdjustWallet(passenger.ID, admin.ID, uuid.NewString(), "Previously refunded amount", 1); err != nil {
		t.Fatal(err)
	}
	rideID := uuid.NewString()
	payment := models.PaymentTransaction{ID: uuid.NewString(), RideID: &rideID, PayerID: passenger.ID, DriverID: &driver.ID, Method: "wallet", Fare: 8, Amount: 5, DiscountAmount: 3, Commission: 7, RefundedAmount: 1, Status: "partially_refunded", IdempotencyKey: uuid.NewString()}
	if err := s.DB().Create(&payment).Error; err != nil {
		t.Fatal(err)
	}
	legacy := models.Refund{ID: uuid.NewString(), TransactionID: payment.ID, Amount: 1, Status: "processed", RequestedBy: admin.ID, IdempotencyKey: uuid.NewString()}
	if err := s.DB().Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	entry := models.LedgerEntry{ID: uuid.NewString(), Account: "platform_revenue", Direction: "debit", Amount: 1, RefType: "refund", RefID: legacy.ID, IdempotencyKey: uuid.NewString()}
	if err := s.DB().Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	for _, amount := range []int64{1, 3} {
		refund, err := s.RequestRefund(payment.ID, uuid.NewString(), "Remaining return", amount, admin)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WorkRefund(refund.ID, "approve", "", admin); err != nil {
			t.Fatal(err)
		}
	}
	var commission int64
	if err := s.DB().Model(&models.LedgerEntry{}).Where("account = ? AND ref_type = ?", "platform_revenue", "refund").Select("SUM(amount)").Scan(&commission).Error; err != nil {
		t.Fatal(err)
	}
	if commission != 7 {
		t.Fatalf("commission reversed=%d, want 7 including historical entries", commission)
	}
	wallet, err := s.GetWallet(driver.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Balance != 0 {
		t.Fatalf("driver balance = %d", wallet.Balance)
	}
	if err := s.DB().First(&entry, "id = ?", entry.ID).Error; err != nil {
		t.Fatal(err)
	}
	if entry.Amount != 1 {
		t.Fatal("historical ledger entry changed")
	}
}
