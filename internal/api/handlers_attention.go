package api

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/semantic"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// Attention endpoint (#261).
//
// Items are computed on demand from the current state; nothing is persisted
// (no read/unread, no e-mail). Each kind is one query, and every candidate is
// filtered through the same authz predicate that guards the fix, so a user
// only sees resources they can act on. Admin-only kinds never reach a member.

const (
	attentionCacheTTL            = 30 * time.Second
	attentionExportLagThreshold  = 15 * time.Minute
	attentionStaleMultiplier     = 3
	attentionScheduleProbeBudget = 2 * time.Second
)

func (h *OpsHandlers) ListAttention(ctx context.Context, _ ListAttentionRequestObject) (ListAttentionResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListAttention401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{
			Code: ErrorCodeUnauthorized, Message: "invalid user",
		}}, nil
	}

	cache := h.attentionCacheStore()
	key := p.UserID.String()
	if items, ok := cache.Get(key); ok {
		return ListAttention200JSONResponse(items), nil
	}

	items, err := h.attentionItems(ctx, p)
	if err != nil {
		return nil, err
	}
	cache.Set(key, items)
	return ListAttention200JSONResponse(items), nil
}

// attentionCacheStore lazily builds the per-user cache. Tests may replace it
// with a shorter TTL before the first call.
func (h *OpsHandlers) attentionCacheStore() cacheStore[[]AttentionItem] {
	h.attentionOnce.Do(func() {
		if h.attentionCache == nil {
			h.attentionCache = newCache[[]AttentionItem](h.rt, "attention", attentionCacheTTL)
		}
	})
	return h.attentionCache
}

func (h *OpsHandlers) attentionItems(ctx context.Context, p authz.Principal) ([]AttentionItem, error) {
	items := make([]AttentionItem, 0)

	items = append(items, h.attentionBindingErrors(ctx, p)...)
	items = append(items, h.attentionMetadataFailures(ctx, p)...)
	items = append(items, h.attentionMetadataStale(ctx, p)...)
	items = append(items, h.attentionEngineUnreachable(ctx, p)...)
	items = append(items, h.attentionRetrieverErrors(ctx, p)...)
	items = append(items, h.attentionLineageBroken(ctx, p)...)

	if p.IsAdmin() {
		items = append(items, h.attentionReconcileDrift(ctx)...)
		items = append(items, h.attentionSchedules(ctx)...)
		items = append(items, h.attentionAuditExport(ctx)...)
	}

	// Errors first, then oldest first inside each severity.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Severity != items[j].Severity {
			return items[i].Severity == AttentionSeverityError
		}
		return items[i].Since.Before(items[j].Since)
	})
	return items, nil
}

func (h *OpsHandlers) attentionBindingErrors(ctx context.Context, p authz.Principal) []AttentionItem {
	rows, err := h.signals.BindingErrors(ctx)
	if err != nil {
		return nil
	}
	out := make([]AttentionItem, 0, len(rows))
	for _, r := range rows {
		if _, _, err := h.authz().Catalog(ctx, p, r.CatalogID, authz.ActWrite); err != nil {
			continue
		}
		out = append(out, AttentionItem{
			Kind:     BindingError,
			Severity: AttentionSeverityError,
			Resource: AttentionResource{
				Type: AttentionResourceTypeBinding,
				Id:   idString(r.ID),
				Name: r.CatalogName,
			},
			Params: &map[string]string{
				"catalog": r.CatalogName,
				"engine":  r.EngineName,
				"state":   r.LifecycleState,
			},
			Since: r.StateChangedAt.Time,
			Href:  ptrString("/connections?catalog=" + idString(r.CatalogID)),
		})
	}
	return out
}

func (h *OpsHandlers) attentionMetadataFailures(ctx context.Context, p authz.Principal) []AttentionItem {
	rows, err := h.signals.MetadataFailures(ctx)
	if err != nil {
		return nil
	}
	out := make([]AttentionItem, 0, len(rows))
	for _, r := range rows {
		if _, _, err := h.authz().Catalog(ctx, p, r.CatalogID, authz.ActWrite); err != nil {
			continue
		}
		params := map[string]string{"catalog": r.CatalogName}
		if r.ErrorCode != nil {
			params["error_code"] = *r.ErrorCode
		}
		since := time.Now()
		if r.FinishedAt.Valid {
			since = r.FinishedAt.Time
		}
		out = append(out, AttentionItem{
			Kind:     MetadataSyncFailed,
			Severity: AttentionSeverityError,
			Resource: AttentionResource{
				Type: AttentionResourceTypeCatalog,
				Id:   idString(r.CatalogID),
				Name: r.CatalogName,
			},
			Params: &params,
			Since:  since,
			Href:   ptrString("/connections?catalog=" + idString(r.CatalogID)),
		})
	}
	return out
}

