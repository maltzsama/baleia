package api

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/temporal/schedulespecs"
	"go.temporal.io/api/serviceerror"
)

// Platform sections of the system status (#291). Each section is probed under
// its own 2 s timeout and degrades to `unknown` with a stable code; no host,
// credential or raw error ever reaches the response.

func statusStringPtr(s string) *string { return &s }

func (h *Handlers) probePlatform(ctx context.Context, status *SystemStatus) {
	var wg sync.WaitGroup
	wg.Add(7)
	go func() {
		defer wg.Done()
		status.Schedules = runResource(ctx, "temporal_unavailable", h.schedulesStatus)
	}()
	go func() { defer wg.Done(); status.Engines = runResource(ctx, "engines_unavailable", h.enginesStatus) }()
	go func() { defer wg.Done(); status.Bindings = runResource(ctx, "bindings_unavailable", h.bindingsStatus) }()
	go func() { defer wg.Done(); status.Metadata = runResource(ctx, "metadata_unavailable", h.metadataStatus) }()
	go func() {
		defer wg.Done()
		status.Reconcile = runResource(ctx, "reconcile_unavailable", h.reconcileStatus)
	}()
	go func() {
		defer wg.Done()
		status.AuditExport = runResource(ctx, "audit_export_unavailable", h.auditExportStatus)
	}()
	go func() { defer wg.Done(); status.Plugin = runResource(ctx, "plugin_unavailable", h.pluginStatus) }()
	wg.Wait()
}

func runResource(ctx context.Context, errorCode string, probe func(context.Context) SystemStatusResource) SystemStatusResource {
	ctx, cancel := context.WithTimeout(ctx, systemStatusSectionTimeout)
	defer cancel()
	res := probe(ctx)
	if res.Status == "" {
		res.Status = SystemStatusResourceStatusOk
	}
	if res.Status == SystemStatusResourceStatusUnknown && res.ErrorCode == nil {
		res.ErrorCode = statusStringPtr(errorCode)
	}
	return res
}

func statusItems(items []SystemStatusItem) *[]SystemStatusItem { return &items }

func (h *Handlers) metadataSyncEvery() time.Duration {
	if h.cfg == nil {
		return config.DefaultMetadataSyncInterval
	}
	return h.cfg.MetadataSyncEvery()
}

// schedulesStatus reports every exported core schedule, including the
// directory reconciler added with user suspension (#257).
func (h *Handlers) schedulesStatus(ctx context.Context) SystemStatusResource {
	items := make([]SystemStatusItem, 0)
	describe := h.systemStatusSchedule
	if describe == nil {
		if h.temporalClient == nil {
			return SystemStatusResource{Status: SystemStatusResourceStatusUnknown, ErrorCode: statusStringPtr("temporal_unavailable"), Items: statusItems(items)}
		}
		describe = func(ctx context.Context, id string) (SystemStatusItemStatus, string) {
			_, err := h.temporalClient.ScheduleClient().GetHandle(ctx, id).Describe(ctx)
			if err == nil {
				return SystemStatusItemStatusOk, ""
			}
			var nf *serviceerror.NotFound
			if errors.As(err, &nf) {
				return SystemStatusItemStatusMissing, "schedule_missing"
			}
			return SystemStatusItemStatusUnknown, "schedule_unknown"
		}
	}

	degraded := false
	for _, spec := range schedulespecs.Core(h.metadataSyncEvery()) {
		status, detail := describe(ctx, spec.ID)
		item := SystemStatusItem{Name: spec.ID, Status: status}
		if detail != "" {
			item.Detail = statusStringPtr(detail)
		}
		if status == SystemStatusItemStatusUnknown {
			degraded = true
		}
		items = append(items, item)
	}

	res := SystemStatusResource{Status: SystemStatusResourceStatusOk, Items: statusItems(items)}
	if degraded {
		res.Status = SystemStatusResourceStatusDegraded
	}
	return res
}

