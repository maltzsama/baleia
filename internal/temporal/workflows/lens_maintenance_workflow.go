package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func LensMaintenanceWorkflow(ctx workflow.Context, executionID, catalog, namespace, table, retention string) error {
	// Maintenance is expensive; never auto-retry.
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    60 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})

	return workflow.ExecuteActivity(ctx, activities.LensRunMaintenance, activities.LensRunMaintenanceParams{
		ExecutionID: executionID,
		Catalog:     catalog,
		Namespace:   namespace,
		Table:       table,
		Retention:   retention,
	}).Get(ctx, nil)
}