func (h *OpsHandlers) attentionMetadataStale(ctx context.Context, p authz.Principal) []AttentionItem {
	threshold := attentionStaleMultiplier * h.metadataSyncEvery()
	rows, err := h.signals.MetadataStale(ctx, threshold.Seconds())
	if err != nil {
		return nil
	}
	out := make([]AttentionItem, 0, len(rows))
	for _, r := range rows {
		if _, _, err := h.authz().Catalog(ctx, p, r.CatalogID, authz.ActWrite); err != nil {
			continue
		}
		out = append(out, AttentionItem{
			Kind:     MetadataStale,
			Severity: AttentionSeverityWarning,
			Resource: AttentionResource{
				Type: AttentionResourceTypeCatalog,
				Id:   idString(r.CatalogID),
				Name: r.CatalogName,
			},
			Params: &map[string]string{"catalog": r.CatalogName},
			Since:  r.Since.Time,
			Href:   ptrString("/connections?catalog=" + idString(r.CatalogID)),
		})
	}
	return out
}

func (h *OpsHandlers) attentionEngineUnreachable(ctx context.Context, p authz.Principal) []AttentionItem {
	if h.engines == nil {
		return nil
	}
	engines, err := h.signals.Engines(ctx, p.UserID, p.IsAdmin())
	if err != nil {
		return nil
	}
	writable := make([]db.Engine, 0, len(engines))
	for _, e := range engines {
		if _, _, err := h.authz().Engine(ctx, p, e.ID, authz.ActWrite); err != nil {
			continue
		}
		writable = append(writable, e)
	}
	out := make([]AttentionItem, 0, len(writable))
	for _, e := range h.describeEngines(ctx, writable) {
		if e.Health == nil || e.Health.Status != "unreachable" {
			continue
		}
		params := map[string]string{}
		if e.Health.ErrorCode != nil {
			params["error_code"] = *e.Health.ErrorCode
		}
		since := time.Now()
		if e.Health.CheckedAt != nil {
			since = *e.Health.CheckedAt
		}
		out = append(out, AttentionItem{
			Kind:     EngineUnreachable,
			Severity: AttentionSeverityError,
			Resource: AttentionResource{
				Type: AttentionResourceTypeEngine,
				Id:   e.Id.String(),
				Name: e.Name,
			},
			Params: &params,
			Since:  since,
			Href:   ptrString("/engines?engine=" + e.Id.String()),
		})
	}
	return out
}

func (h *OpsHandlers) attentionRetrieverErrors(ctx context.Context, p authz.Principal) []AttentionItem {
	rows, err := h.signals.RetrieverErrors(ctx)
	if err != nil {
		return nil
	}
	out := make([]AttentionItem, 0, len(rows))
	for _, r := range rows {
		if _, err := h.authz().Workspace(ctx, p, r.WorkspaceID, authz.ActWrite); err != nil {
			continue
		}
		out = append(out, AttentionItem{
			Kind:     RetrieverError,
			Severity: AttentionSeverityError,
			Resource: AttentionResource{
				Type: AttentionResourceTypeRetriever,
				Id:   idString(r.ID),
				Name: r.Name,
			},
			Params: &map[string]string{"workspace": r.WorkspaceName},
			Since:  r.UpdatedAt.Time,
			Href:   ptrString("/retrievers?retriever=" + idString(r.ID)),
		})
	}
	return out
}

func (h *OpsHandlers) attentionReconcileDrift(ctx context.Context) []AttentionItem {
	rows, err := h.signals.ReconcileDrift(ctx)
	if err != nil {
		return nil
	}
	out := make([]AttentionItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, AttentionItem{
			Kind:     ReconcileDrift,
			Severity: AttentionSeverityWarning,
			Resource: AttentionResource{
				Type: AttentionResourceTypeEngine,
				Id:   idString(r.EngineID),
				Name: r.EngineName,
			},
			Params: &map[string]string{
				"engine":  r.EngineName,
				"actions": strconv.Itoa(int(r.ActionCount)),
			},
			Since: r.FinishedAt.Time,
			Href:  ptrString("/engines?engine=" + idString(r.EngineID) + "&tab=reconcile"),
		})
	}
	return out
}

