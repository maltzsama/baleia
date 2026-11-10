package repo

import (
	"context"
	"encoding/json"

	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
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
		var events []string
		if json.Unmarshal([]byte(w.Events), &events) != nil {
			continue
		}
		for _, e := range events {
			if e == event || e == "*" {
				out = append(out, w)
				break
			}
		}
	}
	return out, nil
}
