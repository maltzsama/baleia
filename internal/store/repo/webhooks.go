package repo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Webhooks persists workspace webhook registrations and their deliveries.
type Webhooks struct{ db *gorm.DB }

// NewWebhooks builds the webhook store.
func NewWebhooks(db *gorm.DB) *Webhooks { return &Webhooks{db: db} }

// Create inserts a webhook and returns the stored row.
func (r *Webhooks) Create(ctx context.Context, w model.Webhook) (model.Webhook, error) {
	if err := r.db.WithContext(ctx).Create(&w).Error; err != nil {
		return model.Webhook{}, err
	}
	return w, nil
}

// List returns a workspace's webhooks, oldest first.
func (r *Webhooks) List(ctx context.Context, workspaceID string) ([]model.Webhook, error) {
	var rows []model.Webhook
	err := r.db.WithContext(ctx).
		Where("workspace_id = ?", workspaceID).
		Order("created_at").
		Find(&rows).Error
	return rows, err
}

// Delete removes one workspace webhook. It reports whether a row matched.
func (r *Webhooks) Delete(ctx context.Context, workspaceID, id string) (bool, error) {
	res := r.db.WithContext(ctx).
		Where("id = ? AND workspace_id = ?", id, workspaceID).
		Delete(&model.Webhook{})
	return res.RowsAffected > 0, res.Error
}

// Match returns the enabled webhooks of a workspace subscribed to event ("*"
// matches every event). The relay calls it when it fans an event out.
func (r *Webhooks) Match(ctx context.Context, workspaceID, event string) ([]model.Webhook, error) {
	var rows []model.Webhook
	if err := r.db.WithContext(ctx).
		Where("workspace_id = ? AND enabled = ?", workspaceID, true).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.Webhook, 0, len(rows))
	for _, w := range rows {
		if subscribes(w, event) {
			out = append(out, w)
		}
	}
	return out, nil
}

// MatchEvent returns the enabled webhooks of every workspace subscribed to
// event. It is for events that are not workspace-scoped (a catalog sync).
func (r *Webhooks) MatchEvent(ctx context.Context, event string) ([]model.Webhook, error) {
	var rows []model.Webhook
	if err := r.db.WithContext(ctx).Where("enabled = ?", true).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.Webhook, 0, len(rows))
	for _, w := range rows {
		if subscribes(w, event) {
			out = append(out, w)
		}
	}
	return out, nil
}

func subscribes(w model.Webhook, event string) bool {
	var events []string
	if json.Unmarshal([]byte(w.Events), &events) != nil {
		return false
	}
	for _, e := range events {
		if e == event || e == "*" {
			return true
		}
	}
	return false
}

// EnqueueDelivery inserts a delivery unless (webhook_id, event_id) already
// exists. It reports whether it created one.
func (r *Webhooks) EnqueueDelivery(ctx context.Context, d model.WebhookDelivery) (bool, error) {
	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&d)
	return res.RowsAffected > 0, res.Error
}

// DueDeliveries returns the pending deliveries due now, oldest first, with
// their webhook loaded.
func (r *Webhooks) DueDeliveries(ctx context.Context, limit int) ([]model.WebhookDelivery, error) {
	var rows []model.WebhookDelivery
	err := r.db.WithContext(ctx).
		Preload("Webhook").
		Where("status = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", "pending", time.Now().UTC()).
		Order("id").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

// ClaimDueDeliveries atomically leases the pending deliveries due now, so two
// relays never send the same one. SKIP LOCKED lets concurrent relays take
// different rows; the lease is overwritten on success or on the next retry.
func (r *Webhooks) ClaimDueDeliveries(ctx context.Context, limit int, lease time.Duration) ([]model.WebhookDelivery, error) {
	var rows []model.WebhookDelivery
	now := time.Now().UTC()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Preload("Webhook").
			Where("status = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", "pending", now).
			Order("id").
			Limit(limit).
			Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]uint64, len(rows))
		for i, d := range rows {
			ids[i] = d.ID
		}
		return tx.Model(&model.WebhookDelivery{}).Where("id IN ?", ids).
			Update("next_attempt_at", now.Add(lease)).Error
	})
	return rows, err
}

// MarkDelivered records a successful delivery.
func (r *Webhooks) MarkDelivered(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Model(&model.WebhookDelivery{}).Where("id = ?", id).
		Updates(map[string]any{"status": "delivered", "last_error": nil}).Error
}

// MarkFailed records a failed attempt. A nil next marks the delivery dead.
func (r *Webhooks) MarkFailed(ctx context.Context, id uint64, attempts int, next *time.Time, lastError string) error {
	status := "pending"
	if next == nil {
		status = "dead"
	}
	return r.db.WithContext(ctx).Model(&model.WebhookDelivery{}).Where("id = ?", id).
		Updates(map[string]any{
			"status": status, "attempts": attempts, "next_attempt_at": next, "last_error": lastError,
		}).Error
}
