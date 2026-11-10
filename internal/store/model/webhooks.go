package model

import "time"

// Webhook is one workspace's outbound delivery target. Events holds the event
// types it subscribes to (JSON array of strings). SecretEnc is the HMAC secret
// sealed by the keyring; it is never returned on reads.
type Webhook struct {
	ID          UUID      `gorm:"column:id;primaryKey;not null"`
	WorkspaceID UUID      `gorm:"column:workspace_id;not null;index:idx_webhooks_workspace,priority:1"`
	URL         string    `gorm:"column:url;not null"`
	SecretEnc   string    `gorm:"column:secret_enc;not null"`
	Events      JSON      `gorm:"column:events;not null"`
	Enabled     bool      `gorm:"column:enabled;not null;default:true"`
	CreatedBy   UUID      `gorm:"column:created_by;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`

	Workspace    *Workspace `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	CreatedByRef *User      `gorm:"foreignKey:CreatedBy;references:ID;constraint:OnDelete:RESTRICT"`
}

func (Webhook) TableName() string { return "webhooks" }

// WebhookDelivery is one event fanned out to one webhook. It is the durable
// record the relay retries and replays from: status is pending, delivered or
// dead (retries exhausted).
type WebhookDelivery struct {
	ID            uint64     `gorm:"column:id;primaryKey;autoIncrement"`
	WebhookID     UUID       `gorm:"column:webhook_id;not null;index:idx_webhook_deliveries_webhook,priority:1;uniqueIndex:webhook_deliveries_event_uq,priority:1"`
	EventID       string     `gorm:"column:event_id;not null;size:64;uniqueIndex:webhook_deliveries_event_uq,priority:2"`
	Event         string     `gorm:"column:event;not null"`
	Payload       JSON       `gorm:"column:payload;not null"`
	Status        string     `gorm:"column:status;not null;default:pending"`
	Attempts      int        `gorm:"column:attempts;not null;default:0"`
	NextAttemptAt *time.Time `gorm:"column:next_attempt_at;index:idx_webhook_deliveries_due,priority:1"`
	LastError     *string    `gorm:"column:last_error"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`

	Webhook *Webhook `gorm:"foreignKey:WebhookID;references:ID;constraint:OnDelete:CASCADE"`
}

func (WebhookDelivery) TableName() string { return "webhook_deliveries" }
