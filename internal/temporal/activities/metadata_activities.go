package activities

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/galileostudio/baleia/internal/metadata"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// metadataSyncer stores a catalog's metadata snapshot; *metadata.Syncer
// implements it.
type metadataSyncer interface {
	Sync(ctx context.Context, catalogID pgtype.UUID, progress func(seen int)) (metadata.RunStats, error)
}

// SetMetadataSyncer wires the metadata sync (#15).
func (a *Activities) SetMetadataSyncer(s metadataSyncer) { a.metadata = s }

// SyncCatalogMetadataParams names the catalog to sync.
type SyncCatalogMetadataParams struct {
	CatalogID string
}

// SyncCatalogMetadataResult summarizes the pass.
type SyncCatalogMetadataResult struct {
	Path          string
	TablesSeen    int
	TablesChanged int
	TablesRemoved int
}

// metadataHeartbeatInterval is how often the sync heartbeats; a var so tests
// can shrink it.
var metadataHeartbeatInterval = 10 * time.Second

// SyncCatalogMetadata applies one full metadata pass to a catalog. It
// heartbeats the tables processed; a pass is idempotent, so it may be
// retried. A catalog that is gone or already syncing is not retried.
func SyncCatalogMetadata(ctx context.Context, params SyncCatalogMetadataParams) (*SyncCatalogMetadataResult, error) {
	return defaultActivities.syncCatalogMetadata(ctx, params)
}

func (a *Activities) syncCatalogMetadata(ctx context.Context, p SyncCatalogMetadataParams) (*SyncCatalogMetadataResult, error) {
	id, err := uuid.Parse(p.CatalogID)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError("invalid catalog id", "invalid_input", err)
	}
	if a.metadata == nil {
		return nil, temporal.NewNonRetryableApplicationError("metadata sync is not configured", "unavailable", nil)
	}
	// Heartbeat on a ticker for the whole pass: a one-query catalog walk can
	// run minutes before its first table arrives.
	var seen atomic.Int64
	progress := func(n int) { seen.Store(int64(n)) }
	done := make(chan struct{})
	beating := make(chan struct{})
	go func() {
		defer close(beating)
		t := time.NewTicker(metadataHeartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				activity.RecordHeartbeat(ctx, seen.Load())
			}
		}
	}()
	stats, err := a.metadata.Sync(ctx, pgtype.UUID{Bytes: id, Valid: true}, progress)
	close(done)
	<-beating
	switch {
	case errors.Is(err, metadata.ErrSyncInProgress):
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), "sync_in_progress", err)
	case errors.Is(err, metadata.ErrCatalogNotFound):
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), "not_found", err)
	case err != nil:
		return nil, err
	}
	activity.GetLogger(ctx).Info("metadata synced", "catalog_id", p.CatalogID, "path", stats.Path,
		"seen", stats.TablesSeen, "changed", stats.TablesChanged, "removed", stats.TablesRemoved)
	return &SyncCatalogMetadataResult{Path: stats.Path, TablesSeen: stats.TablesSeen, TablesChanged: stats.TablesChanged, TablesRemoved: stats.TablesRemoved}, nil
}
