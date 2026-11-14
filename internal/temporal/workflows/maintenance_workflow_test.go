package workflows

import (
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

// TestMaintenanceRunWorkflow_ClosesTheRun: whatever the activity returns, the
// workflow closes the run itself, so a run is never left "running" because the
// activity could not.
func TestMaintenanceRunWorkflow_ClosesTheRun(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		activityErr error
		wantFailure string
		wantErr     bool
	}{
		{name: "success closes without a failure"},
		{
			name:        "failure closes with the activity's cause",
			activityErr: temporal.NewNonRetryableApplicationError("optimize failed", "maintenance", nil),
			wantFailure: "optimize failed", wantErr: true,
		},
		{
			name:        "a plain error closes with its message",
			activityErr: errors.New("engine down"),
			wantFailure: "engine down", wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestWorkflowEnvironment()
			env.OnActivity(activities.LensRunMaintenance, mock.Anything, mock.Anything).Return(tt.activityErr)
			env.OnActivity(activities.CloseMaintenanceRun, mock.Anything,
				activities.CloseMaintenanceRunParams{RunID: "run-1", Failure: tt.wantFailure}).Return(nil).Once()

			env.ExecuteWorkflow(MaintenanceRunWorkflow, "run-1", "engine-1", "lake", "cdc", "orders", "7d", []string{"optimize"})

			require.True(t, env.IsWorkflowCompleted())
			if tt.wantErr {
				require.Error(t, env.GetWorkflowError())
			} else {
				require.NoError(t, env.GetWorkflowError())
			}
			env.AssertExpectations(t)
		})
	}
}

// TestMaintenanceRunWorkflow_CloseFailureKeepsTheRunError: a run that cannot
// be closed does not hide how the activity ended.
func TestMaintenanceRunWorkflow_CloseFailureKeepsTheRunError(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.OnActivity(activities.LensRunMaintenance, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.CloseMaintenanceRun, mock.Anything, mock.Anything).Return(errors.New("database down"))

	env.ExecuteWorkflow(MaintenanceRunWorkflow, "run-1", "engine-1", "lake", "cdc", "orders", "7d", []string{"optimize"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}
