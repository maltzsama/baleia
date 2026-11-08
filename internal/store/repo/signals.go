package repo

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/ops/signals"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
)

// *Repo implements the status page and attention reads (ADR 0020, #773).
var (
	_ signals.Store = (*Repo)(nil)
)

// syncableCatalogState is the Go form of the sqlc syncable catalog predicate:
// an enabled catalog that either has a source origin or an enabled binding,
// with its last finished pass and latest pass status.
type syncableCatalogState struct {
	catalogID    string
	name         string
	createdAt    time.Time
	lastFinished *time.Time
	lastStatus   string
}

// ── status reads ──────────────────────────────────────────────────────────────

// MetadataStatusCounts counts syncable catalogs, the stale ones and the ones
// whose latest pass failed (#291).
func (r *Repo) MetadataStatusCounts(ctx context.Context, staleSeconds int32) (db.MetadataStatusCountsRow, error) {
	states, err := r.syncableCatalogStates(ctx)
	if err != nil {
		return db.MetadataStatusCountsRow{}, err
	}
	cutoff := time.Now().UTC().Add(-time.Duration(staleSeconds) * time.Second)
	out := db.MetadataStatusCountsRow{Catalogs: int64(len(states))}
	for _, st := range states {
		since := st.createdAt
		if st.lastFinished != nil {
			since = *st.lastFinished
		}
		if since.Before(cutoff) {
			out.Stale++
		}
		if st.lastStatus == "failed" {
			out.Failed++
		}
	}
	return out, nil
}

// syncableCatalogStates reads the enabled catalogs that a metadata pass syncs,
// with their last finished pass and latest pass status.
func (r *Repo) syncableCatalogStates(ctx context.Context) ([]syncableCatalogState, error) {
	var catalogs []model.Catalog
	if err := r.Users.db.WithContext(ctx).Where("enabled = ?", true).Find(&catalogs).Error; err != nil {
		return nil, err
	}
	var bindings []model.CatalogBinding
	if err := r.Users.db.WithContext(ctx).Where("enabled = ?", true).Find(&bindings).Error; err != nil {
		return nil, err
	}
	bound := map[string]bool{}
	for _, b := range bindings {
		bound[string(b.CatalogID)] = true
	}
	syncable := make([]model.Catalog, 0, len(catalogs))
	ids := make([]string, 0, len(catalogs))
	for _, c := range catalogs {
		if string(c.Origin) == "source" || bound[string(c.ID)] {
			syncable = append(syncable, c)
			ids = append(ids, string(c.ID))
		}
	}
	runsByCatalog := map[string][]model.MetadataSyncRun{}
	if len(ids) > 0 {
		var runs []model.MetadataSyncRun
		if err := r.Users.db.WithContext(ctx).
			Where("catalog_id IN ?", ids).
			Order("started_at ASC").
			Find(&runs).Error; err != nil {
			return nil, err
		}
		for _, run := range runs {
			runsByCatalog[string(run.CatalogID)] = append(runsByCatalog[string(run.CatalogID)], run)
		}
	}
	out := make([]syncableCatalogState, 0, len(syncable))
	for _, c := range syncable {
		id := string(c.ID)
		st := syncableCatalogState{catalogID: id, name: string(c.Name), createdAt: c.CreatedAt, lastStatus: "never"}
		for _, run := range runsByCatalog[id] {
			if run.Status == "finished" && run.FinishedAt != nil {
				if st.lastFinished == nil || run.FinishedAt.After(*st.lastFinished) {
					t := *run.FinishedAt
					st.lastFinished = &t
				}
			}
			st.lastStatus = run.Status
		}
		out = append(out, st)
	}
	return out, nil
}