// enginesStatus reuses the cached engine probe: the status page never adds a
// second round trip.
func (h *Handlers) enginesStatus(ctx context.Context) SystemStatusResource {
	items := make([]SystemStatusItem, 0)
	if h.queries == nil {
		return SystemStatusResource{Status: SystemStatusResourceStatusUnknown, ErrorCode: statusStringPtr("database_unavailable"), Items: statusItems(items)}
	}
	engines, err := h.queries.ListEngines(ctx)
	if err != nil {
		return SystemStatusResource{Status: SystemStatusResourceStatusUnknown, ErrorCode: statusStringPtr("database_unavailable"), Items: statusItems(items)}
	}
	for _, e := range engines {
		item := SystemStatusItem{Name: e.Name, Status: SystemStatusItemStatusUnknown, Detail: statusStringPtr(e.Kind)}
		if h.systemStatusEngine != nil {
			status, detail := h.systemStatusEngine(ctx, e)
			item.Status = status
			if detail != "" {
				item.Detail = statusStringPtr(detail)
			}
		} else if h.engines != nil {
			if eng, err := h.engines.Get(ctx, e.ID); err == nil {
				probe := h.engineProbeFor(ctx, e, eng)
				item.Status = SystemStatusItemStatus(probe.Health.Status)
				if probe.Version != nil {
					item.Detail = probe.Version
				}
			}
		}
		items = append(items, item)
	}
	return SystemStatusResource{Status: SystemStatusResourceStatusOk, Items: statusItems(items)}
}

func (h *Handlers) bindingsStatus(ctx context.Context) SystemStatusResource {
	items := make([]SystemStatusItem, 0)
	if h.queries == nil {
		return SystemStatusResource{Status: SystemStatusResourceStatusUnknown, ErrorCode: statusStringPtr("database_unavailable"), Items: statusItems(items)}
	}
	rows, err := h.queries.CountManagedBindingsByState(ctx)
	if err != nil {
		return SystemStatusResource{Status: SystemStatusResourceStatusUnknown, ErrorCode: statusStringPtr("database_unavailable"), Items: statusItems(items)}
	}
	for _, r := range rows {
		n := r.Count
		items = append(items, SystemStatusItem{Name: r.LifecycleState, Status: SystemStatusItemStatusOk, Value: &n})
	}
	return SystemStatusResource{Status: SystemStatusResourceStatusOk, Items: statusItems(items)}
}

func (h *Handlers) metadataStatus(ctx context.Context) SystemStatusResource {
	items := make([]SystemStatusItem, 0)
	if h.queries == nil {
		return SystemStatusResource{Status: SystemStatusResourceStatusUnknown, ErrorCode: statusStringPtr("database_unavailable"), Items: statusItems(items)}
	}
	threshold := int32(2 * h.metadataSyncEvery().Seconds())
	if threshold < 60 {
		threshold = 60
	}
	counts, err := h.queries.MetadataStatusCounts(ctx, threshold)
	if err != nil {
		return SystemStatusResource{Status: SystemStatusResourceStatusUnknown, ErrorCode: statusStringPtr("database_unavailable"), Items: statusItems(items)}
	}
	items = append(items,
		SystemStatusItem{Name: "catalogs", Status: SystemStatusItemStatusOk, Value: &counts.Catalogs},
		SystemStatusItem{Name: "stale", Status: SystemStatusItemStatusOk, Value: &counts.Stale},
		SystemStatusItem{Name: "failed", Status: SystemStatusItemStatusOk, Value: &counts.Failed},
	)
	return SystemStatusResource{Status: SystemStatusResourceStatusOk, Items: statusItems(items)}
}

