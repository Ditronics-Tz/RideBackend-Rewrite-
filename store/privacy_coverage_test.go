package store_test

import (
	"gorm.io/gorm/schema"
	"ride-backend/db"
	"ride-backend/store"
	"sync"
	"testing"
)

func TestEveryMigratedTableHasPrivacyCoverageOrReasonedExemption(t *testing.T) {
	exported, anonymized := store.RegisteredPrivacyCoverage()
	exempt := map[string]string{
		"staff_users":       "staff identity and credentials belong to the separate staff realm; they are not passenger or driver data",
		"staff_sessions":    "staff authentication session records are security controls, not app-user data",
		"staff_audit_logs":  "security audit history is legally retained and is never deleted; new audit metadata stores IDs only",
		"referral_settings": "global campaign configuration contains no individual passenger or driver data",
	}
	cache := schema.NamingStrategy{}
	for _, model := range db.AutoMigrateModels() {
		s, err := schema.Parse(model, &sync.Map{}, cache)
		if err != nil {
			t.Fatalf("parse migration model: %v", err)
		}
		table := s.Table
		if exported[table] == "" && anonymized[table] == "" && exempt[table] == "" {
			t.Errorf("AutoMigrate table %q is missing exporter/anonymizer coverage or a written exemption", table)
		}
	}
}