// attentionSchedules reports a spec schedule that is missing or paused. It is
// admin-only and a no-op when the API has no Temporal client (tests) or the
// worker does not ensure schedules.
func (h *core) attentionSchedules(ctx context.Context) []AttentionItem {
	if h.temporalClient == nil || !h.settings.Ops.SchedulesEnabled {
		return nil
	}
	out := make([]AttentionItem, 0)
	for _, id := range h.expectedScheduleIDs() {
		probeCtx, cancel := context.WithTimeout(ctx, attentionScheduleProbeBudget)
		desc, err := h.temporalClient.ScheduleClient().GetHandle(probeCtx, id).Describe(probeCtx)
		cancel()
		if err != nil {
			var notFound *serviceerror.NotFound
			if errors.As(err, &notFound) {
				out = append(out, attentionScheduleItem(id, "missing"))
			}
			continue
		}
		if desc.Schedule.State.Paused {
			out = append(out, attentionScheduleItem(id, "paused"))
			continue
		}
		// A schedule that has not acted in a while is stale: the worker that
		// would run its workflows is down, or the pass stopped working (#750).
		if item := scheduleStaleItem(id, desc); item != nil {
			out = append(out, *item)
		}
	}
	return out
}

// scheduleStaleItem returns an attention item when a schedule's most recent
// action is older than 3 × its cadence; nil when the schedule has no known
// cadence or has acted recently.
func scheduleStaleItem(id string, desc *client.ScheduleDescription) *AttentionItem {
	cadence, ok := scheduleCadence(id)
	if !ok {
		return nil
	}
	// Reference the most recent action start; when none has run yet, fall back
	// to the schedule's creation time so a never-run schedule is still stale.
	var last time.Time
	if n := len(desc.Info.RecentActions); n > 0 {
		last = desc.Info.RecentActions[n-1].ActualTime
	} else if !desc.Info.CreatedAt.IsZero() {
		last = desc.Info.CreatedAt
	} else {
		return nil
	}
	if time.Since(last) < attentionStaleMultiplier*cadence {
		return nil
	}
	item := attentionScheduleItem(id, "stale")
	return &item
}

// scheduleCadence is the cadence of schedules the attention loop reports
// staleness for; false when the schedule's cadence is not known here.
func scheduleCadence(id string) (time.Duration, bool) {
	switch id {
	case schedules.LensTableHealth:
		return time.Hour, true
	default:
		return 0, false
	}
}

func attentionScheduleItem(id, state string) AttentionItem {
	return AttentionItem{
		Kind:     ScheduleMissing,
		Severity: AttentionSeverityError,
		Resource: AttentionResource{
			Type: AttentionResourceTypeSchedule,
			Id:   id,
			Name: id,
		},
		Params: &map[string]string{"schedule": id, "state": state},
		Since:  time.Now(),
		Href:   ptrString("/settings/system"),
	}
}

// expectedScheduleIDs mirrors the set cmd/worker/main.go ensures. #291 will
// centralize the spec list; until then the IDs are the shared source of truth.
func (h *core) expectedScheduleIDs() []string {
	ids := []string{
		schedules.LensQueryPoller,
		schedules.LensClusterPoller,
		schedules.LensTableHealth,
		schedules.RetrieverScheduler,
		schedules.MetadataSync,
		schedules.AuditPartitions,
		schedules.EphemeralCatalogSweeper,
		schedules.TokenCleanup,
	}
	if h.settings.Ops.ReconcileInterval > 0 {
		ids = append(ids, schedules.CatalogReconcile)
	}
	if h.settings.Ops.AuditExportURL != "" {
		ids = append(ids, schedules.AuditExport)
	}
	return ids
}

func (h *OpsHandlers) attentionAuditExport(ctx context.Context) []AttentionItem {
	if h.settings.Ops.AuditExportURL == "" {
		return nil
	}
	since, err := h.signals.AuditExportSince(ctx)
	if err != nil || !since.Valid {
		return nil
	}
	lag := time.Since(since.Time)
	if lag < attentionExportLagThreshold {
		return nil
	}
	return []AttentionItem{{
		Kind:     AuditExportLagging,
		Severity: AttentionSeverityWarning,
		Resource: AttentionResource{
			Type: AttentionResourceTypeAuditExport,
			Id:   "opensearch",
			Name: "OpenSearch audit export",
		},
		Params: &map[string]string{"lag_seconds": strconv.Itoa(int(lag.Seconds()))},
		Since:  since.Time,
		Href:   ptrString("/settings/system"),
	}}
}

// ptrString returns a pointer to s; the generated models use *string for
// optional fields.
func ptrString(s string) *string { return &s }

