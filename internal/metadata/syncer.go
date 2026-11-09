package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/obs"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ErrCatalogNotFound means the catalog to sync does not exist.
var ErrCatalogNotFound = errors.New("catalog not found")

// Store is the persistence the syncer needs (ADR 0020, #774): the catalog
// lookup, the run ledger and the per-table upserts. *repo.Repo implements it.
type Store interface {
	GetCatalogForSync(ctx context.Context, id db.UUID) (db.GetCatalogForSyncRow, error)
	BeginMetadataSyncRun(ctx context.Context, catalogID db.UUID, sourcePath string, staleAfter time.Duration) (db.StartMetadataSyncRunRow, bool, error)
	TouchMetadataSyncRun(ctx context.Context, id db.UUID) error
	FinishMetadataSyncRun(ctx context.Context, arg db.FinishMetadataSyncRunParams) error
	GetTableFingerprints(ctx context.Context, catalogID db.UUID) ([]db.GetTableFingerprintsRow, error)
	UpsertMetadataNamespace(ctx context.Context, arg db.UpsertMetadataNamespaceParams) error
	UpsertMetadataTable(ctx context.Context, arg db.UpsertMetadataTableParams) (db.UpsertMetadataTableRow, error)
	TouchMetadataTable(ctx context.Context, arg db.TouchMetadataTableParams) (int64, error)
	DeleteMetadataColumns(ctx context.Context, tableID db.UUID) error
	InsertMetadataColumns(ctx context.Context, arg db.InsertMetadataColumnsParams) error
	DeleteMetadataForeignKeys(ctx context.Context, tableID db.UUID) error
	InsertMetadataForeignKeys(ctx context.Context, arg db.InsertMetadataForeignKeysParams) error
	TombstoneMissingTables(ctx context.Context, arg db.TombstoneMissingTablesParams) (int64, error)
	TombstoneMissingNamespaces(ctx context.Context, arg db.TombstoneMissingNamespacesParams) error
	PurgeMetadataTableTombstones(ctx context.Context, catalogID db.UUID) error
	PurgeMetadataNamespaceTombstones(ctx context.Context, catalogID db.UUID) error
}

// Tx runs fn in one transaction over a Store bound to it, after atomically
// checking that runID is still the running owner of the pass.
type Tx func(ctx context.Context, runID db.UUID, fn func(Store) error) error

// Opener picks a catalog's source and opens it (SourceFor in production).
type Opener interface {
	// Open returns the source path (PathProvider or PathEngine) even when it
	// fails, so the failed run records it.
	Open(ctx context.Context, c db.GetCatalogForSyncRow) (path string, src Source, err error)
}

// syncHeartbeatEvery is how often a running pass refreshes its heartbeat;
// syncRunStaleAfter is how old a heartbeat must be before the next pass takes
// the catalog over. The activity heartbeat timeout is one minute, so a
// crashed worker's retry can take over on its next attempt.
const (
	syncHeartbeatEvery = 10 * time.Second
	syncRunStaleAfter  = 60 * time.Second
)

// Syncer stores what a catalog's source reports, one full pass at a time.
type Syncer struct {
	store  Store
	tx     Tx
	opener Opener
	// heartbeatEvery and staleAfter are the lease timings; zero uses the
	// package defaults. Tests shrink them.
	heartbeatEvery time.Duration
	staleAfter     time.Duration
}

// NewSyncer returns a syncer writing through store, one table per
// transaction.
func NewSyncer(store Store, tx Tx, opener Opener) *Syncer {
	return &Syncer{store: store, tx: tx, opener: opener}
}

// heartbeatInterval is how often a running pass refreshes its lease.
func (s *Syncer) heartbeatInterval() time.Duration {
	if s.heartbeatEvery > 0 {
		return s.heartbeatEvery
	}
	return syncHeartbeatEvery
}

// leaseDuration is how old a heartbeat must be before another pass may take
// the catalog over.
func (s *Syncer) leaseDuration() time.Duration {
	if s.staleAfter > 0 {
		return s.staleAfter
	}
	return syncRunStaleAfter
}

// beginRun acquires the catalog's lease. A pass that finds a fresh lease
// waits for it to expire (a retry after a crashed worker) instead of failing
// at once; a duplicate of a live pass gives up after one lease period.
func (s *Syncer) beginRun(ctx context.Context, catalogID db.UUID, path string) (db.StartMetadataSyncRunRow, bool, error) {
	lease := s.leaseDuration()
	deadline := time.Now().Add(lease + 2*s.heartbeatInterval())
	poll := min(s.heartbeatInterval(), time.Second)
	for {
		run, started, err := s.store.BeginMetadataSyncRun(ctx, catalogID, path, lease)
		if err != nil {
			return run, false, err
		}
		if started {
			return run, true, nil
		}
		if !time.Now().Before(deadline) {
			return run, false, nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return run, false, ctx.Err()
		case <-timer.C:
		}
	}
}

