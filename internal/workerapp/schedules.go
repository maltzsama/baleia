package workerapp

import (
	"context"
	"fmt"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"github.com/galileostudio/baleia/internal/temporal/schedulespecs"
	"go.temporal.io/sdk/client"
)

// EnsureSchedules creates the recurring jobs as Temporal Schedules,
// idempotently on every boot. Each workflow does a single pass and returns.
func EnsureSchedules(ctx context.Context, c client.Client, cfg *config.Config) error {
	if err := schedules.Ensure(ctx, c, TaskQueue, schedulespecs.Core(cfg.MetadataSyncEvery())); err != nil {
		return fmt.Errorf("schedules: %w", err)
	}
	// Binding reconciliation on a schedule is opt-in (#37).
	var err error
	if cfg.ReconcileInterval > 0 {
		err = schedules.Ensure(ctx, c, TaskQueue, []schedules.Spec{
			schedulespecs.CatalogReconcile(cfg.ReconcileInterval),
		})
	} else {
		err = schedules.Remove(ctx, c, schedules.CatalogReconcile)
	}
	if err != nil {
		return fmt.Errorf("reconcile schedule: %w", err)
	}
	// Audit export to OpenSearch (#50) runs only when a sink is configured.
	if cfg.AuditExportURL != "" {
		if err := schedules.Ensure(ctx, c, TaskQueue, []schedules.Spec{
			schedulespecs.AuditExport(cfg.AuditExportInterval),
		}); err != nil {
			return fmt.Errorf("audit export schedule: %w", err)
		}
	} else {
		// Best effort: an absent schedule is the state we want.
		_ = schedules.Remove(ctx, c, schedules.AuditExport)
	}
	// Cold offload and retention (ADR 0024, ADR 0026).
	ensure, remove, err := coldSchedules(cfg)
	if err != nil {
		return err
	}
	if err := schedules.Ensure(ctx, c, TaskQueue, ensure); err != nil {
		return fmt.Errorf("cold schedules: %w", err)
	}
	for _, id := range remove {
		// Best effort: an absent schedule is the state we want.
		_ = schedules.Remove(ctx, c, id)
	}
	return nil
}

// coldSchedules decides which cold offload and retention schedules exist. A
// stream with a cold bucket is offloaded, hourly. Its retention runs on the
// stream's cadence when it has a bucket to gate on, or when it opted out of the
// gate (BALEIA_*_OFFLOAD_BEFORE_DROP=false), and does not exist otherwise: a
// stream with neither keeps its retention fail-closed, it never drops. A
// schedule that should not exist is returned in remove, so a changed
// configuration takes it away.
func coldSchedules(cfg *config.Config) (ensure []schedules.Spec, remove []string, err error) {
	for _, st := range []struct {
		bucket                 string
		dropWithoutOffload     bool
		cadence                string
		offloadID, retentionID string
		offload                func(time.Duration) schedules.Spec
		retention              func(string) schedules.Spec
	}{
		{
			bucket: cfg.AuditColdBucket, dropWithoutOffload: cfg.AuditDropWithoutOffload, cadence: cfg.AuditRetentionCadence,
			offloadID: schedules.AuditOffload, retentionID: schedules.AuditRetention,
			offload: schedulespecs.AuditOffload, retention: schedulespecs.AuditRetention,
		},
		{
			bucket: cfg.QueryHistoryColdBucket, dropWithoutOffload: cfg.QueryHistoryDropWithoutOffload, cadence: cfg.QueryHistoryRetentionCadence,
			offloadID: schedules.QueryHistoryOffload, retentionID: schedules.QueryHistoryRetention,
			offload: schedulespecs.QueryHistoryOffload, retention: schedulespecs.QueryHistoryRetention,
		},
	} {
		if st.bucket != "" {
			ensure = append(ensure, st.offload(time.Hour))
		} else {
			remove = append(remove, st.offloadID)
		}
		if st.bucket == "" && !st.dropWithoutOffload {
			remove = append(remove, st.retentionID)
			continue
		}
		cron, err := config.RetentionCron(st.cadence)
		if err != nil {
			return nil, nil, fmt.Errorf("%s retention cadence: %w", st.retentionID, err)
		}
		ensure = append(ensure, st.retention(cron))
	}
	return ensure, remove, nil
}
