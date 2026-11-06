package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"ride-backend/models"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func transportScenario(t *testing.T) (*Store, *models.User, *models.User, string, *time.Time) {
	t.Helper()
	s := ridePostgresStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	s.Clock = func() time.Time { return now }
	d := sharedDriverFixture(t, s)
	if err := s.db.Model(&models.DriverProfile{}).Where("user_id=?", d.ID).Update("passenger_seats", 3).Error; err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateUser("Passenger", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveTransportSettings(models.TransportSettings{Enabled: true, SharedFee: 700, HailPercent: 12}); err != nil {
		t.Fatal(err)
	}
	id, err := s.PublishShared(d.ID, "publish-acceptance", models.SharedPublish{Pickup: models.RidePoint{Lat: -6.94, Lng: 39.19, Address: "Mbande"}, Dropoff: models.RidePoint{Lat: -6.82, Lng: 39.28, Address: "Posta"}, Capacity: 3, Seats: 2, Price: 2000, WaitMinutes: 6})
	if err != nil {
		t.Fatal(err)
	}
	return s, d, p, id, &now
}

func TestPostgresConfirmedSeatSurvivesDepartureDeadline(t *testing.T) {
	s, d, p, id, now := transportScenario(t)
	if err := s.SetDriverAvailability(context.Background(), d.ID, "online-during-sharing", true); err == nil {
		t.Fatal("hail mode enabled while sharing")
	}
	if _, err := s.SharedAction(p.ID, "passenger", id, "reserve-before-deadline", "reserve", "test", models.SharedAction{}); err != nil {
		t.Fatal(err)
	}
	v, err := s.SharedDetail(id, p.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SharedAction(d.ID, "driver", id, "verify-before-deadline", "verify", "test", models.SharedAction{SeatID: v.MySeat.ID, PIN: v.PIN}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(7 * time.Minute)
	v, err = s.SharedDetail(id, p.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "waiting" || v.Confirmed != 1 || v.MySeat.Status != "confirmed" {
		t.Fatalf("confirmed pickup lost after waiting deadline: status=%s confirmed=%d seat=%s", v.Status, v.Confirmed, v.MySeat.Status)
	}
	if _, err = s.CreateRideRequest(context.Background(), p.ID, "hail-after-deadline", models.CreateRideInput{}, 1000, 2000, 5, time.Minute); err == nil {
		t.Fatal("confirmed passenger allocated to hail")
	}
	if _, err = s.SharedAction(d.ID, "driver", id, "depart-after-deadline", "start", "test", models.SharedAction{}); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresActiveHailCanSendLocationAfterAcceptance(t *testing.T) {
	s := ridePostgresStore(t)
	d := sharedDriverFixture(t, s)
	p, err := s.CreateUser("Passenger", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = s.SetDriverLocation(ctx, d.ID, "location-before-hail", -6.82, 39.28, 5); err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateRideRequest(ctx, p.ID, "hail-for-location", models.CreateRideInput{Pickup: models.RidePoint{Lat: -6.82, Lng: 39.28, Address: "Posta"}, VehicleType: "car"}, 1000, 2000, 5, time.Minute)
	if err != nil || r.Status != "offered" {
		t.Fatal(r, err)
	}
	if _, err = s.AcceptRide(ctx, d.ID, r.ID, "accept-for-location"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetDriverLocation(ctx, d.ID, "location-during-hail", -6.83, 39.27, 5); err != nil {
		t.Fatalf("active trip tracking rejected: %v", err)
	}
}

func holdAndVerify(t *testing.T, s *Store, d, p *models.User, id string) {
	t.Helper()
	if _, err := s.SharedAction(p.ID, "passenger", id, "reserve-passenger", "reserve", "test", models.SharedAction{}); err != nil {
		t.Fatal(err)
	}
	v, err := s.SharedDetail(id, p.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SharedAction(d.ID, "driver", id, "verify-passenger/"+v.MySeat.ID, "verify", "test", models.SharedAction{SeatID: v.MySeat.ID, PIN: v.PIN}); err != nil {
		t.Fatal(err)
	}
}
func finishShared(t *testing.T, s *Store, d *models.User, id string) {
	t.Helper()
	for _, action := range []string{"start", "complete"} {
		if _, err := s.SharedAction(d.ID, "driver", id, "driver-"+action, action, "test", models.SharedAction{}); err != nil {
			t.Fatal(action, err)
		}
	}
}

func TestPostgresTransportReplayAcrossInstancesAndRollback(t *testing.T) {
	s, d, p, id, _ := transportScenario(t)
	second := New(s.db)
	page, err := second.SharedChanges(context.Background(), p.ID, id, 0, 0, 10, 0)
	if err != nil || len(page.Events) != 1 || page.Events[0].Type != "shared_trip.published" {
		t.Fatal(page, err)
	}
	cursor := page.Cursor
	holdAndVerify(t, s, d, p, id)
	page, err = second.SharedChanges(context.Background(), p.ID, id, 0, 0, 10, cursor)
	if err != nil || len(page.Events) != 2 || page.Events[1].Version <= page.Events[0].Version {
		t.Fatal(page, err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "pin") || strings.Contains(string(raw), p.ID) {
		t.Fatal("private data in public event")
	}
	prior := page.Cursor
	err = s.Transaction(func(w *Store) error {
		if err := lockTransportCursor(w.db); err != nil {
			return err
		}
		var trip models.SharedTrip
		if err := w.db.First(&trip, "id=?", id).Error; err != nil {
			return err
		}
		if err := w.appendTransportEvent(&trip, d.ID, "must.rollback"); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	if err == nil {
		t.Fatal("rollback fixture")
	}
	page, err = second.SharedChanges(context.Background(), p.ID, id, 0, 0, 10, prior)
	if err != nil || len(page.Events) != 0 {
		t.Fatal("rolled-back event delivered", page, err)
	}
}

func TestPostgresSharedReceiptReceivableAndRetry(t *testing.T) {
	s, d, p, id, _ := transportScenario(t)
	holdAndVerify(t, s, d, p, id)
	w, err := s.GetWallet(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.db.Model(w).Update("balance", -10000).Error; err != nil {
		t.Fatal(err)
	}
	finishShared(t, s, d, id)
	v, err := s.SharedDetail(id, p.ID, "test")
	if err != nil || v.Status != "completed" || v.MySeat.Status != "completed" || v.Receipt == nil || v.Receipt.Status != "receivable" || v.Receipt.Gross != 2000 || v.Receipt.Net != 1300 {
		t.Fatal(v, err)
	}
	if err = s.checkFinancialEligibility(d.ID); err == nil {
		t.Fatal("outstanding fee did not restrict future trips")
	}
	if err = s.RetryTransportCollections(); err != nil {
		t.Fatal(err)
	}
	if err = s.db.Model(w).Update("balance", 0).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err = s.RetryTransportCollections(); err != nil {
			t.Fatal(err)
		}
	}
	v, err = s.SharedDetail(id, d.ID, "test")
	if err != nil || v.Receipt.Status != "wallet_debited" {
		t.Fatal(v, err)
	}
	var revenue int64
	s.db.Model(&models.LedgerEntry{}).Where("ref_id=? AND account='platform_revenue'", id).Count(&revenue)
	if revenue != 1 {
		t.Fatal("duplicate revenue", revenue)
	}
	wallet, err := s.GetWallet(d.ID)
	if err != nil || wallet.Balance != -700 {
		t.Fatal(wallet, err)
	}
}

func TestPostgresSharedExpiryAndFailedPickupLimits(t *testing.T) {
	s, d, p, id, now := transportScenario(t)
	if _, err := s.SharedAction(p.ID, "passenger", id, "reserve-at-start", "reserve", "test", models.SharedAction{}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.SharedDetail(id, p.ID, "test")
	wrong := "000000"
	if wrong == v.PIN {
		wrong = "111111"
	}
	for i := 0; i < 5; i++ {
		if _, err := s.SharedAction(d.ID, "driver", id, fmt.Sprintf("wrong-code-%d", i), "verify", "test", models.SharedAction{SeatID: v.MySeat.ID, PIN: wrong}); err == nil {
			t.Fatal("wrong PIN accepted")
		}
	}
	if _, err := s.SharedAction(d.ID, "driver", id, "correct-after-five", "verify", "test", models.SharedAction{SeatID: v.MySeat.ID, PIN: v.PIN}); err == nil {
		t.Fatal("locked-out PIN accepted")
	}
	v, _ = s.SharedDetail(id, p.ID, "test")
	if v.Held != 0 || v.Confirmed != 0 {
		t.Fatal(v)
	}
	if _, err := s.SharedAction(p.ID, "passenger", id, "reserve-replacement", "reserve", "test", models.SharedAction{}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(5 * time.Minute)
	v, _ = s.SharedDetail(id, p.ID, "test")
	if v.Held != 0 || v.PIN != "" {
		t.Fatal("worker-free expiry failed", v)
	}
	if err := s.ExpireShared(); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Minute)
	if err := s.ExpireShared(); err != nil {
		t.Fatal(err)
	}
	v, _ = s.SharedDetail(id, p.ID, "test")
	if v.Status != "expired" {
		t.Fatal(v.Status)
	}
	var n int64
	s.db.Model(&models.TransportAssessment{}).Where("trip_id=?", id).Count(&n)
	if n != 0 {
		t.Fatal("expired trip charged")
	}
}

func TestPostgresSharedLocationAndModeAwareSupport(t *testing.T) {
	s, d, p, id, now := transportScenario(t)
	stranger, err := s.CreateUser("Other", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SharedDriverLocation(context.Background(), id, stranger.ID); err == nil {
		t.Fatal("location exposed before booking")
	}
	holdAndVerify(t, s, d, p, id)
	if err = s.SetDriverLocation(context.Background(), d.ID, "shared-location-one", -6.94, 39.19, 5); err != nil {
		t.Fatal(err)
	}
	point, err := s.SharedDriverLocation(context.Background(), id, p.ID)
	if err != nil || point == nil {
		t.Fatal(point, err)
	}
	ticket, err := s.CreateSupportTicket(p.ID, models.CreateSupportTicketInput{SharedTripID: &id, Subject: "Trip issue", Description: "Need help with this shared trip", Category: "ride"})
	if err != nil || ticket.SharedTripID == nil {
		t.Fatal(ticket, err)
	}
	if _, err = s.CreateSupportTicket(stranger.ID, models.CreateSupportTicketInput{SharedTripID: &id, Subject: "Trip issue", Description: "Need help", Category: "ride"}); err == nil {
		t.Fatal("stranger attached private trip")
	}
	alert, err := s.TriggerSharedSOS(p.ID, id, SOSInput{Lat: -6.94, Lng: 39.19, Accuracy: 5})
	if err != nil || alert.SharedTripID == nil || alert.RideID != models.UnassignedRideDriverID {
		t.Fatal(alert, err)
	}
	again, err := s.TriggerSharedSOS(p.ID, id, SOSInput{Lat: -6.94, Lng: 39.19, Accuracy: 5})
	if err != nil || again.ID != alert.ID {
		t.Fatal("SOS duplicated", err)
	}
	*now = now.Add(3 * time.Minute)
	point, err = s.SharedDriverLocation(context.Background(), id, p.ID)
	if err != nil || point != nil {
		t.Fatal("stale location exposed", point, err)
	}
}

func TestPostgresSharingDisablePreservesActiveTrip(t *testing.T) {
	s, d, p, id, _ := transportScenario(t)
	holdAndVerify(t, s, d, p, id)
	cfg, err := s.TransportSettings()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Enabled = false
	if _, err = s.SaveTransportSettings(cfg); err != nil {
		t.Fatal(err)
	}
	finishShared(t, s, d, id)
	v, err := s.SharedDetail(id, p.ID, "test")
	if err != nil || v.Status != "completed" {
		t.Fatal(v, err)
	}
}

func TestPostgresSharedCancelVerifyRace(t *testing.T) {
	s, d, p, id, _ := transportScenario(t)
	if _, err := s.SharedAction(p.ID, "passenger", id, "hold-for-race", "reserve", "test", models.SharedAction{}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.SharedDetail(id, p.ID, "test")
	var wg sync.WaitGroup
	for _, action := range []string{"verify", "release"} {
		wg.Add(1)
		go func(action string) {
			defer wg.Done()
			actor, role := d.ID, "driver"
			if action == "release" {
				actor, role = p.ID, "passenger"
			}
			_, _ = s.SharedAction(actor, role, id, "race-action-"+action, action, "test", models.SharedAction{SeatID: v.MySeat.ID, PIN: v.PIN})
		}(action)
	}
	wg.Wait()
	v, err := s.SharedDetail(id, p.ID, "test")
	if err != nil || v.Held != 0 || v.Confirmed != 0 || v.Available != 2 {
		t.Fatal("inconsistent release/verify race", v, err)
	}
}

func TestPostgresConcurrentHailAndSharedDriverAllocation(t *testing.T) {
	s, d, p, id, _ := transportScenario(t)
	if _, err := s.SharedAction(d.ID, "driver", id, "cancel-before-race", "cancel", "test", models.SharedAction{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.SetDriverAvailability(ctx, d.ID, "online-before-race", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDriverLocation(ctx, d.ID, "location-before-race", -6.94, 39.19, 5); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var sharedID string
	var hail *models.Ride
	var sharingErr, hailErr error
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		sharedID, sharingErr = s.WithContext(ctx).PublishShared(d.ID, "publish-race-allocation", models.SharedPublish{Pickup: models.RidePoint{Lat: -6.94, Lng: 39.19, Address: "Mbande"}, Dropoff: models.RidePoint{Lat: -6.82, Lng: 39.28, Address: "Posta"}, Capacity: 3, Seats: 1, Price: 2000, WaitMinutes: 20})
	}()
	go func() {
		defer wg.Done()
		<-start
		hail, hailErr = s.CreateRideRequest(ctx, p.ID, "hail-race-allocation", models.CreateRideInput{Pickup: models.RidePoint{Lat: -6.94, Lng: 39.19, Address: "Mbande"}, Dropoff: models.RidePoint{Lat: -6.82, Lng: 39.28, Address: "Posta"}, VehicleType: "car"}, 10000, 5000, 10, time.Minute)
	}()
	close(start)
	wg.Wait()
	if ctx.Err() != nil {
		t.Fatal("allocation lock order deadlocked", ctx.Err())
	}
	if hailErr != nil {
		t.Fatal(hailErr)
	}
	if sharingErr == nil && hail.Status == "offered" {
		t.Fatal("incompatible commitments", sharedID, hail.ID)
	}
	if sharingErr != nil && hail.Status != "offered" {
		t.Fatal("neither mode allocated", sharingErr, hail.Status)
	}
}

func TestPostgresConcurrentHailAndSharedPassengerAllocation(t *testing.T) {
	s, _, p, id, _ := transportScenario(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var reserveErr, hailErr error
	var hail *models.Ride
	wg.Add(2)
	start := make(chan struct{})
	go func() {
		defer wg.Done()
		<-start
		_, reserveErr = s.WithContext(ctx).SharedAction(p.ID, "passenger", id, "reserve-passenger-race", "reserve", "test", models.SharedAction{})
	}()
	go func() {
		defer wg.Done()
		<-start
		hail, hailErr = s.CreateRideRequest(ctx, p.ID, "hail-passenger-race", models.CreateRideInput{}, 1000, 2000, 5, time.Minute)
	}()
	close(start)
	wg.Wait()
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	if reserveErr == nil && hailErr == nil && hail.Status != "no_driver_found" {
		t.Fatal("passenger has incompatible commitments")
	}
}

func TestPostgresTransportWatchLatencyAndFanout(t *testing.T) {
	s, d, p, id, _ := transportScenario(t)
	baseline, err := s.SharedChanges(context.Background(), p.ID, id, 0, 0, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	const watchers = 20
	done := make(chan time.Duration, watchers)
	failed := make(chan error, watchers)
	start := time.Now()
	for i := 0; i < watchers; i++ {
		go func() {
			instance := New(s.db)
			page, err := instance.WaitSharedChanges(ctx, p.ID, id, 0, 0, 10, baseline.Cursor)
			if err != nil {
				failed <- err
				return
			}
			if len(page.Events) == 0 {
				failed <- fmt.Errorf("missing durable event")
				return
			}
			done <- time.Since(start)
		}()
	}
	if _, err = s.SharedAction(p.ID, "passenger", id, "hold-live-fanout", "reserve", "test", models.SharedAction{}); err != nil {
		t.Fatal(err)
	}
	samples := []time.Duration{}
	for i := 0; i < watchers; i++ {
		select {
		case elapsed := <-done:
			samples = append(samples, elapsed)
		case err := <-failed:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatal("fanout timed out", ctx.Err())
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p95 := samples[(len(samples)*95+99)/100-1]
	t.Logf("20 independent store instances, durable trip invalidation p95=%s", p95)
	if p95 >= 2*time.Second {
		t.Fatalf("two-second discovery target exceeded: %s", p95)
	}
	_ = d
}

func TestPostgresSharedUnknownCapacityAndImmutableTerms(t *testing.T) {
	s, d, _, id, _ := transportScenario(t)
	seats := 4
	if _, err := s.UpdateDriverIdentifiers(d.ID, models.DriverProfileInput{PassengerSeats: &seats}); err == nil {
		t.Fatal("capacity changed during active trip")
	}
	if _, err := s.SharedAction(d.ID, "driver", id, "cancel-for-capacity", "cancel", "test", models.SharedAction{}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&models.DriverProfile{}).Where("user_id=?", d.ID).Update("passenger_seats", 0).Error; err != nil {
		t.Fatal(err)
	}
	in := models.SharedPublish{Pickup: models.RidePoint{Lat: -6.94, Lng: 39.19, Address: "Mbande"}, Dropoff: models.RidePoint{Lat: -6.82, Lng: 39.28, Address: "Posta"}, Capacity: 3, Seats: 1, Price: 100, WaitMinutes: 20}
	if _, err := s.PublishShared(d.ID, "unknown-capacity-test", in); err == nil {
		t.Fatal("unknown capacity accepted")
	}
	if err := s.db.Model(&models.DriverProfile{}).Where("user_id=?", d.ID).Update("passenger_seats", 3).Error; err != nil {
		t.Fatal(err)
	}
	trip, err := s.PublishShared(d.ID, "immutable-publish-test", in)
	if err != nil {
		t.Fatal(err)
	}
	in.Price = 101
	if _, err = s.PublishShared(d.ID, "immutable-publish-test", in); err == nil {
		t.Fatal("retry changed published price")
	}
	v, _ := s.SharedDetail(trip, d.ID, "test")
	if v.Price != 100 || v.Fee != 700 {
		t.Fatal("published terms changed", v)
	}
}

func TestPostgresSharedSeveralPassengersOneFeeAndNegativeNet(t *testing.T) {
	s, d, p, id, _ := transportScenario(t)
	if err := s.db.Model(&models.SharedTrip{}).Where("id=?", id).Update("price", 100).Error; err != nil {
		t.Fatal(err)
	} // fixture low-fare route
	holdAndVerify(t, s, d, p, id)
	second, err := s.CreateUser("Second", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	holdAndVerify(t, s, d, second, id)
	finishShared(t, s, d, id)
	v, err := s.SharedDetail(id, d.ID, "test")
	if err != nil || v.Receipt == nil || v.Receipt.Fee != 700 || v.Receipt.Gross != 200 || v.Receipt.Net != -500 || v.Receipt.Passengers != 2 {
		t.Fatal(v, err)
	}
	totals, err := s.DriverPeriodEarnings(d.ID, "today")
	if err != nil || totals["gross_tzs"] != int64(200) || totals["commission_tzs"] != int64(700) || totals["trips"] != int64(1) {
		t.Fatal(totals, err)
	}
}

func TestPostgresHailPromoKeepsGrossFeeBasisAndBalancedLedger(t *testing.T) {
	s := ridePostgresStore(t)
	d := sharedDriverFixture(t, s)
	p, err := s.CreateUser("Passenger", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveTransportSettings(models.TransportSettings{SharedFee: 700, HailPercent: 12, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = s.SetDriverLocation(ctx, d.ID, "location-promo-test", -6.94, 39.19, 5); err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateRideRequest(ctx, p.ID, "hail-promo-test", models.CreateRideInput{Pickup: models.RidePoint{Lat: -6.94, Lng: 39.19, Address: "Mbande"}, Dropoff: models.RidePoint{Lat: -6.82, Lng: 39.28, Address: "Posta"}, VehicleType: "car"}, 10000, 2500, 10, time.Minute)
	if err != nil || r.Status != "offered" {
		t.Fatal(r, err)
	}
	if _, err = s.AcceptRide(ctx, d.ID, r.ID, "accept-promo-test"); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"arrived", "start", "complete"} {
		if _, err = s.TransitionRide(ctx, d.ID, r.ID, "promo-action-"+action, action, 2500); err != nil {
			t.Fatal(err)
		}
	}
	promo := models.PromoCode{ID: newID(), Code: "SHARELESS", Title: "Acceptance", DiscountType: "fixed", DiscountValue: 500, Audience: "all", MaxPerUser: 1, Status: "active", Source: "admin", StartsAt: s.now().Add(-time.Hour), ExpiresAt: s.now().Add(time.Hour), CreatedAt: s.now(), UpdatedAt: s.now()}
	if err = s.db.Create(&promo).Error; err != nil {
		t.Fatal(err)
	}
	pay, err := s.InitiatePayment(p.ID, r.ID, "promo-cash-payment", PayInput{Method: "cash", PromoCode: promo.Code})
	if err != nil || pay.Amount != 2000 || pay.Commission != 300 || pay.Status != "succeeded" {
		t.Fatal(pay, err)
	}
	replay, err := s.InitiatePayment(p.ID, r.ID, "promo-cash-payment", PayInput{Method: "cash", PromoCode: promo.Code})
	if err != nil || replay.ID != pay.ID {
		t.Fatal(err)
	}
	var balance int64
	if err = s.db.Model(&models.LedgerEntry{}).Select("coalesce(sum(case when direction='debit' then amount else -amount end),0)").Scan(&balance).Error; err != nil || balance != 0 {
		t.Fatal("unbalanced assessment/promo/collection", balance, err)
	}
	summary, _, outstanding, err := s.TransportFinanceSummary()
	if err != nil || summary.GMV != 2500 || summary.Commission != 300 || outstanding != 0 {
		t.Fatal("finance double counted the payment", summary, outstanding, err)
	}
	wallet, err := s.GetWallet(d.ID)
	if err != nil || wallet.Balance != 200 {
		t.Fatal(wallet, err)
	}
}

func TestPostgresSharedExpiryVerificationRaceAtDeadline(t *testing.T) {
	s, d, p, id, now := transportScenario(t)
	if _, err := s.SharedAction(p.ID, "passenger", id, "reserve-expiry-race", "reserve", "test", models.SharedAction{}); err != nil {
		t.Fatal(err)
	}
	v, err := s.SharedDetail(id, p.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(5 * time.Minute)
	var wg sync.WaitGroup
	wg.Add(2)
	var verifyErr, expiryErr error
	go func() {
		defer wg.Done()
		_, verifyErr = s.SharedAction(d.ID, "driver", id, "verify-expiry-race", "verify", "test", models.SharedAction{SeatID: v.MySeat.ID, PIN: v.PIN})
	}()
	go func() { defer wg.Done(); expiryErr = s.ExpireShared() }()
	wg.Wait()
	if verifyErr == nil || expiryErr != nil {
		t.Fatal("expired seat verified or worker failed", verifyErr, expiryErr)
	}
	v, err = s.SharedDetail(id, p.ID, "test")
	if err != nil || v.MySeat.Status != "expired" || v.Confirmed != 0 || v.PIN != "" {
		t.Fatal(v, err)
	}
}

func TestPostgresSharedPrivacyRetainsReceiptAndOtherBookings(t *testing.T) {
	s, d, p, id, _ := transportScenario(t)
	other, err := s.CreateUser("Other passenger", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
	if err != nil {
		t.Fatal(err)
	}
	holdAndVerify(t, s, d, p, id)
	holdAndVerify(t, s, d, other, id)
	finishShared(t, s, d, id)
	exported, _, err := (sharedPrivacy{s}).Export(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(exported)
	if err != nil || strings.Contains(string(raw), other.ID) || strings.Contains(string(raw), other.Email) {
		t.Fatal("other passenger exported", err)
	}
	if err = s.db.Transaction(func(tx *gorm.DB) error { return (sharedPrivacy{}).Anonymize(tx, p.ID) }); err != nil {
		t.Fatal(err)
	}
	v, err := s.SharedDetail(id, other.ID, "test")
	if err != nil || v.MySeat.Status != "completed" || v.Receipt.Gross != 4000 || v.Receipt.Passengers != 2 {
		t.Fatal("deletion changed another passenger's receipt", v, err)
	}
}
