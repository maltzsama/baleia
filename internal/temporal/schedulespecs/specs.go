// Package schedulespecs is the single list of recurring Temporal Schedules the
// worker ensures. It lives outside schedules to avoid an import cycle:
// workflows import schedules for their metric labels.
package schedulespecs

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
)

// Core returns the schedules every worker ensures at boot. metadataSyncEvery
// comes from configuration.
func Core(metadataSyncEvery time.Duration) []schedules.Spec {
	return []schedules.Spec{
		{ID: schedules.LensQueryPoller, Workflow: workflows.LensQueryPollWorkflow, Every: 30 * time.Second},
		{ID: schedules.LensClusterPoller, Workflow: workflows.LensClusterPollWorkflow, Every: 30 * time.Second},
		{ID: schedules.LensTableHealth, Workflow: workflows.LensTableHealthWorkflow, Every: time.Hour},
		{ID: schedules.RetrieverScheduler, Workflow: workflows.RetrieverDispatchWorkflow, Every: 60 * time.Second},
		{ID: schedules.MetadataSync, Workflow: workflows.MetadataSyncDispatchWorkflow, Every: metadataSyncEvery},
		{ID: schedules.AuditPartitions, Workflow: workflows.AuditPartitionsWorkflow, Every: 24 * time.Hour},
		{ID: schedules.EphemeralCatalogSweeper, Workflow: workflows.EphemeralCatalogSweepWorkflow, Every: time.Minute},
		{ID: schedules.TokenCleanup, Workflow: workflows.TokenCleanupWorkflow, Every: 24 * time.Hour},
	}
}

// CatalogReconcile is the opt-in binding reconciliation schedule (#37).
func CatalogReconcile(every time.Duration) schedules.Spec {
	return schedules.Spec{ID: schedules.CatalogReconcile, Workflow: workflows.CatalogReconcileWorkflow, Every: every}
}

// AuditExport is the audit export schedule (#50), enabled when a sink exists.
func AuditExport(every time.Duration) schedules.Spec {
	return schedules.Spec{ID: schedules.AuditExport, Workflow: workflows.AuditExportWorkflow, Every: every}
}
