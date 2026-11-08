package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"ride-backend/models"
)

type AnnouncementInput struct {
	Audience string `json:"audience"`
	Title    string `json:"title"`
	Body     string `json:"body"`
}

type AnnouncementResult struct {
	ID            string `json:"id"`
	NotifiedCount int    `json:"notified_count"`
	PushMode      string `json:"push_mode"`
}

// Announce atomically snapshots the audience, queues inbox/push deliveries and
// records an audit receipt. Its deterministic primary key also serializes
// concurrent retries without a new table or duplicate announcements.
func (s *Store) Announce(ctx context.Context, actor *models.StaffUser, key string, input AnnouncementInput) (*AnnouncementResult, error) {
	if actor == nil || actor.Role != models.StaffRoleAdmin || !actor.IsActive {
		return nil, OpError(403, "Admin role required")
	}
	if len(key) < 8 || len(key) > 100 {
		return nil, FieldError("Idempotency-Key", "Idempotency-Key must contain 8-100 characters")
	}
	input.Title, input.Body = strings.TrimSpace(input.Title), strings.TrimSpace(input.Body)
	if input.Title == "" || len([]rune(input.Title)) > 160 || input.Body == "" || len([]rune(input.Body)) > 500 {
		return nil, FieldError("body", "title must contain 1-160 characters and body 1-500 characters")
	}
	if input.Audience != "both" && input.Audience != "passengers" && input.Audience != "drivers" {
		return nil, FieldError("audience", "audience must be both, passengers or drivers")
	}
	encoded, _ := json.Marshal(input)
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("rida/announcement/"+actor.ID+"/"+key)).String()
	result := AnnouncementResult{ID: id, PushMode: "none"}
	if s.PushSender != nil {
		result.PushMode = "fcm"
	}
	type receiptData struct {
		Hash   string             `json:"hash"`
		Result AnnouncementResult `json:"result"`
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		meta, _ := json.Marshal(receiptData{Hash: hash, Result: result})
		receipt := models.StaffAuditLog{ID: id, ActorID: actor.ID, Action: "app_announcement", TargetType: "announcement", TargetID: id, Meta: string(meta), CreatedAt: s.now()}
		insert := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt)
		if insert.Error != nil {
			return insert.Error
		}
		if insert.RowsAffected == 0 {
			if err := tx.First(&receipt, "id = ?", id).Error; err != nil {
				return err
			}
			var prior receiptData
			if err := json.Unmarshal([]byte(receipt.Meta), &prior); err != nil {
				return err
			}
			if prior.Hash != hash {
				return OpError(409, "Idempotency-Key was already used for another announcement")
			}
			result = prior.Result
			return nil
		}
		roles := []models.Role{models.RolePassenger, models.RoleDriver}
		if input.Audience == "passengers" {
			roles = []models.Role{models.RolePassenger}
		} else if input.Audience == "drivers" {
			roles = []models.Role{models.RoleDriver}
		}
		var users []string
		if err := tx.Model(&models.User{}).Where("role IN ? AND is_active = true AND status = 'active' AND deleted_at IS NULL", roles).Order("id ASC").Pluck("id", &users).Error; err != nil {
			return err
		}
		scoped := *s
		scoped.db = tx
		for _, userID := range users {
			if err := scoped.queueAppNotification(ctx, userID, "announcement", input.Title, input.Body, map[string]string{"announcement_id": id}); err != nil {
				return err
			}
		}
		result.NotifiedCount = len(users)
		meta, _ = json.Marshal(receiptData{Hash: hash, Result: result})
		return tx.Model(&receipt).Update("meta", string(meta)).Error
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}
