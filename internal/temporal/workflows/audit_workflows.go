package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// AuditPartitionsWorkflow keeps the audit trail's monthly partitions ahead
// of time (schedule audit-partitions, daily, #45). A missing partition makes
// every audit write fail, so a day of retries is cheap insurance.
func AuditPartitionsWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5},
	})
	return workflow.ExecuteActivity(ctx, activities.EnsureAuditPartitions).Get(ctx, nil)
}
