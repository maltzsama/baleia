package api

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"go.temporal.io/api/serviceerror"
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

func (h *Handlers) ListAttention(ctx context.Context, _ ListAttentionRequestObject) (ListAttentionResponseObject, error) {
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
func (h *Handlers) attentionCacheStore() *TTLStore[[]AttentionItem] {
	h.attentionOnce.Do(func() {
		if h.attentionCache == nil {
			h.attentionCache = NewTTLStore[[]AttentionItem](attentionCacheTTL)
		}
	})
	return h.attentionCache
}

func (h *Handlers) attentionItems(ctx context.Context, p authz.Principal) ([]AttentionItem, error) {
	items := make([]AttentionItem, 0)

	items = append(items, h.attentionBindingErrors(ctx, p)...)
	items = append(items, h.attentionMetadataFailures(ctx, p)...)
	items = append(items, h.attentionMetadataStale(ctx, p)...)
	items = append(items, h.attentionEngineUnreachable(ctx, p)...)
	items = append(items, h.attentionRetrieverErrors(ctx, p)...)

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

func (h *core) attentionBindingErrors(ctx context.Context, p authz.Principal) []AttentionItem {
	rows, err := h.queries.ListAttentionBindingErrors(ctx)
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

func (h *core) attentionMetadataFailures(ctx context.Context, p authz.Principal) []AttentionItem {
	rows, err := h.queries.ListAttentionMetadataSyncFailed(ctx)
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

func (h *core) attentionMetadataStale(ctx context.Context, p authz.Principal) []AttentionItem {
	threshold := attentionStaleMultiplier * h.metadataSyncEvery()
	rows, err := h.queries.ListAttentionMetadataStale(ctx, threshold.Seconds())
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

func (h *Handlers) attentionEngineUnreachable(ctx context.Context, p authz.Principal) []AttentionItem {
	if h.engines == nil {
		return nil
	}
	var engines []db.Engine
	var err error
	if p.IsAdmin() {
		engines, err = h.queries.ListEngines(ctx)
	} else {
		engines, err = h.queries.ListEnginesVisibleToUser(ctx, p.UserID)
	}
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

func (h *core) attentionRetrieverErrors(ctx context.Context, p authz.Principal) []AttentionItem {
	rows, err := h.queries.ListAttentionRetrieverErrors(ctx)
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

func (h *core) attentionReconcileDrift(ctx context.Context) []AttentionItem {
	rows, err := h.queries.ListAttentionReconcileDrift(ctx)
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
		}
	}
	return out
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

func (h *core) attentionAuditExport(ctx context.Context) []AttentionItem {
	if h.settings.Ops.AuditExportURL == "" {
		return nil
	}
	since, err := h.queries.GetAttentionAuditExportLag(ctx)
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
