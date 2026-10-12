package store

import (
	"context"
	"encoding/json"
	"gorm.io/gorm/clause"
	"ride-backend/models"
	"sort"
	"strings"
	"time"
)

func (s *Store) FraudRules() ([]models.FraudRule, error) {
	rows := []models.FraudRule{}
	if err := s.db.Order("key ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, r := range rows {
		known[r.Key] = true
	}
	for key, params := range fraudDefaults {
		if !known[key] {
			severity := "medium"
			auto := ""
			if key == "duplicate_identifiers" || key == "location_mismatch" || key == "payout_anomaly" {
				severity = "high"
				auto = "payout_hold,promo_block"
			}
			rows = append(rows, models.FraudRule{Key: key, Enabled: true, Params: params, Severity: severity, AutoMitigation: auto})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows, nil
}
func (s *Store) UpdateFraudRules(actor string, rules []models.FraudRule) error {
	return s.Transaction(func(tx *Store) error {
		for _, r := range rules {
			defaults, ok := fraudDefaults[r.Key]
			if !ok {
				return FieldError("key", "unknown fraud rule")
			}
			if !map[string]bool{"low": true, "medium": true, "high": true}[r.Severity] {
				return FieldError("severity", "invalid severity")
			}
			params, err := ruleParams(r)
			if err != nil {
				return err
			}
			var expected map[string]float64
			if err := json.Unmarshal([]byte(defaults), &expected); err != nil {
				return err
			}
			if len(params) != len(expected) {
				return FieldError("params", "all documented numeric parameters are required")
			}
			for key, v := range params {
				if _, ok := expected[key]; !ok || v <= 0 || v > float64(MaxMoney) || v != float64(int64(v)) {
					return FieldError("params", "unknown or non-positive/integer parameter")
				}
				if (key == "cancel_percent" || key == "min_distance_percent") && v > 100 {
					return FieldError("params", "percent must be at most 100")
				}
			}
			for _, m := range strings.Split(r.AutoMitigation, ",") {
				if m != "" && m != "payout_hold" && m != "promo_block" {
					return FieldError("auto_mitigation", "only payout_hold and promo_block are allowed")
				}
			}
			r.UpdatedBy = &actor
			r.UpdatedAt = tx.now()
			r.CreatedAt = tx.now()
			if err := tx.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"enabled", "params", "severity", "auto_mitigation", "updated_by", "updated_at"})}).Create(&r).Error; err != nil {
				return err
			}
			if err := tx.CreateAuditLog(actor, "update_fraud_rule", "fraud_rule", r.Key, "", map[string]any{"enabled": r.Enabled, "severity": r.Severity}); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) evaluateFraud(ctx context.Context, event models.FraudEvent, rules []models.FraudRule) ([]Finding, error) {
	findings := []Finding{}
	for _, settings := range rules {
		if !settings.Enabled {
			continue
		}
		available, _, err := s.RuleAvailability(settings.Key)
		if err != nil {
			return nil, err
		}
		if !available {
			continue
		}
		var rule Rule = builtinRule{s, settings}
		rows, err := rule.Evaluate(ctx, event)
		if err != nil {
			return nil, err
		}
		findings = append(findings, rows...)
	}
	return findings, nil
}
func (s *Store) EvaluateFraud(ctx context.Context, event models.FraudEvent) error {
	rules, err := s.FraudRules()
	if err != nil {
		return err
	}
	findings, err := s.evaluateFraud(ctx, event, rules)
	if err != nil {
		return err
	}
	for _, finding := range findings {
		var settings models.FraudRule
		for _, r := range rules {
			if r.Key == finding.RuleKey {
				settings = r
			}
		}
		if err := s.mergeFinding(finding, settings); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) mergeFinding(f Finding, r models.FraudRule) error {
	return s.Transaction(func(tx *Store) error {
		var user models.User
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", f.UserID).Error; err != nil {
			return err
		}
		key := f.RuleKey + ":" + f.UserID
		var prior models.FraudFlag
		err := tx.db.Where("dedupe_key = ?", key).Order("created_at DESC,id DESC").First(&prior).Error
		if err != nil && !notFound(err) {
			return err
		}
		if err == nil && ((prior.Status == "dismissed" && prior.SuppressedUntil != nil && prior.SuppressedUntil.After(tx.now())) || prior.Status == "confirmed") {
			return nil
		}
		evidence, err := json.Marshal(f.Evidence)
		if err != nil {
			return err
		}
		related, err := json.Marshal(f.RelatedUserIDs)
		if err != nil {
			return err
		}
		flag := models.FraudFlag{ID: newID(), UserID: f.UserID, RuleKey: f.RuleKey, Type: f.RuleKey, Severity: r.Severity, Score: f.Score, Status: "open", Evidence: string(evidence), RelatedUserIDs: string(related), DedupeKey: key, CreatedAt: tx.now()}
		created := true
		if prior.ID != "" && (prior.Status == "open" || prior.Status == "reviewing") {
			flag = prior
			created = false
			if err := tx.db.Model(&flag).Updates(map[string]any{"evidence": string(evidence), "related_user_ids": string(related), "score": f.Score}).Error; err != nil {
				return err
			}
		} else if err := tx.db.Create(&flag).Error; err != nil {
			return err
		}
		for _, action := range strings.Split(r.AutoMitigation, ",") {
			if action != "" {
				if err := tx.setMitigation(flag, action, "system"); err != nil {
					return err
				}
			}
		}
		if created {
			return tx.enqueue("staff:support", "fraud_review", "", "")
		}
		return nil
	})
}
func (s *Store) setMitigation(flag models.FraudFlag, kind, actor string) error {
	m := models.FraudMitigation{ID: newID(), FlagID: flag.ID, UserID: flag.UserID, Type: kind, Active: true, CreatedAt: s.now()}
	result := s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "flag_id"}, {Name: "type"}}, DoNothing: true}).Create(&m)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		r := s.db.Model(&models.FraudMitigation{}).Where("flag_id = ? AND type = ? AND active = ?", flag.ID, kind, false).Update("active", true)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return nil
		}
	}
	return s.db.Create(&models.FraudAction{ID: newID(), FlagID: flag.ID, Action: kind, By: actor, At: s.now(), CreatedAt: s.now()}).Error
}

