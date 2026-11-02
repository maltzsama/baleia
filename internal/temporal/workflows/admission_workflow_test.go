package workflows

import (
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/temporal/activities"
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
	env.OnActivity(activities.RecordQueueState, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.RecordQueueAdmission, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.RecordQueueTimeout, mock.Anything, mock.Anything).Return(nil)

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

// TestAdmissionState_WeightedFairness: within a class, two fairness keys share
// the slots in proportion to their weight (1:2) instead of plain FIFO (#546).
func TestAdmissionState_WeightedFairness(t *testing.T) {
	t.Parallel()
	s := admissionState()
	s.Capacities = []AdmissionCapacity{
		{Class: queryengine.ClassInteractive, MaxRunning: 3, MaxQueued: 10, QueueTimeout: time.Hour},
	}
	now := time.Unix(0, 0)
	failures, fail := collectFailures()

	for i, id := range []string{"a1", "a2", "a3"} {
		s.request(now.Add(time.Duration(i)*time.Second), AdmissionRequest{ExecutionID: id, Class: queryengine.ClassInteractive, FairnessKey: "A", Weight: 1}, fail)
	}
	for i, id := range []string{"b1", "b2", "b3"} {
		s.request(now.Add(time.Duration(10+i)*time.Second), AdmissionRequest{ExecutionID: id, Class: queryengine.ClassInteractive, FairnessKey: "B", Weight: 2}, fail)
	}

	leases := s.admitAll()
	require.Len(t, leases, 3)
	byKey := map[string]int{}
	for _, lease := range leases {
		byKey[lease.FairnessKey]++
	}
	require.Equal(t, 1, byKey["A"], "weight 1")
	require.Equal(t, 2, byKey["B"], "weight 2")
	require.Empty(t, *failures)
}

// TestAdmissionState_AgingUnderCeiling: when the engine ceiling is the binding
// constraint, the run that waited longest is admitted first, so a background
// run cannot starve behind newer interactive runs (#546).
func TestAdmissionState_AgingUnderCeiling(t *testing.T) {
	t.Parallel()
	s := admissionState()
	s.Capacities = []AdmissionCapacity{
		{Class: queryengine.ClassInteractive, MaxRunning: 2, MaxQueued: 10, QueueTimeout: time.Hour},
		{Class: queryengine.ClassBackground, MaxRunning: 1, MaxQueued: 10, QueueTimeout: time.Hour},
	}
	s.MaxRunningTotal = 2
	now := time.Unix(0, 0)
	failures, fail := collectFailures()

	s.request(now, AdmissionRequest{ExecutionID: "bg", Class: queryengine.ClassBackground, FairnessKey: "ws"}, fail)
	s.request(now.Add(time.Minute), AdmissionRequest{ExecutionID: "i1", Class: queryengine.ClassInteractive, FairnessKey: "ws"}, fail)
	s.request(now.Add(2*time.Minute), AdmissionRequest{ExecutionID: "i2", Class: queryengine.ClassInteractive, FairnessKey: "ws"}, fail)

	leases := s.admitAll()
	require.Len(t, leases, 2)
	require.Equal(t, "bg", leases[0].ExecutionID, "the oldest run goes first under the shared ceiling")
	require.Equal(t, "i1", leases[1].ExecutionID)
	require.Empty(t, *failures)
}

// TestAdmissionState_WithinKeyIsFIFO: aging keeps a key's runs in arrival order.
func TestAdmissionState_WithinKeyIsFIFO(t *testing.T) {
	t.Parallel()
	s := admissionState()
	now := time.Unix(0, 0)
	failures, fail := collectFailures()

	s.request(now, AdmissionRequest{ExecutionID: "a", Class: queryengine.ClassInteractive, FairnessKey: "ws"}, fail)
	s.request(now.Add(time.Second), AdmissionRequest{ExecutionID: "b", Class: queryengine.ClassInteractive, FairnessKey: "ws"}, fail)
	s.request(now.Add(2*time.Second), AdmissionRequest{ExecutionID: "c", Class: queryengine.ClassInteractive, FairnessKey: "ws"}, fail)

	require.Len(t, s.admitAll(), 1)
	s.release("a")
	require.Len(t, s.admitAll(), 1)
	require.Equal(t, "b", s.Running[0].ExecutionID)
	require.Empty(t, *failures)
}

// TestAdmissionWorkflow_ThrottleAppliesLive: the adaptive gate admits only
// interactive work while throttled, and lifts without a restart (#551, #579).
func TestAdmissionWorkflow_ThrottleAppliesLive(t *testing.T) {
	t.Parallel()
	env, admitted, _ := newAdmissionEnv(t)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdmissionThrottleSignal, AdmissionThrottle{Throttled: true, Reason: "cluster:queued"})
		env.SignalWorkflow(AdmissionRequestSignal, AdmissionRequest{ExecutionID: "h", Class: queryengine.ClassHeavy})
		env.SignalWorkflow(AdmissionRequestSignal, AdmissionRequest{ExecutionID: "i", Class: queryengine.ClassInteractive})
	}, time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AdmissionThrottleSignal, AdmissionThrottle{Throttled: false})
	}, time.Second)
	env.RegisterDelayedCallback(env.CancelWorkflow, 2*time.Second)
	env.ExecuteWorkflow(AdmissionWorkflow, "e1", admissionState())

	require.Len(t, *admitted, 2)
	require.Equal(t, "i", (*admitted)[0].ExecutionID, "only interactive is admitted while throttled")
	require.Equal(t, "h", (*admitted)[1].ExecutionID, "heavy starts once the gate lifts")
}

