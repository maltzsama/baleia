package workflows

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func v4Input() QueryExecutionInput {
	in := v3Input()
	in.WorkloadEnabled = true
	in.FairnessKey = "ws-1"
	in.Class = queryengine.ClassInteractive
	return in
}

// mockQueueActivities answers the status transitions V4 makes on the
// admission path.
func mockQueueActivities(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity(activities.MarkQueued, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.MarkAdmitted, mock.Anything, mock.Anything).Return(nil)
}

// signalRecorder counts the signals the workflow sends to the admission
// workflow.
type signalRecorder struct {
	mu      sync.Mutex
	signals map[string]int
}

func (r *signalRecorder) on(_, _, _, name string, _ any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.signals == nil {
		r.signals = map[string]int{}
	}
	r.signals[name]++
	return nil
}

func (r *signalRecorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.signals[name]
}

func TestV4_AdmittedRunsTheScriptAndReleases(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	bookkeeping(env, 1)
	mockQueueActivities(env)
	env.OnActivity(activities.ExecuteScript, mock.Anything, mock.Anything).Return(finished(1), nil)
	env.OnActivity(activities.CompleteExecution, mock.Anything, mock.Anything).Return(nil)
	rec := &signalRecorder{}
	env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(rec.on)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdmittedSignal, AdmissionLease{ExecutionID: "exec-2", Class: queryengine.ClassInteractive})
	}, time.Millisecond)
	env.ExecuteWorkflow(QueryExecutionWorkflowV4, v4Input())

	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, 1, rec.count(AdmissionRequestSignal))
	require.Equal(t, 1, rec.count(AdmissionReleaseSignal), "the slot is released after the script")
}

func TestV4_AdmissionFailureFailsTheExecution(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	rec := &signalRecorder{}
	env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(rec.on)
	mockQueueActivities(env)
	var failed activities.FailExecutionParams
	env.OnActivity(activities.FailExecution, mock.Anything, mock.Anything).Return(func(_ context.Context, p activities.FailExecutionParams) error {
		failed = p
		return nil
	})
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdmissionFailedSignal, AdmissionFailure{Code: AdmissionFailureQueueTimeout, Message: "waited too long"})
	}, time.Millisecond)
	env.ExecuteWorkflow(QueryExecutionWorkflowV4, v4Input())

	require.Error(t, env.GetWorkflowError())
	require.Equal(t, "waited too long", failed.ErrorMessage)
	require.Equal(t, 1, rec.count(AdmissionRequestSignal))
	require.Equal(t, 0, rec.count(AdmissionReleaseSignal), "a run that never held a slot releases nothing")
}

func TestV4_DisabledRunsDirectly(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	bookkeeping(env, 1)
	env.OnActivity(activities.ExecuteScript, mock.Anything, mock.Anything).Return(finished(1), nil)
	env.OnActivity(activities.CompleteExecution, mock.Anything, mock.Anything).Return(nil)
	in := v4Input()
	in.WorkloadEnabled = false
	env.ExecuteWorkflow(QueryExecutionWorkflowV4, in)

	require.NoError(t, env.GetWorkflowError())
}

func TestV4_CancelWhileQueuedReleasesAndRecordsTheCancel(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	rec := &signalRecorder{}
	env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(rec.on)
	mockQueueActivities(env)
	env.OnActivity(activities.AbandonStatements, mock.Anything, mock.Anything).Return(nil)
	var completed activities.CompleteExecutionParams
	env.OnActivity(activities.CompleteExecution, mock.Anything, mock.Anything).Return(func(_ context.Context, p activities.CompleteExecutionParams) error {
		completed = p
		return nil
	})
	env.RegisterDelayedCallback(env.CancelWorkflow, time.Second)
	env.ExecuteWorkflow(QueryExecutionWorkflowV4, v4Input())

	require.Equal(t, "cancelled", completed.Status)
	require.Equal(t, 1, rec.count(AdmissionCancelSignal))
	require.Equal(t, 1, rec.count(AdmissionReleaseSignal))
}
