package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/obs"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ErrCatalogNotFound means the catalog to sync does not exist.
var ErrCatalogNotFound = errors.New("catalog not found")

// Opener picks a catalog's source and opens it (SourceFor in production).
type Opener interface {
	// Open returns the source path (PathProvider or PathEngine) even when it
	// fails, so the failed run records it.
	Open(ctx context.Context, c db.GetCatalogForSyncRow) (path string, src Source, err error)
}

// Syncer stores what a catalog's source reports, one full pass at a time.
type Syncer struct {
	pool   *pgxpool.Pool
	opener Opener
}

// NewSyncer returns a syncer writing through pool.
func NewSyncer(pool *pgxpool.Pool, opener Opener) *Syncer {
	return &Syncer{pool: pool, opener: opener}
}

// Sync applies a full pass to one catalog: it upserts the tables it sees,
// replaces the columns of the changed ones (one transaction per table),
// tombstones what it did not see, purges old tombstones and records a
// metadata_sync_runs row. The pass as a whole is not one transaction. One
// sync per catalog runs at a time; a second caller gets ErrSyncInProgress.
// progress, when set, is called after each table with the tables seen.
func (s *Syncer) Sync(ctx context.Context, catalogID pgtype.UUID, progress func(seen int)) (out RunStats, err error) {
	ctx, span := tracer.Start(ctx, "metadata.sync",
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
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return RunStats{}, err
	}
	defer conn.Release()
	q := db.New(conn)

	lockKey := uuid.UUID(catalogID.Bytes).String()
	locked, err := q.TryMetadataSyncLock(ctx, lockKey)
	if err != nil {
		return RunStats{}, err
	}
	if !locked {
		return RunStats{}, ErrSyncInProgress
	}
	defer func() { _ = q.ReleaseMetadataSyncLock(context.WithoutCancel(ctx), lockKey) }()

	cat, err := q.GetCatalogForSync(ctx, catalogID)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunStats{}, ErrCatalogNotFound
	}
	if err != nil {
		return RunStats{}, err
	}
	// A run a crashed worker left running ends now: this pass holds the lock.
	if err := q.FailStaleMetadataSyncRuns(ctx, catalogID); err != nil {
		return RunStats{}, err
	}

	path, src, openErr := s.opener.Open(ctx, cat)
	stats := RunStats{Path: path}
	run, err := q.StartMetadataSyncRun(ctx, db.StartMetadataSyncRunParams{CatalogID: catalogID, SourcePath: path})
	if err != nil {
		if src != nil {
			_ = src.Close()
		}
		return stats, err
	}
	finish := func(err error) (RunStats, error) {
		p := db.FinishMetadataSyncRunParams{
			ID: run.ID, Status: "finished",
			TablesSeen: i32(stats.TablesSeen), TablesChanged: i32(stats.TablesChanged), TablesRemoved: i32(stats.TablesRemoved),
		}
		if err != nil {
			code, msg := errorCode(err), truncate(err.Error(), 1000)
			p.Status, p.ErrorCode, p.ErrorMessage = "failed", &code, &msg
		}
		if ferr := q.FinishMetadataSyncRun(context.WithoutCancel(ctx), p); ferr != nil && err == nil {
			err = ferr
		}
		return stats, err
	}
	if openErr != nil {
		return finish(openErr)
	}
	defer func() { _ = src.Close() }()

	stored, err := q.GetTableFingerprints(ctx, catalogID)
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

	w := &writer{ctx: ctx, conn: conn, q: q, catalogID: catalogID, typeSystem: src.TypeSystem(),
		stats: &stats, progress: progress, namespaces: map[string]bool{}}
	lookup := func(ns []string, name string) (Known, bool) {
		k, ok := known[tableKey(ns, name)]
		return k, ok
	}
	if err := src.Walk(ctx, lookup, w); err != nil {
		return finish(err)
	}

	removed, err := q.TombstoneMissingTables(ctx, db.TombstoneMissingTablesParams{CatalogID: catalogID, PassStartedAt: run.StartedAt})
	if err != nil {
		return finish(err)
	}
	stats.TablesRemoved = int(removed)
	if err := q.TombstoneMissingNamespaces(ctx, db.TombstoneMissingNamespacesParams{CatalogID: catalogID, PassStartedAt: run.StartedAt}); err != nil {
		return finish(err)
	}
	if err := q.PurgeMetadataTableTombstones(ctx, catalogID); err != nil {
		return finish(err)
	}
	if err := q.PurgeMetadataNamespaceTombstones(ctx, catalogID); err != nil {
		return finish(err)
	}
	return finish(nil)
}

// writer stores what a source visits, on the connection holding the lock.
type writer struct {
	ctx        context.Context
	conn       *pgxpool.Conn
	q          *db.Queries
	catalogID  pgtype.UUID
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
	return w.q.UpsertMetadataNamespace(w.ctx, db.UpsertMetadataNamespaceParams{CatalogID: w.catalogID, Namespace: nonNil(ns)})
}

func (w *writer) Table(t TableSnapshot) error {
	if err := w.Namespace(t.Namespace); err != nil {
		return err
	}
	if t.Unchanged {
		n, err := w.q.TouchMetadataTable(w.ctx, db.TouchMetadataTableParams{CatalogID: w.catalogID, Namespace: nonNil(t.Namespace), Name: t.Name})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("table %v.%s reported unchanged but not stored", t.Namespace, t.Name)
		}
	} else if err := w.store(t); err != nil {
		return fmt.Errorf("store table %v.%s: %w", t.Namespace, t.Name, err)
	}
	w.stats.TablesSeen++
	if w.progress != nil {
		w.progress(w.stats.TablesSeen)
	}
	return nil
}

// store upserts a table and, when it changed, replaces its columns, in one
// transaction.
func (w *writer) store(t TableSnapshot) error {
	props, err := json.Marshal(nonNilMap(t.Properties))
	if err != nil {
		return err
	}
	tx, err := w.conn.Begin(w.ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(w.ctx)) }()
	qtx := w.q.WithTx(tx)
	row, err := qtx.UpsertMetadataTable(w.ctx, db.UpsertMetadataTableParams{
		CatalogID: w.catalogID, Namespace: nonNil(t.Namespace), Name: t.Name, Kind: t.Kind,
		Fingerprint: t.Fingerprint, Properties: props, Comment: optional(t.Comment),
	})
	if err != nil {
		return err
	}
	if row.Changed {
		if err := qtx.DeleteMetadataColumns(w.ctx, row.ID); err != nil {
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
			if err := qtx.InsertMetadataColumns(w.ctx, p); err != nil {
				return err
			}
		}
		w.stats.TablesChanged++
	}
	return tx.Commit(w.ctx)
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
var tracer = obs.Tracer("metadata")
