package workerapp

import (
	"context"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/monitoring/events"
	"github.com/galileostudio/baleia/internal/realtime"
)

// RelayWebhooks reads the durable webhook outbox and appends each event to the
// webhooks Stream the delivery relay drains (ADR 0022). Duplicates are
// harmless: the delivery relay dedupes by event id.
func RelayWebhooks(ctx context.Context, logger *slog.Logger, outbox *events.Outbox, bus *realtime.Client) {
	for ctx.Err() == nil {
		err := outbox.Listen(ctx, "webhook", nil, func(payload string) {
			if _, aerr := bus.StreamAdd(ctx, "webhooks", map[string]any{"event": payload}, 0); aerr != nil && logger != nil {
				logger.WarnContext(ctx, "webhook relay: stream append failed", "err", aerr)
			}
		})
		if ctx.Err() != nil {
			return
		}
		if logger != nil {
			logger.WarnContext(ctx, "webhook relay: outbox listen returned", "err", err)
		}
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return
		}
	}
}
