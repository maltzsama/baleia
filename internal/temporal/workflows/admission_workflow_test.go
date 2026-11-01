package workflows

import (
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func admissionState() AdmissionState {
	return AdmissionState{
		Capacities: []AdmissionCapacity{
			{Class: queryengine.ClassInteractive, MaxRunning: 1, MaxQueued: 2, QueueTimeout: time.Hour},
			{Class: queryengine.ClassHeavy, MaxRunning: 1, MaxQueued: 1, QueueTimeout: time.Hour},
			{Class: queryengine.ClassBackground, MaxRunning: 0, MaxQueued: 0, QueueTimeout: time.Hour},
		},
	}
}

func collectFailures() (*[]AdmissionFailure, func(AdmissionFailure)) {
	var failures []AdmissionFailure
	return &failures, func(f AdmissionFailure) { failures = append(failures, f) }
}

func TestAdmissionState_AdmitReleaseReuse(t *testing.T) {
	t.Parallel()
	s := admissionState()
	now := time.Unix(0, 0)
	failures, fail := collectFailures()

	s.request(now, AdmissionRequest{ExecutionID: "a", Class: queryengine.ClassInteractive}, fail)
	leases := s.admitAll()
	require.Len(t, leases, 1)
	require.Equal(t, "a", leases[0].ExecutionID)

	s.request(now.Add(time.Second), AdmissionRequest{ExecutionID: "b", Class: queryengine.ClassInteractive}, fail)
	require.Len(t, s.Queued, 1, "the second run waits for the only interactive slot")
	require.Empty(t, s.admitAll())
	require.Empty(t, *failures)

	s.release("a")
	leases = s.admitAll()
	require.Len(t, leases, 1)
	require.Equal(t, "b", leases[0].ExecutionID, "the freed slot goes to the queued run")
	require.Empty(t, s.Queued)
}

func TestAdmissionState_DuplicateRequestTakesNoSecondSlot(t *testing.T) {
	t.Parallel()
	s := admissionState()
	now := time.Unix(0, 0)
	failures, fail := collectFailures()

	s.request(now, AdmissionRequest{ExecutionID: "a", Class: queryengine.ClassInteractive}, fail)
	require.Len(t, s.admitAll(), 1)
	s.request(now, AdmissionRequest{ExecutionID: "a", Class: queryengine.ClassInteractive}, fail)
	require.Len(t, s.Running, 1)
	require.Empty(t, s.Queued)
	require.Empty(t, *failures)
}

func TestAdmissionState_ClosedClassAndFullQueue(t *testing.T) {
	t.Parallel()
	s := admissionState()
	now := time.Unix(0, 0)
	failures, fail := collectFailures()

	// Background is closed (MaxRunning 0).
	s.request(now, AdmissionRequest{ExecutionID: "bg", Class: queryengine.ClassBackground}, fail)
	require.Len(t, *failures, 1)
	require.Equal(t, "queue_full", (*failures)[0].Code)

	// Interactive has MaxQueued 2: one running, two queued, the fourth is full.
	s.request(now, AdmissionRequest{ExecutionID: "a", Class: queryengine.ClassInteractive}, fail)
	require.Len(t, s.admitAll(), 1)
	s.request(now, AdmissionRequest{ExecutionID: "b", Class: queryengine.ClassInteractive}, fail)
	s.request(now, AdmissionRequest{ExecutionID: "c", Class: queryengine.ClassInteractive}, fail)
	s.request(now, AdmissionRequest{ExecutionID: "d", Class: queryengine.ClassInteractive}, fail)
	require.Equal(t, "queue_full", (*failures)[1].Code)
}

func TestAdmissionState_EngineCeiling(t *testing.T) {
	t.Parallel()
	s := admissionState()
	s.MaxRunningTotal = 1
	now := time.Unix(0, 0)
	failures, fail := collectFailures()

	s.request(now, AdmissionRequest{ExecutionID: "a", Class: queryengine.ClassInteractive}, fail)
	s.request(now.Add(time.Second), AdmissionRequest{ExecutionID: "b", Class: queryengine.ClassHeavy}, fail)
	leases := s.admitAll()
	require.Len(t, leases, 1)
	require.Equal(t, "a", leases[0].ExecutionID)
	require.Len(t, s.Queued, 1, "the ceiling keeps the heavy run queued")

	s.MaxRunningTotal = 2
	leases = s.admitAll()
	require.Len(t, leases, 1)
	require.Equal(t, "b", leases[0].ExecutionID)
	require.Empty(t, *failures)
}

func TestAdmissionState_Expire(t *testing.T) {
	t.Parallel()
	s := admissionState()
	now := time.Unix(0, 0)
	failures, fail := collectFailures()

	s.request(now, AdmissionRequest{ExecutionID: "a", Class: queryengine.ClassInteractive}, fail)
	require.Len(t, s.admitAll(), 1)
	s.request(now, AdmissionRequest{ExecutionID: "b", Class: queryengine.ClassInteractive, QueueTimeout: time.Second}, fail)

	require.Empty(t, s.expire(now.Add(500*time.Millisecond)))
	require.Len(t, s.Queued, 1)

	expired := s.expire(now.Add(2 * time.Second))
	require.Len(t, expired, 1)
	require.Equal(t, "b", expired[0].ExecutionID)
	require.Empty(t, s.Queued)
	require.Empty(t, *failures)
}

func newAdmissionEnv(t *testing.T) (*testsuite.TestWorkflowEnvironment, *[]AdmissionLease, *[]AdmissionFailure) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(AdmissionWorkflow)

	admitted := &[]AdmissionLease{}
	failed := &[]AdmissionFailure{}
	env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, mock.Anything, AdmittedSignal, mock.Anything).
		Return(func(_, _, _, _ string, arg any) error {
			if lease, ok := arg.(AdmissionLease); ok {
				*admitted = append(*admitted, lease)
			}
			return nil
		})
	env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, mock.Anything, AdmissionFailedSignal, mock.Anything).
		Return(func(_, _, _, _ string, arg any) error {
			if f, ok := arg.(AdmissionFailure); ok {
				*failed = append(*failed, f)
			}
			return nil
		})
	return env, admitted, failed
}

