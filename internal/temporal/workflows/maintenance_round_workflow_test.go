package workflows

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// TestMaintenanceRound_StartsEachRun: every planned run runs as its own
// workflow with its run id (#500).
func TestMaintenanceRound_StartsEachRun(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(MaintenanceOperationWorkflow)
	env.OnActivity(activities.PlanMaintenanceRound, mock.Anything, activities.PlanMaintenanceRoundParams{CatalogID: "cat-1"}).
		Return([]activities.PlannedRun{
			{RunID: "run-1", WorkflowID: "maintenance-op-1", TimeoutSeconds: 600},
			{RunID: "run-2", WorkflowID: "maintenance-op-2", TimeoutSeconds: 60},
		}, nil)
	var mu sync.Mutex
	var ran []string
	env.OnActivity(activities.RunMaintenanceOperation, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, p activities.RunMaintenanceOperationParams) error {
			mu.Lock()
			defer mu.Unlock()
			ran = append(ran, p.RunID)
			return nil
		})

	env.ExecuteWorkflow(MaintenanceRoundWorkflow, "cat-1")

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.ElementsMatch(t, []string{"run-1", "run-2"}, ran)
}

// TestMaintenanceRound_PlanError: a failed plan fails the round, so the
// health pass logs it.
func TestMaintenanceRound_PlanError(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.OnActivity(activities.PlanMaintenanceRound, mock.Anything, mock.Anything).
		Return(nil, errors.New("database down"))

	env.ExecuteWorkflow(MaintenanceRoundWorkflow, "cat-1")

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}

// TestTableHealthAnalyze_ThenRound: analyze now runs the pass, then the
// catalog's round; a failed pass stops before the round.
func TestTableHealthAnalyze_ThenRound(t *testing.T) {
	t.Parallel()
	for _, fail := range []bool{false, true} {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.RegisterWorkflow(MaintenanceRoundWorkflow)
		params := activities.LensAnalyzeTableHealthParams{CatalogID: "cat-1", Namespace: "cdc", Table: "orders"}
		var analyzeErr error
		if fail {
			analyzeErr = errors.New("catalog unreachable")
		}
		env.OnActivity(activities.LensAnalyzeTableHealth, mock.Anything, params).Return(analyzeErr)
		rounds := 0
		env.OnActivity(activities.PlanMaintenanceRound, mock.Anything, activities.PlanMaintenanceRoundParams{CatalogID: "cat-1"}).
			Return(func(context.Context, activities.PlanMaintenanceRoundParams) ([]activities.PlannedRun, error) {
				rounds++
				return nil, nil
			})

		env.ExecuteWorkflow(TableHealthAnalyzeWorkflow, params)

		require.True(t, env.IsWorkflowCompleted())
		if fail {
			require.Error(t, env.GetWorkflowError())
			require.Zero(t, rounds)
		} else {
			require.NoError(t, env.GetWorkflowError())
			require.Equal(t, 1, rounds)
		}
	}
}
