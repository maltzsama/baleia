package activities

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/metadata"
	"github.com/galileostudio/baleia/internal/queryengine"
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
	start := time.Now()
	stats, err := a.metadata.Sync(ctx, pgtype.UUID{Bytes: id, Valid: true}, progress)
	close(done)
	<-beating
	if !errors.Is(err, metadata.ErrSyncInProgress) {
		a.metrics.metadata.Synced(time.Since(start), err)
	}
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

// engineObserver records what a federated engine reports and adopts the
// catalogs Baleia did not create (bindings.Lifecycle.Observe); it is wired in
// cmd/worker, where the engine's binder is built.
type engineObserver func(ctx context.Context, eng db.Engine, clusterID pgtype.UUID) error

// SetEngineObserver wires the federated engines' catalog observation.
func (a *Activities) SetEngineObserver(o engineObserver) { a.observeEngine = o }

// engineRefreshTimeout bounds the discovery of one engine's catalogs.
const engineRefreshTimeout = 30 * time.Second

// RefreshEngineCatalogsResult counts the engines refreshed.
type RefreshEngineCatalogsResult struct {
	Engines int
	Failed  int
}

// RefreshEngineCatalogs is step 0 of the metadata dispatcher (#17): it
// refreshes the native catalogs of every enabled direct engine (new
// databases appear, missing ones are disabled, never deleted) and observes
// every federated engine (catalogs found in Trino are adopted). One
// unreachable engine never stops the others.
func RefreshEngineCatalogs(ctx context.Context) (*RefreshEngineCatalogsResult, error) {
	return defaultActivities.refreshEngineCatalogs(ctx)
}

func (a *Activities) refreshEngineCatalogs(ctx context.Context) (*RefreshEngineCatalogsResult, error) {
	rows, err := a.queries.ListEnabledEnginesWithConnection(ctx)
	if err != nil {
		return nil, err
	}
	logger := activity.GetLogger(ctx)
	res := &RefreshEngineCatalogsResult{}
	for i, r := range rows {
		ectx, cancel := context.WithTimeout(ctx, engineRefreshTimeout)
		switch r.Engine.Class {
		case string(queryengine.ClassDirect):
			err = a.refreshNativeCatalogs(ectx, r)
		case string(queryengine.ClassFederated):
			err = nil
			if a.observeEngine != nil && r.ClusterID.Valid {
				err = a.observeEngine(ectx, r.Engine, r.ClusterID)
			}
		}
		cancel()
		res.Engines++
		if err != nil {
			res.Failed++
			logger.Warn("refresh engine catalogs", "engine", r.Engine.Name, "err", err)
		}
		activity.RecordHeartbeat(ctx, i+1)
	}
	return res, nil
}

// refreshNativeCatalogs upserts the catalogs a direct engine exposes and
// disables the native bindings of those it no longer does.
func (a *Activities) refreshNativeCatalogs(ctx context.Context, r db.ListEnabledEnginesWithConnectionRow) error {
	conn := r.CatalogConnection
	kind := connections.Kind(conn.Type)
	p, err := a.connections.Get(kind)
	if err != nil {
		return err
	}
	plain, err := catalogs.OpenCredentials(conn, a.keyring)
	if err != nil {
		return err
	}
	cfg, err := providers.FromStored(a.connections, kind, conn.ID, conn.Url, conn.Config, plain)
	if err != nil {
		return err
	}
	names, err := catalogs.NativeNames(ctx, p, cfg, conn.Name)
	if err != nil {
		return err
	}
	for _, name := range names {
		cat, err := a.queries.UpsertNativeCatalog(ctx, db.UpsertNativeCatalogParams{ConnectionID: conn.ID, Name: name})
		if err != nil {
			return err
		}
		if _, err := a.queries.UpsertNativeBinding(ctx, db.UpsertNativeBindingParams{CatalogID: cat.ID, EngineID: r.Engine.ID, EngineCatalogName: name}); err != nil {
			return err
		}
	}
	_, err = a.queries.DisableMissingNativeBindings(ctx, db.DisableMissingNativeBindingsParams{EngineID: r.Engine.ID, Present: names})
	return err
}

// ListSyncableCatalogs lists the catalogs the scheduled sync refreshes.
func ListSyncableCatalogs(ctx context.Context) ([]string, error) {
	return defaultActivities.listSyncableCatalogs(ctx)
}

func (a *Activities) listSyncableCatalogs(ctx context.Context) ([]string, error) {
	ids, err := a.queries.ListSyncableCatalogs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = uuid.UUID(id.Bytes).String()
	}
	return out, nil
}
