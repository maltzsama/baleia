package events

import (
	"context"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// Outbox implements Transport over a GORM table: a writer inserts an event and
// every API replica polls the table and relays it to its own subscribers. It
// replaces the pgx LISTEN/NOTIFY hop so the store path stays GORM-only
// (ADR 0020); the poll interval is the cross-process latency.
type Outbox struct{ db *gorm.DB }

// NewOutbox builds the outbox over the store connection.
func NewOutbox(db *gorm.DB) *Outbox { return &Outbox{db: db} }

// Notify records one event for the listeners.
func (o *Outbox) Notify(ctx context.Context, channel, payload string) error {
	return o.db.WithContext(ctx).Create(&model.MonitoringEvent{
		Channel: model.Text(channel),
		Payload: payload,
	}).Error
}

// Listen polls the outbox for new events and delivers every payload until ctx
// ends. Events older than an hour are pruned; they are only wake-up signals.
func (o *Outbox) Listen(ctx context.Context, channel string, onConnected func(), deliver func(payload string)) error {
	if onConnected != nil {
		onConnected()
	}
	var last uint64
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		var rows []model.MonitoringEvent
		if err := o.db.WithContext(ctx).
			Where("channel = ? AND id > ?", channel, last).
			Order("id").
			Limit(200).
			Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			last = row.ID
			deliver(row.Payload)
		}
		_ = o.db.WithContext(ctx).
			Where("created_at < ?", time.Now().UTC().Add(-time.Hour)).
			Delete(&model.MonitoringEvent{}).Error
	}
}
