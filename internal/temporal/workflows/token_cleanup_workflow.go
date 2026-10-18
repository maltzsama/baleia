package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// TokenCleanupWorkflow prunes expired refresh tokens and expired or used
// password-reset tokens (schedule token-cleanup, daily, #51). It does one pass
// and returns, like every scheduled workflow.
func TokenCleanupWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5},
	})
	return workflow.ExecuteActivity(ctx, activities.CleanupExpiredTokens).Get(ctx, nil)
}
