package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// CatalogReconcileWorkflow applies one reconcile of every federated engine
// (schedule catalog-reconcile, only when BALEIA_RECONCILE_INTERVAL is set,
// #37). It is not retried: the next tick runs again.
func CatalogReconcileWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	return workflow.ExecuteActivity(ctx, activities.ReconcileCatalogs, activities.ReconcileCatalogsParams{DryRun: false}).Get(ctx, nil)
}
