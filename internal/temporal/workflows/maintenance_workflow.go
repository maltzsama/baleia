package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// MaintenanceRunWorkflow runs the selected Iceberg maintenance operations for
// one table and records the run to completion (#117). It is separate from the
// legacy LensMaintenanceWorkflow so in-flight histories keep replaying.
func MaintenanceRunWorkflow(ctx workflow.Context, runID, engineID, catalog, namespace, table, retention string, operations []string) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    60 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	return workflow.ExecuteActivity(ctx, activities.LensRunMaintenance, activities.LensRunMaintenanceParams{
		RunID:       runID,
		EngineID:    engineID,
		ExecutionID: runID,
		Catalog:     catalog,
		Namespace:   namespace,
		Table:       table,
		Retention:   retention,
		Operations:  operations,
	}).Get(ctx, nil)
}