func TestAdmissionWorkflow_AdmitsReleasesAndCancels(t *testing.T) {
	t.Parallel()
	env, admitted, failed := newAdmissionEnv(t)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdmissionRequestSignal, AdmissionRequest{ExecutionID: "a", Class: queryengine.ClassInteractive})
		env.SignalWorkflow(AdmissionRequestSignal, AdmissionRequest{ExecutionID: "b", Class: queryengine.ClassInteractive})
	}, time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdmissionReleaseSignal, AdmissionRelease{ExecutionID: "a"})
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdmissionCancelSignal, AdmissionCancel{ExecutionID: "b"})
	}, 2*time.Second)
	env.RegisterDelayedCallback(env.CancelWorkflow, 3*time.Second)
	env.ExecuteWorkflow(AdmissionWorkflow, "e1", admissionState())

	require.Len(t, *admitted, 2)
	require.Equal(t, "a", (*admitted)[0].ExecutionID)
	require.Equal(t, "b", (*admitted)[1].ExecutionID, "the released slot goes to the queued run")
	require.Len(t, *failed, 0, "b was admitted before the cancel")
}

func TestAdmissionWorkflow_CancelWhileQueued(t *testing.T) {
	t.Parallel()
	env, admitted, failed := newAdmissionEnv(t)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdmissionRequestSignal, AdmissionRequest{ExecutionID: "a", Class: queryengine.ClassInteractive})
		env.SignalWorkflow(AdmissionRequestSignal, AdmissionRequest{ExecutionID: "b", Class: queryengine.ClassInteractive})
	}, time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdmissionCancelSignal, AdmissionCancel{ExecutionID: "b"})
	}, time.Second)
	env.RegisterDelayedCallback(env.CancelWorkflow, 2*time.Second)
	env.ExecuteWorkflow(AdmissionWorkflow, "e1", admissionState())

	require.Len(t, *admitted, 1)
	require.Len(t, *failed, 1)
	require.Equal(t, "cancelled", (*failed)[0].Code)
}