// LatestReconcileRuns is each engine's most recent reconcile run.
func (r *Repo) LatestReconcileRuns(ctx context.Context) ([]db.LatestReconcileRunsRow, error) {
	var rows []model.CatalogReconcileRun
	if err := r.Users.db.WithContext(ctx).
		Order("engine_id, started_at DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]db.LatestReconcileRunsRow, 0)
	for _, row := range rows {
		engineID := string(row.EngineID)
		if seen[engineID] {
			continue
		}
		seen[engineID] = true
		out = append(out, db.LatestReconcileRunsRow{
			EngineID:   PGUUID(engineID),
			StartedAt:  PGTime(row.StartedAt),
			FinishedAt: PGTime(row.FinishedAt),
			DryRun:     row.DryRun,
			Error:      row.Error,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return UUIDString(out[i].EngineID) < UUIDString(out[j].EngineID)
	})
	return out, nil
}

// AuditExportLagSeconds is how far the audit export cursor is behind, zero when
// nothing has been exported yet.
func (r *Repo) AuditExportLagSeconds(ctx context.Context) (float64, error) {
	var cursor model.AuditExportCursor
	err := r.Users.db.WithContext(ctx).Where("sink = ?", "opensearch").First(&cursor).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return time.Since(cursor.OccurredAt).Seconds(), nil
}

// PluginLastConfirmation is when updatedBy last wrote the registry, invalid
// when it never did.
func (r *Repo) PluginLastConfirmation(ctx context.Context, updatedBy string) (pgtype.Timestamptz, error) {
	var row model.TrinoCatalogRegistry
	err := r.Users.db.WithContext(ctx).
		Where("updated_by = ?", updatedBy).
		Order("updated_at DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return pgtype.Timestamptz{}, nil
	}
	if err != nil {
		return pgtype.Timestamptz{}, err
	}
	return PGTime(row.UpdatedAt), nil
}

// ── attention reads ───────────────────────────────────────────────────────────

// ListAttentionBindingErrors lists bindings in error, or stuck deprovisioning
// for more than the grace period.
func (r *Repo) ListAttentionBindingErrors(ctx context.Context) ([]db.ListAttentionBindingErrorsRow, error) {
	grace := time.Now().UTC().Add(-10 * time.Minute)
	var bindings []model.CatalogBinding
	if err := r.Users.db.WithContext(ctx).Where("enabled = ?", true).Find(&bindings).Error; err != nil {
		return nil, err
	}
	catalogs, err := r.catalogNames(ctx)
	if err != nil {
		return nil, err
	}
	engines, err := r.engineNames(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]db.ListAttentionBindingErrorsRow, 0)
	for _, b := range bindings {
		catalogName, ok := catalogs[string(b.CatalogID)]
		if !ok {
			continue
		}
		state := string(b.LifecycleState)
		if state != "error" && !(state == "deprovisioning" && b.StateChangedAt.Before(grace)) {
			continue
		}
		out = append(out, db.ListAttentionBindingErrorsRow{
			ID:             PGUUID(string(b.ID)),
			CatalogID:      PGUUID(string(b.CatalogID)),
			CatalogName:    catalogName,
			EngineName:     engines[string(b.EngineID)],
			LifecycleState: state,
			StateChangedAt: PGTime(b.StateChangedAt),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StateChangedAt.Time.Before(out[j].StateChangedAt.Time)
	})
	return out, nil
}

// ListAttentionMetadataSyncFailed lists enabled catalogs whose latest pass failed.
func (r *Repo) ListAttentionMetadataSyncFailed(ctx context.Context) ([]db.ListAttentionMetadataSyncFailedRow, error) {
	var catalogs []model.Catalog
	if err := r.Users.db.WithContext(ctx).Where("enabled = ?", true).Find(&catalogs).Error; err != nil {
		return nil, err
	}
	latest, err := r.latestSyncRuns(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]db.ListAttentionMetadataSyncFailedRow, 0)
	for _, c := range catalogs {
		run, ok := latest[string(c.ID)]
		if !ok || run.Status != "failed" {
			continue
		}
		out = append(out, db.ListAttentionMetadataSyncFailedRow{
			CatalogID:   PGUUID(string(c.ID)),
			CatalogName: string(c.Name),
			FinishedAt:  PGTimePtr(run.FinishedAt),
			ErrorCode:   run.ErrorCode,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].FinishedAt, out[j].FinishedAt
		if !a.Valid {
			return false
		}
		if !b.Valid {
			return true
		}
		return a.Time.Before(b.Time)
	})
	return out, nil
}

// ListAttentionMetadataStale lists syncable catalogs whose last finished pass is
// older than secs (or that never finished one).
func (r *Repo) ListAttentionMetadataStale(ctx context.Context, secs float64) ([]db.ListAttentionMetadataStaleRow, error) {
	states, err := r.syncableCatalogStates(ctx)
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().UTC().Add(-time.Duration(secs * float64(time.Second)))
	out := make([]db.ListAttentionMetadataStaleRow, 0, len(states))
	for _, st := range states {
		since := st.createdAt
		if st.lastFinished != nil {
			since = *st.lastFinished
		}
		if !since.Before(cutoff) {
			continue
		}
		out = append(out, db.ListAttentionMetadataStaleRow{
			CatalogID: PGUUID(st.catalogID), CatalogName: st.name, Since: PGTime(since),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Since.Time.Before(out[j].Since.Time) })
	return out, nil
}

// ListAttentionRetrieverErrors lists retrievers whose last refresh failed.
func (r *Repo) ListAttentionRetrieverErrors(ctx context.Context) ([]db.ListAttentionRetrieverErrorsRow, error) {
	var retrievers []model.Retriever
	if err := r.Users.db.WithContext(ctx).Where("status = ?", "error").Find(&retrievers).Error; err != nil {
		return nil, err
	}
	workspaces, err := r.workspaceNames(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]db.ListAttentionRetrieverErrorsRow, len(retrievers))
	for i, ret := range retrievers {
		out[i] = db.ListAttentionRetrieverErrorsRow{
			ID:            PGUUID(string(ret.ID)),
			Name:          string(ret.Name),
			WorkspaceID:   PGUUID(string(ret.WorkspaceID)),
			WorkspaceName: workspaces[string(ret.WorkspaceID)],
			UpdatedAt:     PGTime(ret.UpdatedAt),
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.Time.Before(out[j].UpdatedAt.Time) })
	return out, nil
}

// ListAttentionReconcileDrift lists engines whose latest dry run reports
// mutating actions.
func (r *Repo) ListAttentionReconcileDrift(ctx context.Context) ([]db.ListAttentionReconcileDriftRow, error) {
	var runs []model.CatalogReconcileRun
	if err := r.Users.db.WithContext(ctx).
		Where("dry_run = ?", true).
		Order("engine_id, started_at DESC").
		Find(&runs).Error; err != nil {
		return nil, err
	}
	engines, err := r.engineNames(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]db.ListAttentionReconcileDriftRow, 0)
	for _, run := range runs {
		engineID := string(run.EngineID)
		if seen[engineID] {
			continue
		}
		seen[engineID] = true
		if run.Error != nil {
			continue
		}
		var actions []json.RawMessage
		if err := json.Unmarshal(run.Actions, &actions); err != nil || len(actions) == 0 {
			continue
		}
		out = append(out, db.ListAttentionReconcileDriftRow{
			EngineID:    PGUUID(engineID),
			EngineName:  engines[engineID],
			FinishedAt:  PGTime(run.FinishedAt),
			ActionCount: int32(len(actions)), //nolint:gosec // bounded by the run's action list
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].FinishedAt.Time.Before(out[j].FinishedAt.Time) })
	return out, nil
}