type FraudWork struct {
	Status  string     `json:"status"`
	Note    string     `json:"note"`
	Suspend bool       `json:"suspend"`
	Until   *time.Time `json:"until"`
}

func (s *Store) WorkFraud(id, action string, actor *models.StaffUser, in FraudWork) error {
	return s.Transaction(func(tx *Store) error {
		var flag models.FraudFlag
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&flag, "id = ?", id).Error; err != nil {
			return err
		}
		if actor.Role != models.StaffRoleAdmin && (action == "hold-payouts" || action == "release" || (action == "resolve" && (in.Status != "dismissed" || flag.Severity != "low"))) {
			return OpError(403, "this fraud action requires admin")
		}
		if flag.Status == "dismissed" || flag.Status == "confirmed" {
			if action != "note" && action != "release" {
				return OpError(409, "flag already resolved")
			}
		}
		if flag.AssignedTo != nil && *flag.AssignedTo != actor.ID && actor.Role != models.StaffRoleAdmin {
			return OpError(409, "flag held by another reviewer")
		}
		switch action {
		case "assign":
			if err := tx.db.Model(&flag).Updates(map[string]any{"assigned_to": actor.ID, "status": "reviewing"}).Error; err != nil {
				return err
			}
		case "note":
			if err := cleanNote(in.Note, 4000); err != nil {
				return err
			}
			if err := tx.db.Create(&models.FraudNote{ID: newID(), FlagID: id, StaffID: actor.ID, Note: in.Note, CreatedAt: tx.now()}).Error; err != nil {
				return err
			}
		case "hold-payouts":
			if err := tx.setMitigation(flag, "payout_hold", actor.ID); err != nil {
				return err
			}
		case "release":
			if err := tx.releaseFraudHolds(flag.ID, actor.ID); err != nil {
				return err
			}
		case "resolve":
			if err := cleanNote(in.Note, 2000); err != nil {
				return err
			}
			if in.Status != "confirmed" && in.Status != "dismissed" {
				return FieldError("status", "resolve as confirmed or dismissed")
			}
			values := map[string]any{"status": in.Status, "resolution_note": in.Note, "resolved_by": actor.ID, "resolved_at": tx.now()}
			if in.Status == "dismissed" {
				values["suppressed_until"] = tx.now().AddDate(0, 0, 30)
				if err := tx.releaseFraudHolds(id, actor.ID); err != nil {
					return err
				}
			} else if in.Suspend {
				if err := tx.ChangeAccountStatus(flag.UserID, actor, "suspended", in.Note, in.Until); err != nil {
					return err
				}
			}
			if err := tx.db.Model(&flag).Updates(values).Error; err != nil {
				return err
			}
		default:
			return OpError(404, "unknown fraud action")
		}
		return tx.CreateAuditLog(actor.ID, "fraud_"+action, "fraud_flag", id, "", map[string]any{"status": in.Status})
	})
}
func (s *Store) releaseFraudHolds(id, actor string) error {
	if err := s.db.Model(&models.FraudMitigation{}).Where("flag_id = ?", id).Update("active", false).Error; err != nil {
		return err
	}
	return s.db.Create(&models.FraudAction{ID: newID(), FlagID: id, Action: "released", By: actor, At: s.now(), CreatedAt: s.now()}).Error
}
func (s *Store) DryRunFraud(key string) (int, error) {
	rules, err := s.FraudRules()
	if err != nil {
		return 0, err
	}
	var selected *models.FraudRule
	for i := range rules {
		if rules[i].Key == key {
			selected = &rules[i]
		}
	}
	if selected == nil {
		return 0, OpError(404, "rule not found")
	}
	selected.Enabled = true
	users := []models.User{}
	if err := s.db.Where("status <> ? AND created_at <= ?", "deleted", s.now()).Find(&users).Error; err != nil {
		return 0, err
	}
	count := 0
	for _, u := range users {
		rows, err := s.evaluateFraud(context.Background(), models.FraudEvent{UserID: u.ID, Type: "dry_run"}, []models.FraudRule{*selected})
		if err != nil {
			return 0, err
		}
		if len(rows) > 0 {
			count++
		}
	}
	return count, nil
}
func (s *Store) ProcessFraud() error {
	if err := s.SeedFraudRules(); err != nil {
		return err
	}
	if err := s.db.Model(&models.FraudEvent{}).Where("status = ? AND claimed_at < ?", "processing", s.now().Add(-5*time.Minute)).Update("status", "pending").Error; err != nil {
		return err
	}
	events := []models.FraudEvent{}
	if err := s.db.Where("status = ?", "pending").Order("created_at ASC,id ASC").Limit(100).Find(&events).Error; err != nil {
		return err
	}
	for _, event := range events {
		r := s.db.Model(&models.FraudEvent{}).Where("id = ? AND status = ?", event.ID, "pending").Updates(map[string]any{"status": "processing", "claimed_at": s.now()})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			continue
		}
		if err := s.Transaction(func(tx *Store) error {
			if err := tx.EvaluateFraud(context.Background(), event); err != nil {
				return err
			}
			return tx.db.Model(&event).Update("status", "processed").Error
		}); err != nil {
			if updateErr := s.db.Model(&event).Update("status", "pending").Error; updateErr != nil {
				return updateErr
			}
			return err
		}
	}
	return s.db.Where("at < ?", s.now().AddDate(0, 0, -30)).Delete(&models.RideLocationPoint{}).Error
}
func (s *Store) QueueNightlyFraud() error {
	users := []models.User{}
	if err := s.db.Where("status <> ?", "deleted").Find(&users).Error; err != nil {
		return err
	}
	for _, u := range users {
		if err := s.QueueFraudEvent(u.ID, "nightly", nil, "nightly:"+u.ID+":"+s.now().Format("2006-01-02")); err != nil {
			return err
		}
	}
	return nil
}
