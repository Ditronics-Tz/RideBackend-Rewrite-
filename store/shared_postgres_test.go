package store

import (
	"context"
	"github.com/google/uuid"
	"ride-backend/models"
	"sync"
	"testing"
	"time"
)

func sharedDriverFixture(t *testing.T, s *Store) *models.User {
	t.Helper()
	driver, err := s.CreateUser("Driver", uuid.NewString()+"@example.test", "unused", models.RoleDriver)
	if err != nil {
		t.Fatal("driver fixture failed")
	}
	if _, err := s.UpdateDriverIdentifiers(driver.ID, models.DriverProfileInput{LicenceNumber: "LIC" + driver.ID, PlateNumber: "PL" + driver.ID, VehicleCardNumber: "VC" + driver.ID}); err != nil {
		t.Fatal("driver identifiers fixture failed")
	}
	now := s.now()
	expires := now.Add(365 * 24 * time.Hour)
	for _, kind := range models.RequiredDocumentTypes {
		var expiry *time.Time
		if models.RequiresExpiry(kind) {
			expiry = &expires
		}
		if err := s.db.Create(&models.DriverDocument{ID: uuid.NewString(), DriverID: driver.ID, Type: kind, FilePath: "private/test", Status: "approved", IsCurrent: true, ExpiresAt: expiry, CreatedAt: now}).Error; err != nil {
			t.Fatal("driver document fixture failed")
		}
	}
	if err := s.db.Model(&models.DriverProfile{}).Where("user_id = ?", driver.ID).Updates(map[string]any{"is_online": true, "verification_status": models.Approved, "vehicle_type": "car"}).Error; err != nil {
		t.Fatal("driver availability fixture failed")
	}
	return driver
}
func TestPostgresSharedLastSeatVerificationAndWholeTripFee(t *testing.T) {
	s := ridePostgresStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	s.Clock = func() time.Time { return now }
	d := sharedDriverFixture(t, s)
	cfg, err := s.SaveTransportSettings(models.TransportSettings{Enabled: true, SharedFee: 700, HailPercent: 12})
	if err != nil {
		t.Fatal(err)
	}
	input := models.SharedPublish{Pickup: models.RidePoint{Lat: -6.94, Lng: 39.19, Address: "Mbande"}, Dropoff: models.RidePoint{Lat: -6.82, Lng: 39.28, Address: "Gerezani Posta"}, Capacity: 3, Seats: 1, Price: 1800, WaitMinutes: 20}
	trip, err := s.PublishShared(d.ID, "publish-001", input)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.PublishShared(d.ID, "publish-001", input)
	if err != nil || replay != trip {
		t.Fatal("publish replay", err)
	}
	passengers := make([]string, 12)
	for i := range passengers {
		p, err := s.CreateUser("Passenger", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
		if err != nil {
			t.Fatal(err)
		}
		passengers[i] = p.ID
	}
	list, err := s.SharedList(passengers[0], "passenger", "nearby", "test", input.Pickup.Lat, input.Pickup.Lng, 5, 50, 0)
	if err != nil || len(list) != 1 {
		t.Fatal("pickup missing", list, err)
	}
	far, err := s.SharedList(passengers[0], "passenger", "nearby", "test", 0, 0, 5, 50, 0)
	if err != nil || len(far) != 0 {
		t.Fatal("out-of-radius pickup", err)
	}
	var wg sync.WaitGroup
	winners := make(chan string, len(passengers))
	for _, p := range passengers {
		wg.Add(1)
		go func(actor string) {
			defer wg.Done()
			if _, err := s.SharedAction(actor, "passenger", trip, "reserve-001", "reserve", "test", models.SharedAction{}); err == nil {
				winners <- actor
			}
		}(p)
	}
	wg.Wait()
	close(winners)
	if len(winners) != 1 {
		t.Fatalf("last seat awarded %d times", len(winners))
	}
	p := <-winners
	v, err := s.SharedDetail(trip, p, "test")
	if err != nil || v.Held != 1 || v.Available != 0 || v.Confirmed != 0 || v.MySeat == nil || len(v.PIN) != 6 {
		t.Fatal("invalid hold", err)
	}
	first := v.MySeat.ID
	driver, _ := s.SharedDetail(trip, d.ID, "test")
	if driver.PIN != "" || len(driver.Manifest) != 1 {
		t.Fatal("manifest privacy")
	}
	if _, err := s.CreateRideRequest(context.Background(), p, "hail-while-held", models.CreateRideInput{}, 2000, 1000, 5, time.Minute); err == nil {
		t.Fatal("hailed ride while reserved")
	}
	act := func(action, key string, in models.SharedAction) error {
		_, e := s.SharedAction(d.ID, "driver", trip, key, action, "test", in)
		return e
	}
	if act("start", "early-start", models.SharedAction{}) == nil {
		t.Fatal("started before pickup")
	}
	now = now.Add(5 * time.Minute)
	if e := act("verify", "late-verify", models.SharedAction{SeatID: first, PIN: v.PIN}); e == nil {
		t.Fatal("expired PIN accepted")
	}
	if _, e := s.SharedAction(p, "passenger", trip, "reserve-002", "reserve", "test", models.SharedAction{}); e != nil {
		t.Fatal(e)
	}
	v, _ = s.SharedDetail(trip, p, "test")
	if v.MySeat.ID == first {
		t.Fatal("expired reservation reused")
	}
	wrong := "000000"
	if wrong == v.PIN {
		wrong = "111111"
	}
	for i := 0; i < 2; i++ {
		if act("verify", "bad-replayed", models.SharedAction{SeatID: v.MySeat.ID, PIN: wrong}) == nil {
			t.Fatal("accepted wrong code")
		}
	}
	var row models.SharedSeat
	s.db.First(&row, "id=?", v.MySeat.ID)
	if row.Failures != 1 {
		t.Fatal("retry consumed another attempt")
	}
	if e := act("verify", "good-verify", models.SharedAction{SeatID: v.MySeat.ID, PIN: v.PIN}); e != nil {
		t.Fatal(e)
	}
	if e := act("start", "valid-start", models.SharedAction{}); e != nil {
		t.Fatal(e)
	}
	cfg.SharedFee = 1000
	cfg.HailPercent = 20
	if _, e := s.SaveTransportSettings(cfg); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SaveTransportSettings(cfg); e == nil {
		t.Fatal("overwrote newer settings")
	}
	for i := 0; i < 2; i++ {
		if e := act("complete", "complete-001", models.SharedAction{}); e != nil {
			t.Fatal(e)
		}
	}
	if e := act("complete", "complete-002", models.SharedAction{}); e == nil {
		t.Fatal("charged again")
	}
	var entries []models.LedgerEntry
	if err := s.db.Where("ref_id=?", trip).Find(&entries).Error; err != nil || len(entries) != 2 {
		t.Fatal("expected balanced settlement", entries, err)
	}
	for _, e := range entries {
		if e.Amount != 700 {
			t.Fatal("fee changed after publication")
		}
	}
}

func TestPostgresHailCommissionSnapshot(t *testing.T) {
	s := ridePostgresStore(t)
	d := sharedDriverFixture(t, s)
	p, e := s.CreateUser("Passenger", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := s.SaveTransportSettings(models.TransportSettings{Enabled: true, SharedFee: 750, HailPercent: 12})
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e = s.SetDriverLocation(ctx, d.ID, "location-123", -6.82, 39.27, 5); e != nil {
		t.Fatal(e)
	}
	route := models.CreateRideInput{Pickup: models.RidePoint{Lat: -6.82, Lng: 39.27, Address: "Posta"}, Dropoff: models.RidePoint{Lat: -6.94, Lng: 39.19, Address: "Mbande"}, VehicleType: "car"}
	ride, e := s.CreateRideRequest(ctx, p.ID, "hail-example", route, 3000, 2500, 10, time.Minute)
	if e != nil || ride.Status != "offered" {
		t.Fatal("offer", ride, e)
	}
	cfg.HailPercent = 25
	if _, e = s.SaveTransportSettings(cfg); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcceptRide(ctx, d.ID, ride.ID, "accept-123"); e != nil {
		t.Fatal(e)
	}
	for _, act := range []string{"arrived", "start", "complete"} {
		ride, e = s.TransitionRide(ctx, d.ID, ride.ID, "hail-step-"+act, act, 2500)
		if e != nil {
			t.Fatal(act, e)
		}
	}
	if ride.AssessedFee == nil || *ride.AssessedFee != 300 {
		t.Fatal("percentage was not snapshotted", ride.AssessedFee)
	}
	total, e := s.DriverPeriodEarnings(d.ID, "today")
	if e != nil || total["commission_tzs"] != int64(300) || total["net_tzs"] != int64(2200) {
		t.Fatal("driver earnings", total, e)
	}
	pay, e := s.InitiatePayment(p.ID, ride.ID, "payment-123", PayInput{Method: "cash"})
	if e != nil || pay.Commission != 300 {
		t.Fatal("settlement", pay, e)
	}
	total, e = s.DriverPeriodEarnings(d.ID, "today")
	if e != nil || total["trips"] != int64(1) {
		t.Fatal("double counted", total, e)
	}
}
