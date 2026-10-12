package store_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"ride-backend/models"
	"ride-backend/store"
	"ride-backend/testutil"
	"strings"
	"testing"
	"time"
)

func TestSafetyLifecycleAndPrivacy(t *testing.T) {
	s, cfg, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	s.Clock = func() time.Time { return now }
	a, err := s.CreateUser("Passenger", uuid.NewString()+"@test.invalid", "", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateUser("Driver Name", uuid.NewString()+"@test.invalid", "", models.RoleDriver)
	if err != nil {
		t.Fatal(err)
	}
	ride := models.Ride{ID: uuid.NewString(), PassengerID: a.ID, DriverID: b.ID, Status: "in_progress", Fare: 5000}
	if err := s.DB().Create(&ride).Error; err != nil {
		t.Fatal(err)
	}
	c := models.EmergencyContact{Name: "Contact", Phone: "+255712345678", NotifyOnSOS: true}
	if err := s.SaveEmergencyContact(a.ID, &c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TriggerSOS(uuid.NewString(), ride.ID, "http://localhost", store.SOSInput{}); err == nil {
		t.Fatal("outsider triggered SOS")
	}
	events, unsubscribe := s.Events.Subscribe()
	defer unsubscribe()
	alert, err := s.TriggerSOS(a.ID, ride.ID, "http://localhost", store.SOSInput{Lat: -6.8, Lng: 39.2})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-events:
		if e.Type != "sos.created" {
			t.Fatal(e.Type)
		}
	default:
		t.Fatal("SSE event missing")
	}
	again, err := s.TriggerSOS(a.ID, ride.ID, "http://localhost", store.SOSInput{})
	if err != nil || again.ID != alert.ID {
		t.Fatal("trigger not idempotent", err)
	}
	now = now.Add(59 * time.Second)
	if err := s.ProcessSafety(cfg); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().First(&again, "id = ?", alert.ID).Error; err != nil {
		t.Fatal(err)
	}
	if again.EscalationLevel != 0 {
		t.Fatal("early escalation")
	}
	now = now.Add(time.Second)
	if err := s.ProcessSafety(cfg); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessSafety(cfg); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().First(&again, "id = ?", alert.ID).Error; err != nil {
		t.Fatal(err)
	}
	if again.EscalationLevel != 1 {
		t.Fatal("escalation not idempotent")
	}
	staff := &models.StaffUser{ID: uuid.NewString(), Role: models.StaffRoleSupport}
	if err := s.WorkSOS(alert.ID, "acknowledge", staff, store.SOSAction{}, 3); err != nil {
		t.Fatal(err)
	}
	if err := s.WorkSOS(alert.ID, "acknowledge", &models.StaffUser{ID: uuid.NewString(), Role: models.StaffRoleSupport}, store.SOSAction{}, 3); err == nil {
		t.Fatal("double acknowledge")
	}
	for i := 0; i < 30; i++ {
		if err := s.SOSPing(a.ID, alert.ID, -6.8, 39.2, 10, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SOSPing(a.ID, alert.ID, -6.8, 39.2, 10, 0); err == nil {
		t.Fatal("ping rate limit missing")
	}
	if err := s.ReportSafe(a.ID, alert.ID, 3); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().First(&again, "id = ?", alert.ID).Error; err != nil {
		t.Fatal(err)
	}
	if again.ResolvedAt != nil || !again.UserReportedSafe {
		t.Fatal("older alarm auto closed")
	}
	token, err := s.ShareRide(ride.ID, a.ID, "http://localhost", nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.PublicShare(token)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "phone") || strings.Contains(string(raw), a.ID) || strings.Contains(string(raw), a.Email) {
		t.Fatal("share leaks PII")
	}
	now = now.Add(13 * time.Hour)
	if _, err := s.PublicShare(token); err == nil {
		t.Fatal("expired token accepted")
	}
	now = now.AddDate(0, 0, 31)
	if err := s.ProcessSafety(cfg); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := s.DB().Model(&models.SOSLocationPing{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("retention did not clear trail")
	}
}
