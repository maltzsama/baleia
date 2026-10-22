package api

import (
	"context"
	"sync"

	"github.com/galileostudio/baleia/internal/db"
)

// OpsHandlers serves monitoring, system status, attention items and the audit log (#365).
type OpsHandlers struct {
	*core
	// schema reports the applied migration version (poolSchema outside tests).
	schema schemaVersioner
	// systemStatusSchedule and systemStatusEngine are the status page's probe
	// seams (#291). Tests inject them without a Temporal server or an engine.
	systemStatusSchedule func(ctx context.Context, id string) (SystemStatusItemStatus, string)
	systemStatusEngine   func(ctx context.Context, e db.Engine) (SystemStatusItemStatus, string)
	// attentionCache caches the computed attention items for 30 s per user (#261).
	attentionCache *TTLStore[[]AttentionItem]
	attentionOnce  sync.Once
}