func (h *Handlers) reconcileStatus(ctx context.Context) SystemStatusResource {
	items := make([]SystemStatusItem, 0)
	if h.queries == nil {
		return SystemStatusResource{Status: SystemStatusResourceStatusUnknown, ErrorCode: statusStringPtr("database_unavailable"), Items: statusItems(items)}
	}
	engines, err := h.queries.ListEngines(ctx)
	if err != nil {
		return SystemStatusResource{Status: SystemStatusResourceStatusUnknown, ErrorCode: statusStringPtr("database_unavailable"), Items: statusItems(items)}
	}
	runs, err := h.queries.LatestReconcileRuns(ctx)
	if err != nil {
		return SystemStatusResource{Status: SystemStatusResourceStatusUnknown, ErrorCode: statusStringPtr("database_unavailable"), Items: statusItems(items)}
	}

	latest := make(map[[16]byte]SystemStatusItemStatus, len(runs))
	for _, r := range runs {
		status := SystemStatusItemStatusOk
		if r.Error != nil && *r.Error != "" {
			status = SystemStatusItemStatusDegraded
		}
		latest[r.EngineID.Bytes] = status
	}

	for _, e := range engines {
		item := SystemStatusItem{Name: e.Name, Status: SystemStatusItemStatusMissing, Detail: statusStringPtr("no_run")}
		if status, ok := latest[e.ID.Bytes]; ok {
			item.Status = status
			item.Detail = statusStringPtr("run_recorded")
		}
		items = append(items, item)
	}
	return SystemStatusResource{Status: SystemStatusResourceStatusOk, Items: statusItems(items)}
}

func (h *Handlers) auditExportStatus(ctx context.Context) SystemStatusResource {
	items := make([]SystemStatusItem, 0)
	configured := h.cfg != nil && h.cfg.AuditExportURL != ""
	configuredValue := int64(0)
	configuredStatus := SystemStatusItemStatusMissing
	if configured {
		configuredValue = 1
		configuredStatus = SystemStatusItemStatusOk
	}
	items = append(items, SystemStatusItem{Name: "configured", Status: configuredStatus, Value: &configuredValue})

	if h.queries == nil {
		return SystemStatusResource{Status: SystemStatusResourceStatusUnknown, ErrorCode: statusStringPtr("database_unavailable"), Items: statusItems(items)}
	}
	lag, err := h.queries.AuditExportLagSeconds(ctx)
	if err != nil {
		return SystemStatusResource{Status: SystemStatusResourceStatusUnknown, ErrorCode: statusStringPtr("database_unavailable"), Items: statusItems(items)}
	}
	lagSeconds := int64(lag)
	items = append(items, SystemStatusItem{Name: "lag_seconds", Status: SystemStatusItemStatusOk, Value: &lagSeconds})
	return SystemStatusResource{Status: SystemStatusResourceStatusOk, Items: statusItems(items)}
}

func (h *Handlers) pluginStatus(ctx context.Context) SystemStatusResource {
	items := make([]SystemStatusItem, 0)
	expected := "0.9.2"
	if h.cfg != nil && h.cfg.PluginVersion != "" {
		expected = h.cfg.PluginVersion
	}
	items = append(items, SystemStatusItem{Name: "expected_version", Status: SystemStatusItemStatusOk, Detail: statusStringPtr(expected)})

	if h.queries == nil {
		return SystemStatusResource{Status: SystemStatusResourceStatusUnknown, ErrorCode: statusStringPtr("database_unavailable"), Items: statusItems(items)}
	}
	last, err := h.queries.PluginLastConfirmation(ctx, "trino")
	if err != nil {
		return SystemStatusResource{Status: SystemStatusResourceStatusUnknown, ErrorCode: statusStringPtr("database_unavailable"), Items: statusItems(items)}
	}
	confirmation := SystemStatusItem{Name: "last_confirmation", Status: SystemStatusItemStatusMissing, Detail: statusStringPtr("never_confirmed")}
	if last.Valid {
		confirmation.Status = SystemStatusItemStatusOk
		confirmation.Detail = statusStringPtr(last.Time.UTC().Format(time.RFC3339))
	}
	items = append(items, confirmation)
	return SystemStatusResource{Status: SystemStatusResourceStatusOk, Items: statusItems(items)}
}
