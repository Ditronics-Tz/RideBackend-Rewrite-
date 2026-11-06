package store

import (
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/models"
)

// Assessment is independent of collection. A completed journey remains completed
// when the wallet is frozen or at its configured floor. Balanced receivables
// represent what is owed until wallet collection is possible.
func (s *Store) assessTransport(mode, id, driver string, gross, fee int64, passengers, version int) error {
	a := models.TransportAssessment{Mode: mode, TripID: id, DriverID: driver, Gross: gross, Fee: fee, Net: gross - fee, Passengers: passengers, FeeVersion: version, Status: "receivable", Created: s.now().Unix(), Updated: s.now().Unix()}
	r := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&a)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected == 0 {
		return nil
	}
	key := "assessment:" + mode + ":" + id
	if err := s.postSystem("driver_receivable", "debit", fee, mode+"_trip", id, key+":owed"); err != nil {
		return err
	}
	if err := s.postSystem("platform_revenue", "credit", fee, mode+"_trip", id, key+":revenue"); err != nil {
		return err
	}
	return s.collectTransport(&a)
}

func (s *Store) collectTransport(a *models.TransportAssessment) error {
	if a.Status != "receivable" {
		return nil
	}
	seed := models.Wallet{ID: newID(), UserID: a.DriverID, Status: "active", CreatedAt: s.now()}
	if err := s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoNothing: true}).Create(&seed).Error; err != nil {
		return err
	}
	var wallet models.Wallet
	// A frozen wallet is a collection issue, not a reason to roll back completion.
	err := s.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&wallet, "user_id=?", a.DriverID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil && wallet.Status != "active" {
		return nil
	}
	settings, err := s.GetPaymentSettings()
	if err != nil {
		return err
	}
	if wallet.Balance-wallet.Held-a.Fee < -settings.DriverMaxNegativeBalance {
		return nil
	}
	key := "assessment:" + a.Mode + ":" + a.TripID
	if err := s.postWallet(a.DriverID, "driver_wallet", "debit", a.Fee, -settings.DriverMaxNegativeBalance, a.Mode+"_trip", a.TripID, key+":wallet"); err != nil {
		return err
	}
	if err := s.postSystem("driver_receivable", "credit", a.Fee, a.Mode+"_trip", a.TripID, key+":collected"); err != nil {
		return err
	}
	a.Status = "wallet_debited"
	a.Updated = s.now().Unix()
	return s.db.Save(a).Error
}

// RetryTransportCollections is bounded, idempotent and safe across workers.
func (s *Store) RetryTransportCollections() error {
	var rows []models.TransportAssessment
	if err := s.db.Where("status='receivable'").Order("created,mode,trip_id").Limit(100).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := s.Transaction(func(w *Store) error {
			var a models.TransportAssessment
			if err := w.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&a, "mode=? AND trip_id=?", row.Mode, row.TripID).Error; err != nil {
				return err
			}
			return w.collectTransport(&a)
		}); err != nil {
			return fmt.Errorf("transport collection: %w", err)
		}
	}
	return nil
}

func (s *Store) SaveTransportSettingsByStaff(actor *models.StaffUser, in models.TransportSettings, reason string) (models.TransportSettings, error) {
	if actor == nil || actor.Role != models.StaffRoleAdmin {
		return in, OpError(403, "Administrator required")
	}
	if reason == "" {
		reason = "Transport fee configuration updated"
	}
	if len([]rune(reason)) > 250 {
		return in, FieldError("reason", "Use at most 250 characters")
	}
	var result models.TransportSettings
	err := s.Transaction(func(w *Store) error {
		var err error
		result, err = w.SaveTransportSettings(in)
		if err != nil {
			return err
		}
		return w.CreateAuditLog(actor.ID, "transport_settings_updated", "transport_settings", "1", "", map[string]any{"reason": reason, "version": result.Version, "shared_fee": result.SharedFee, "hail_percent": result.HailPercent, "enabled": result.Enabled})
	})
	return result, err
}
