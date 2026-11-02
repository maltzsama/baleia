package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// RefreshTablePopularityWorkflow is one pass of the popularity refresh (B7,
// #562): it counts the last 30 days of table reads for every workspace.
func RefreshTablePopularityWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	return workflow.ExecuteActivity(ctx, activities.RefreshTablePopularity, struct{}{}).Get(ctx, nil)
}
