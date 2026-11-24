package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// DataPolicyReissueWorkflow re-issues the published data policy revision when
// it is due (schedule data-policy-reissue, ADR 0028). It does one pass and
// returns, like every scheduled workflow.
func DataPolicyReissueWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	return workflow.ExecuteActivity(ctx, activities.ReissueDataPolicies).Get(ctx, nil)
}