func TestAdmissionWorkflow_ContinuesAsNewWithState(t *testing.T) {
	t.Parallel()
	env, _, _ := newAdmissionEnv(t)
	env.SetCurrentHistoryLength(admissionHistoryLimit + 1)
	state := admissionState()
	state.Running = []AdmissionLease{{ExecutionID: "a", Class: queryengine.ClassInteractive, AdmittedAt: time.Unix(0, 0)}}
	state.Queued = []AdmissionQueued{{ExecutionID: "b", Class: queryengine.ClassInteractive, EnqueuedAt: time.Unix(0, 0), Deadline: time.Unix(3600, 0)}}
	env.ExecuteWorkflow(AdmissionWorkflow, "e1", state)

	err := env.GetWorkflowError()
	var canErr *workflow.ContinueAsNewError
	require.ErrorAs(t, err, &canErr)
	require.Equal(t, "AdmissionWorkflow", canErr.WorkflowType.Name)

	var engineID string
	var carried AdmissionState
	require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(canErr.Input, &engineID, &carried))
	require.Equal(t, "e1", engineID)
	require.Len(t, carried.Running, 1, "the lease survives continue-as-new")
	require.Equal(t, "a", carried.Running[0].ExecutionID)
	require.Len(t, carried.Queued, 1, "the queue survives continue-as-new")
	require.Equal(t, "b", carried.Queued[0].ExecutionID)
	require.Equal(t, state.Capacities, carried.Capacities)
}

func TestAdmissionWorkflow_QueueTimeout(t *testing.T) {
	t.Parallel()
	env, admitted, failed := newAdmissionEnv(t)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdmissionRequestSignal, AdmissionRequest{ExecutionID: "a", Class: queryengine.ClassInteractive})
		env.SignalWorkflow(AdmissionRequestSignal, AdmissionRequest{ExecutionID: "b", Class: queryengine.ClassInteractive, QueueTimeout: time.Second})
	}, time.Millisecond)
	env.RegisterDelayedCallback(env.CancelWorkflow, 3*time.Second)
	env.ExecuteWorkflow(AdmissionWorkflow, "e1", admissionState())

	require.Len(t, *admitted, 1)
	require.Len(t, *failed, 1)
	require.Equal(t, "queue_timeout", (*failed)[0].Code)
}

func TestAdmissionWorkflow_CapacityChangeAppliesLive(t *testing.T) {
	t.Parallel()
	env, admitted, _ := newAdmissionEnv(t)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdmissionRequestSignal, AdmissionRequest{ExecutionID: "a", Class: queryengine.ClassInteractive})
		env.SignalWorkflow(AdmissionRequestSignal, AdmissionRequest{ExecutionID: "b", Class: queryengine.ClassInteractive})
	}, time.Millisecond)
	env.RegisterDelayedCallback(func() {
		caps := admissionState()
		caps.Capacities[0].MaxRunning = 2
		env.SignalWorkflow(AdmissionCapacitySignal, AdmissionCapacities{
			MaxRunningTotal: caps.MaxRunningTotal,
			Classes:         caps.Capacities,
		})
	}, time.Second)
	env.RegisterDelayedCallback(env.CancelWorkflow, 2*time.Second)
	env.ExecuteWorkflow(AdmissionWorkflow, "e1", admissionState())

	require.Len(t, *admitted, 2)
	require.Equal(t, "b", (*admitted)[1].ExecutionID, "raising the capacity admits the queued run")
}
