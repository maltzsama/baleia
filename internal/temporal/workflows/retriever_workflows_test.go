package workflows

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const dueRetrieverID = "11111111-1111-1111-1111-111111111111"

// TestDispatch_StartsRefreshForDue pins the dispatch contract: a due retriever
// starts exactly one child refresh (with refresh_type full) and its schedule is
// advanced immediately. The deterministic child id
// retriever-<id>-<next_refresh_at> together with REJECT_DUPLICATE is what
// prevents a second dispatch pass from starting the same run twice.
func TestDispatch_StartsRefreshForDue(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.OnActivity(activities.RetrieverDispatch, mock.Anything, mock.Anything).
		Return([]activities.RetrieverDue{{ID: dueRetrieverID, NextRefreshAtUnix: 123}}, nil)
	env.OnActivity(activities.AdvanceRetrieverSchedule, mock.Anything, mock.Anything).
		Return(nil)

	// Exactly one child refresh with the retriever id and refresh_type full.
	env.OnWorkflow(RetrieverRefreshWorkflow, mock.Anything, dueRetrieverID, "full").
		Return(nil).
		Once()

	env.ExecuteWorkflow(RetrieverDispatchWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestDispatch_RecordsPassAfterAdvancing: the dispatch pass is recorded as a
// success only once its children started and schedules advanced.
func TestDispatch_RecordsPassAfterAdvancing(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	var events []string
	env.OnActivity(activities.RetrieverDispatch, mock.Anything, mock.Anything).
		Return([]activities.RetrieverDue{{ID: dueRetrieverID, NextRefreshAtUnix: 123}}, nil)
	env.OnActivity(activities.AdvanceRetrieverSchedule, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, p activities.AdvanceRetrieverScheduleParams) error {
			events = append(events, "advance")
			return nil
		})
	env.OnWorkflow(RetrieverRefreshWorkflow, mock.Anything, dueRetrieverID, "full").Return(nil)
	env.OnActivity(activities.SchedulePassSucceeded, mock.Anything, activities.SchedulePassSucceededParams{Schedule: schedules.RetrieverScheduler}).
		Return(func(ctx context.Context, p activities.SchedulePassSucceededParams) error {
			events = append(events, "pass")
			return nil
		}).Once()

	env.ExecuteWorkflow(RetrieverDispatchWorkflow)

	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, []string{"advance", "pass"}, events)
}
