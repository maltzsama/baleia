package workerapp

import (
	"context"
	"fmt"

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
	return nil
}