// Sync applies a full pass to one catalog: it upserts the tables it sees,
// replaces the columns of the changed ones (one transaction per table),
// tombstones what it did not see, purges old tombstones and records a
// metadata_sync_runs row. The pass as a whole is not one transaction. One
// sync per catalog runs at a time; a second caller gets ErrSyncInProgress.
// progress, when set, is called after each table with the tables seen.
func (s *Syncer) Sync(ctx context.Context, catalogID db.UUID, progress func(seen int)) (out RunStats, err error) {
	ctx, span := tracer().Start(ctx, "metadata.sync",
		trace.WithAttributes(attribute.String("baleia.catalog.id", uuid.UUID(catalogID.Bytes).String())))
	defer func() {
		span.SetAttributes(
			attribute.String("baleia.metadata.path", out.Path),
			attribute.Int("baleia.tables.seen", out.TablesSeen),
			attribute.Int("baleia.tables.changed", out.TablesChanged),
			attribute.Int("baleia.tables.removed", out.TablesRemoved),
		)
		obs.FailSpan(span, err, "metadata sync failed")
		span.End()
	}()

	cat, err := s.store.GetCatalogForSync(ctx, catalogID)
	if errors.Is(err, db.ErrNoRows) {
		return RunStats{}, ErrCatalogNotFound
	}
	if err != nil {
		return RunStats{}, err
	}

	path, src, openErr := s.opener.Open(ctx, cat)
	stats := RunStats{Path: path}
	if src != nil {
		defer func() { _ = src.Close() }()
	}

	run, started, err := s.beginRun(ctx, catalogID, path)
	if errors.Is(err, db.ErrNoRows) {
		return stats, ErrCatalogNotFound
	}
	if err != nil {
		return stats, err
	}
	if !started {
		return stats, ErrSyncInProgress
	}
	// The heartbeat keeps the run alive while a slow walk or a large table is
	// being read; the next pass only takes over when it stops.
	hbCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go s.heartbeat(hbCtx, run.ID)
	finish := func(err error) (RunStats, error) {
		stopHeartbeat()
		p := db.FinishMetadataSyncRunParams{
			ID: run.ID, Status: "finished",
			TablesSeen: i32(stats.TablesSeen), TablesChanged: i32(stats.TablesChanged), TablesRemoved: i32(stats.TablesRemoved),
		}
		if err != nil {
			code, msg := errorCode(err), truncate(err.Error(), 1000)
			p.Status, p.ErrorCode, p.ErrorMessage = "failed", &code, &msg
		}
		if ferr := s.store.FinishMetadataSyncRun(context.WithoutCancel(ctx), p); ferr != nil && err == nil {
			err = ferr
		}
		return stats, err
	}
	if openErr != nil {
		return finish(openErr)
	}

	stored, err := s.store.GetTableFingerprints(ctx, catalogID)
	if err != nil {
		return finish(err)
	}
	known := make(map[string]Known, len(stored))
	for _, r := range stored {
		k := Known{Fingerprint: r.Fingerprint}
		var props map[string]string
		if json.Unmarshal(r.Properties, &props) == nil {
			k.ETag = props["etag"]
		}
		known[tableKey(r.Namespace, r.Name)] = k
	}

	w := &writer{ctx: ctx, tx: s.tx, runID: run.ID, catalogID: catalogID, typeSystem: src.TypeSystem(),
		stats: &stats, progress: progress, namespaces: map[string]bool{}}
	lookup := func(ns []string, name string) (Known, bool) {
		k, ok := known[tableKey(ns, name)]
		return k, ok
	}
	if err := src.Walk(ctx, lookup, w); err != nil {
		return finish(err)
	}

	if err := s.tx(ctx, run.ID, func(tx Store) error {
		removed, err := tx.TombstoneMissingTables(ctx, db.TombstoneMissingTablesParams{CatalogID: catalogID, PassStartedAt: run.StartedAt})
		if err != nil {
			return err
		}
		stats.TablesRemoved = int(removed)
		if err := tx.TombstoneMissingNamespaces(ctx, db.TombstoneMissingNamespacesParams{CatalogID: catalogID, PassStartedAt: run.StartedAt}); err != nil {
			return err
		}
		if err := tx.PurgeMetadataTableTombstones(ctx, catalogID); err != nil {
			return err
		}
		return tx.PurgeMetadataNamespaceTombstones(ctx, catalogID)
	}); err != nil {
		return finish(err)
	}
	return finish(nil)
}