// TestAdmissionState_PerKeyCap: an agent's MaxConcurrentQueries caps its own
// runs inside the class; another key can use the free slot, and the capped
// run starts when its key frees one (#589).
func TestAdmissionState_PerKeyCap(t *testing.T) {
	t.Parallel()
	s := admissionState()
	s.Capacities = []AdmissionCapacity{
		{Class: queryengine.ClassInteractive, MaxRunning: 3, MaxQueued: 10, QueueTimeout: time.Hour},
	}
	now := time.Unix(0, 0)
	failures, fail := collectFailures()

	s.request(now, AdmissionRequest{ExecutionID: "a1", Class: queryengine.ClassInteractive, FairnessKey: "A", MaxRunning: 1}, fail)
	require.Len(t, s.admitAll(), 1)
	s.request(now.Add(time.Second), AdmissionRequest{ExecutionID: "a2", Class: queryengine.ClassInteractive, FairnessKey: "A", MaxRunning: 1}, fail)
	s.request(now.Add(2*time.Second), AdmissionRequest{ExecutionID: "b1", Class: queryengine.ClassInteractive, FairnessKey: "B"}, fail)

	leases := s.admitAll()
	require.Len(t, leases, 1)
	require.Equal(t, "b1", leases[0].ExecutionID, "the capped key must not take the next slot")
	require.Len(t, s.Queued, 1, "the capped run waits")

	s.release("a1")
	leases = s.admitAll()
	require.Len(t, leases, 1)
	require.Equal(t, "a2", leases[0].ExecutionID, "the capped run starts when its key frees a slot")
	require.Empty(t, *failures)
}

// TestAdmissionState_CappedRequestRespectsQueueBound: with a full queue, a
// capped arrival is refused instead of queued, even when the class has a free
// slot (#589).
func TestAdmissionState_CappedRequestRespectsQueueBound(t *testing.T) {
	t.Parallel()
	s := admissionState()
	s.Capacities = []AdmissionCapacity{
		{Class: queryengine.ClassInteractive, MaxRunning: 2, MaxQueued: 0, QueueTimeout: time.Hour},
	}
	now := time.Unix(0, 0)
	failures, fail := collectFailures()

	s.request(now, AdmissionRequest{ExecutionID: "a1", Class: queryengine.ClassInteractive, FairnessKey: "A", MaxRunning: 1}, fail)
	require.Len(t, s.admitAll(), 1)
	// The class has room, but the key is capped and the queue is closed.
	s.request(now.Add(time.Second), AdmissionRequest{ExecutionID: "a2", Class: queryengine.ClassInteractive, FairnessKey: "A", MaxRunning: 1}, fail)
	require.Empty(t, s.Queued)
	require.Len(t, *failures, 1)
	require.Equal(t, "queue_full", (*failures)[0].Code)
}