// attentionLineageBroken reports a model or metric whose base table, or a
// column a model references, no longer exists after a metadata sync (B4, #558).
// It is computed on demand from the current state, like every other attention
// item, and only shown to someone who can edit the workspace.
func (h *OpsHandlers) attentionLineageBroken(ctx context.Context, p authz.Principal) []AttentionItem {
	var items []AttentionItem

	models, err := h.lineage.ListLineageBrokenModels(ctx)
	if err != nil {
		return items
	}
	for _, m := range models {
		if _, err := h.authz().Workspace(ctx, p, m.WorkspaceID, authz.ActWrite); err != nil {
			continue
		}
		missing, broken := brokenModelReference(ctx, h.lineage, m)
		if !broken {
			continue
		}
		params := map[string]string{"model": m.Slug, "missing": missing}
		items = append(items, AttentionItem{
			Kind:     LineageBroken,
			Severity: AttentionSeverityWarning,
			Resource: AttentionResource{
				Type: AttentionResourceTypeModel,
				Id:   idString(m.ID),
				Name: m.Label,
			},
			Params: &params,
			Since:  m.UpdatedAt.Time,
			Href:   ptrString("/models/" + idString(m.ID)),
		})
	}

	metrics, err := h.lineage.ListLineageBrokenMetrics(ctx)
	if err != nil {
		return items
	}
	for _, mt := range metrics {
		if _, err := h.authz().Workspace(ctx, p, mt.WorkspaceID, authz.ActWrite); err != nil {
			continue
		}
		if tableExists(ctx, h.lineage, mt.CatalogID, mt.Namespace, mt.TableName) {
			continue
		}
		params := map[string]string{"metric": mt.Slug, "missing": mt.TableName}
		items = append(items, AttentionItem{
			Kind:     LineageBroken,
			Severity: AttentionSeverityWarning,
			Resource: AttentionResource{
				Type: AttentionResourceTypeMetric,
				Id:   idString(mt.ID),
				Name: mt.Label,
			},
			Params: &params,
			Since:  mt.UpdatedAt.Time,
			Href:   ptrString("/metrics/" + idString(mt.ID)),
		})
	}
	return items
}

// brokenModelReference reports whether a model's base table or one of its
// referenced columns no longer exists. missing names what is gone.
func brokenModelReference(ctx context.Context, q lineageReads, m db.ListLineageBrokenModelsRow) (missing string, broken bool) {
	var def semantic.Model
	if err := json.Unmarshal(m.Definition, &def); err != nil {
		return "", false
	}
	if def.Source.Kind != semantic.SourceTable {
		return "", false // a SQL source is best-effort and never flagged here
	}
	table, err := q.GetMetadataTableByRef(ctx, db.GetMetadataTableByRefParams{
		CatalogID: m.CatalogID, Namespace: def.Source.Namespace, Name: def.Source.Table,
	})
	if errors.Is(err, db.ErrNoRows) {
		return def.Source.Table, true
	}
	if err != nil {
		return "", false
	}
	cols, err := q.ListMetadataColumns(ctx, table.ID)
	if err != nil {
		return "", false
	}
	existing := make(map[string]bool, len(cols))
	for _, c := range cols {
		existing[c.Name] = true
	}
	for _, col := range modelReferencedColumns(def) {
		if !existing[col] {
			return col, true
		}
	}
	return "", false
}

// tableExists reports whether a live table row matches the natural key.
func tableExists(ctx context.Context, q lineageReads, catalogID db.UUID, namespace []string, name string) bool {
	_, err := q.GetMetadataTableByName(ctx, db.GetMetadataTableByNameParams{
		CatalogID: catalogID, Namespace: namespace, Name: name,
	})
	return err == nil
}

// modelReferencedColumns are the columns a model names: its primary key and
// the source column of each dimension (the dimension name when SQL is empty).
func modelReferencedColumns(m semantic.Model) []string {
	seen := map[string]bool{}
	var out []string
	add := func(c string) {
		if c == "" || seen[c] {
			return
		}
		seen[c] = true
		out = append(out, c)
	}
	for _, c := range m.PrimaryKey {
		add(c)
	}
	for _, d := range m.Dimensions {
		if d.SQL != "" {
			if isSimpleColumn(d.SQL) {
				add(d.SQL)
			}
		} else {
			add(d.Name)
		}
	}
	return out
}

// isSimpleColumn reports whether s is a plain unquoted identifier, so an
// expression is never treated as a column that must exist.
func isSimpleColumn(s string) bool {
	for i, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return s != ""
}
