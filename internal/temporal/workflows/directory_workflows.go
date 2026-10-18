package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// DirectoryReconcileWorkflow runs one pass of the directory reconciliation
// (schedule directory-reconcile, every 15 minutes, #257). The activity pages
// each enabled LDAP provider; the safety brake aborts a mass suspension.
func DirectoryReconcileWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	return workflow.ExecuteActivity(ctx, activities.ReconcileDirectories, activities.DirectoryReconcileParams{}).Get(ctx, nil)
}