// heartbeat refreshes the running row until ctx ends. A failure is ignored:
// the sync itself reports the store failure that matters.
func (s *Syncer) heartbeat(ctx context.Context, id db.UUID) {
	t := time.NewTicker(s.heartbeatInterval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = s.store.TouchMetadataSyncRun(ctx, id)
		}
	}
}

// writer stores what a source visits.
type writer struct {
	ctx        context.Context
	tx         Tx
	runID      db.UUID
	catalogID  db.UUID
	typeSystem string
	stats      *RunStats
	progress   func(int)
	namespaces map[string]bool
}

func (w *writer) Namespace(ns []string) error {
	key := strings.Join(ns, "\x1f")
	if w.namespaces[key] {
		return nil
	}
	w.namespaces[key] = true
	return w.tx(w.ctx, w.runID, func(tx Store) error {
		return tx.UpsertMetadataNamespace(w.ctx, db.UpsertMetadataNamespaceParams{CatalogID: w.catalogID, Namespace: nonNil(ns)})
	})
}

func (w *writer) Table(t TableSnapshot) error {
	if err := w.Namespace(t.Namespace); err != nil {
		return err
	}
	if t.Unchanged {
		var n int64
		err := w.tx(w.ctx, w.runID, func(tx Store) error {
			var err error
			n, err = tx.TouchMetadataTable(w.ctx, db.TouchMetadataTableParams{CatalogID: w.catalogID, Namespace: nonNil(t.Namespace), Name: t.Name})
			return err
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("table %v.%s reported unchanged but not stored", t.Namespace, t.Name)
		}
	} else if err := w.storeTable(t); err != nil {
		return fmt.Errorf("store table %v.%s: %w", t.Namespace, t.Name, err)
	}
	w.stats.TablesSeen++
	if w.progress != nil {
		w.progress(w.stats.TablesSeen)
	}
	return nil
}

// storeTable upserts a table and, when it changed, replaces its columns and
// foreign keys, in one transaction.
func (w *writer) storeTable(t TableSnapshot) error {
	props, err := json.Marshal(nonNilMap(t.Properties))
	if err != nil {
		return err
	}
	return w.tx(w.ctx, w.runID, func(tx Store) error {
		row, err := tx.UpsertMetadataTable(w.ctx, db.UpsertMetadataTableParams{
			CatalogID: w.catalogID, Namespace: nonNil(t.Namespace), Name: t.Name, Kind: t.Kind,
			Fingerprint: t.Fingerprint, Properties: props, Comment: optional(t.Comment),
		})
		if err != nil {
			return err
		}
		if !row.Changed {
			return nil
		}
		if err := tx.DeleteMetadataColumns(w.ctx, row.ID); err != nil {
			return err
		}
		p := db.InsertMetadataColumnsParams{TableID: row.ID, TypeSystem: w.typeSystem}
		for _, c := range t.Columns {
			p.Ordinals = append(p.Ordinals, int32(c.Ordinal))
			p.Names = append(p.Names, c.Name)
			p.NativeTypes = append(p.NativeTypes, c.NativeType)
			p.Nullables = append(p.Nullables, c.Nullable)
			p.Comments = append(p.Comments, c.Comment)
		}
		if len(t.Columns) > 0 {
			if err := tx.InsertMetadataColumns(w.ctx, p); err != nil {
				return err
			}
		}
		if err := tx.DeleteMetadataForeignKeys(w.ctx, row.ID); err != nil {
			return err
		}
		if len(t.ForeignKeys) > 0 {
			keys, err := json.Marshal(t.ForeignKeys)
			if err != nil {
				return err
			}
			if err := tx.InsertMetadataForeignKeys(w.ctx, db.InsertMetadataForeignKeysParams{
				CatalogID: w.catalogID, TableID: row.ID, ForeignKeys: keys,
			}); err != nil {
				return err
			}
		}
		w.stats.TablesChanged++
		return nil
	})
}

func tableKey(ns []string, name string) string {
	return strings.Join(ns, "\x1f") + "\x00" + name
}

// errorCode is the bounded reason recorded on a failed run.
func errorCode(err error) string {
	var qe *queryengine.Error
	switch {
	case errors.As(err, &qe):
		return string(qe.Code)
	case errors.Is(err, connections.ErrNotFound):
		return "not_found"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	return "sync_failed"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Cut on a rune boundary: PostgreSQL rejects invalid UTF-8 in TEXT.
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func i32(n int) *int32 {
	v := int32(n)
	return &v
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nonNil keeps a root namespace an empty array, never NULL.
func nonNil(ns []string) []string {
	if ns == nil {
		return []string{}
	}
	return ns
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// tracer names the metadata sync spans (#380).
func tracer() trace.Tracer { return obs.Tracer("metadata") }
