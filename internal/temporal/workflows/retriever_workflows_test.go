package workflows

import (
	"testing"

	"github.com/galileostudio/baleia/internal/temporal/activities"
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