// GetAttentionAuditExportLag is the oldest audit event the export has not
// shipped; invalid when nothing is pending.
func (r *Repo) GetAttentionAuditExportLag(ctx context.Context) (pgtype.Timestamptz, error) {
	var cursor model.AuditExportCursor
	err := r.Users.db.WithContext(ctx).Where("sink = ?", "opensearch").First(&cursor).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return pgtype.Timestamptz{}, err
	}
	q := r.Users.db.WithContext(ctx).Model(&model.AuditEvent{})
	if err == nil {
		q = q.Where("(occurred_at > ? OR (occurred_at = ? AND id > ?))", cursor.OccurredAt, cursor.OccurredAt, cursor.ID)
	}
	var event model.AuditEvent
	if err := q.Order("occurred_at ASC, id ASC").First(&event).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pgtype.Timestamptz{}, nil
		}
		return pgtype.Timestamptz{}, err
	}
	return PGTime(event.OccurredAt), nil
}

// ── lookups ───────────────────────────────────────────────────────────────────

func (r *Repo) catalogNames(ctx context.Context) (map[string]string, error) {
	var rows []model.Catalog
	if err := r.Users.db.WithContext(ctx).Where("enabled = ?", true).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[string(row.ID)] = string(row.Name)
	}
	return out, nil
}

func (r *Repo) engineNames(ctx context.Context) (map[string]string, error) {
	var rows []model.Engine
	if err := r.Users.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[string(row.ID)] = string(row.Name)
	}
	return out, nil
}

func (r *Repo) workspaceNames(ctx context.Context) (map[string]string, error) {
	var rows []model.Workspace
	if err := r.Users.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[string(row.ID)] = string(row.Name)
	}
	return out, nil
}

func (r *Repo) latestSyncRuns(ctx context.Context) (map[string]model.MetadataSyncRun, error) {
	var runs []model.MetadataSyncRun
	if err := r.Users.db.WithContext(ctx).Order("started_at ASC").Find(&runs).Error; err != nil {
		return nil, err
	}
	out := map[string]model.MetadataSyncRun{}
	for _, run := range runs {
		out[string(run.CatalogID)] = run
	}
	return out, nil
}
