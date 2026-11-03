package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/galileostudio/baleia/internal/workload"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Signals and queries of the per-engine admission workflow (#399, ADR 0014).
const (
	// AdmissionRequestSignal asks for a slot: AdmissionRequest.
	AdmissionRequestSignal = "request-slot"
	// AdmissionReleaseSignal frees a slot: AdmissionRelease.
	AdmissionReleaseSignal = "release-slot"
	// AdmissionCancelSignal drops a queued request: AdmissionCancel.
	AdmissionCancelSignal = "cancel-slot"
	// AdmissionCapacitySignal replaces the capacities: AdmissionCapacities.
	AdmissionCapacitySignal = "set-capacity"
	// AdmissionThrottleSignal sets the adaptive gate (#551, #579).
	AdmissionThrottleSignal = workload.AdmissionThrottleSignal

	// AdmittedSignal is sent to the execution workflow when it may run.
	AdmittedSignal = "admitted"
	// AdmissionFailedSignal is sent when the run will never be admitted:
	// AdmissionFailure.
	AdmissionFailedSignal = "admission-failed"

	// AdmissionStateQuery returns the live AdmissionState.
	AdmissionStateQuery = "admission-state"

	// admissionHistoryLimit is how many events one run may carry before it
	// continues as new with its queues and leases.
	admissionHistoryLimit = 2000
)

// Failure codes an execution receives when it will never be admitted. They
// mirror the API's ErrorCode values without importing the generated package.
const (
	AdmissionFailureQueueFull    = "queue_full"
	AdmissionFailureQueueTimeout = "queue_timeout"
	AdmissionFailureCancelled    = "cancelled"
)

// AdmissionWorkflowID is the id of an engine's admission workflow. One entity
// workflow per engine serializes its admission, so API replicas do not matter.
func AdmissionWorkflowID(engineID string) string { return workload.AdmissionWorkflowID(engineID) }

// ExecutionWorkflowID is the id of an execution's workflow. The admission
// workflow signals it by this id.
func ExecutionWorkflowID(executionID string) string { return "exec-" + executionID }

// AdmissionRequest asks the engine's queue for a slot. EnqueuedAt is set by
// the workflow, not the caller, so the queue is ordered by server time.
type AdmissionRequest struct {
	ExecutionID  string
	Class        queryengine.WorkloadClass
	FairnessKey  string
	Weight       int
	QueueTimeout time.Duration
	// MaxRunning caps how many runs of this fairness key may run in the class
	// at once (#589). Zero means only the class capacity applies. Agent runs
	// set it to the policy's MaxConcurrentQueries.
	MaxRunning int
}

// AdmissionRelease frees the slot of a finished, failed or cancelled run.
type AdmissionRelease struct {
	ExecutionID string
}

// AdmissionCancel drops a run that is still queued.
type AdmissionCancel struct {
	ExecutionID string
}

// AdmissionCapacity is the capacity of one class on one engine.
type AdmissionCapacity struct {
	Class        queryengine.WorkloadClass
	MaxRunning   int
	MaxQueued    int
	QueueTimeout time.Duration
}

// AdmissionCapacities are every class's capacity plus the optional ceiling.
type AdmissionCapacities struct {
	MaxRunningTotal int
	Classes         []AdmissionCapacity
}

// AdmissionLease is a slot held by one execution. It is held for the whole
// execution, because every statement shares one engine session.
type AdmissionLease struct {
	ExecutionID string
	Class       queryengine.WorkloadClass
	FairnessKey string
	AdmittedAt  time.Time
}

// AdmissionQueued is one waiting run.
type AdmissionQueued struct {
	ExecutionID string
	Class       queryengine.WorkloadClass
	FairnessKey string
	Weight      int
	// MaxRunning is the per-key cap the request carried (#589).
	MaxRunning int
	EnqueuedAt time.Time
	Deadline   time.Time
}

// AdmissionFailure tells a waiting execution why it will not run.
type AdmissionFailure struct {
	ExecutionID string
	Code        string // queue_full | queue_timeout | cancelled
	Message     string
}

// AdmissionThrottle is the adaptive gate's mode (#551, #579).
type AdmissionThrottle = workload.AdmissionThrottle

// AdmissionState is both the workflow's input (first start and
// continue-as-new) and the query's answer.
type AdmissionState struct {
	MaxRunningTotal int
	Capacities      []AdmissionCapacity
	Running         []AdmissionLease
	Queued          []AdmissionQueued
	// Throttled is the adaptive gate: only interactive work is admitted while
	// the cluster is under pressure (#551, #579).
	Throttled      bool
	ThrottleReason string
}

// AdmissionWorkflow is the per-engine entity workflow. It keeps the per-class
// queues, the running leases and the engine ceiling, and signals the waiting
// execution workflow when it may run or when it gives up.
//
// Ordering shares a class's slots across fairness keys in proportion to their
// weight, and ages a waiting run so the engine ceiling cannot starve a
// background run (#546). Within one key the order is FIFO; across the classes
// that can admit at once, the run that waited longest goes first.
func AdmissionWorkflow(ctx workflow.Context, engineID string, in AdmissionState) error {
	logger := workflow.GetLogger(ctx)
	state := in.copy()
	if len(state.Capacities) == 0 {
		state.Capacities = DefaultAdmissionCapacities()
	}

	requestCh := workflow.GetSignalChannel(ctx, AdmissionRequestSignal)
	releaseCh := workflow.GetSignalChannel(ctx, AdmissionReleaseSignal)
	cancelCh := workflow.GetSignalChannel(ctx, AdmissionCancelSignal)
	capacityCh := workflow.GetSignalChannel(ctx, AdmissionCapacitySignal)
	throttleCh := workflow.GetSignalChannel(ctx, AdmissionThrottleSignal)

	// From version 1 a cancelled workflow returns (#639). Before it, with a run
	// still queued, the queue-deadline timer fired at once on the cancelled
	// context and the loop spun without yielding; older histories replay that.
	stopOnCancel := workflow.GetVersion(ctx, "admission-stop-on-cancel", workflow.DefaultVersion, 1) == 1

	// Queue metrics are best-effort; a lost sample must not block admission.
	actx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})

	if err := workflow.SetQueryHandler(ctx, AdmissionStateQuery, func() (AdmissionState, error) {
		return state.copy(), nil
	}); err != nil {
		return err
	}

	for {
		if stopOnCancel && ctx.Err() != nil {
			return ctx.Err()
		}
		for _, lease := range state.admitAll() {
			if err := signalExecution(ctx, lease.ExecutionID, AdmittedSignal, lease); err != nil {
				// The execution never learned it had a slot; do not leak it.
				logger.Warn("could not signal admission", "execution_id", lease.ExecutionID, "err", err)
				state.release(lease.ExecutionID)
				continue
			}
			// The lease's AdmittedAt is the enqueued time; the wait is how long
			// it stood in the queue before a slot freed.
			wait := workflow.Now(ctx).Sub(lease.AdmittedAt)
			if wait < 0 {
				wait = 0
			}
			_ = workflow.ExecuteActivity(actx, activities.RecordQueueAdmission, activities.RecordQueueAdmissionParams{
				EngineID: engineID,
				Class:    string(lease.Class),
				WaitMs:   wait.Milliseconds(),
			})
		}
		recordGauges(actx, engineID, &state)

		if workflow.GetInfo(ctx).GetCurrentHistoryLength() > admissionHistoryLimit {
			return workflow.NewContinueAsNewError(ctx, AdmissionWorkflow, engineID, state.copy())
		}

		sel := workflow.NewSelector(ctx)
		sel.AddReceive(requestCh, func(c workflow.ReceiveChannel, _ bool) {
			var req AdmissionRequest
			c.Receive(ctx, &req)
			state.request(workflow.Now(ctx), req, func(f AdmissionFailure) {
				if err := signalExecution(ctx, f.ExecutionID, AdmissionFailedSignal, f); err != nil {
					logger.Warn("could not signal admission failure", "execution_id", f.ExecutionID, "code", f.Code, "err", err)
				}
			})
		})
		sel.AddReceive(releaseCh, func(c workflow.ReceiveChannel, _ bool) {
			var rel AdmissionRelease
			c.Receive(ctx, &rel)
			state.release(rel.ExecutionID)
		})
		sel.AddReceive(cancelCh, func(c workflow.ReceiveChannel, _ bool) {
			var can AdmissionCancel
			c.Receive(ctx, &can)
			if state.cancel(can.ExecutionID) {
				f := AdmissionFailure{ExecutionID: can.ExecutionID, Code: AdmissionFailureCancelled, Message: "the run was cancelled while queued"}
				if err := signalExecution(ctx, f.ExecutionID, AdmissionFailedSignal, f); err != nil {
					logger.Warn("could not signal queue cancellation", "execution_id", f.ExecutionID, "err", err)
				}
			}
		})
		sel.AddReceive(capacityCh, func(c workflow.ReceiveChannel, _ bool) {
			var caps AdmissionCapacities
			c.Receive(ctx, &caps)
			state.setCapacities(caps)
		})
		sel.AddReceive(throttleCh, func(c workflow.ReceiveChannel, _ bool) {
			var throttle AdmissionThrottle
			c.Receive(ctx, &throttle)
			state.Throttled = throttle.Throttled
			state.ThrottleReason = throttle.Reason
		})
		if deadline, ok := state.nextDeadline(); ok {
			delay := deadline.Sub(workflow.Now(ctx))
			if delay < 0 {
				delay = 0
			}
			sel.AddFuture(workflow.NewTimer(ctx, delay), func(workflow.Future) {
				for _, q := range state.expire(workflow.Now(ctx)) {
					f := AdmissionFailure{ExecutionID: q.ExecutionID, Code: AdmissionFailureQueueTimeout, Message: "the run waited too long in the queue"}
					if err := signalExecution(ctx, q.ExecutionID, AdmissionFailedSignal, f); err != nil {
						logger.Warn("could not signal queue timeout", "execution_id", q.ExecutionID, "err", err)
					}
					_ = workflow.ExecuteActivity(actx, activities.RecordQueueTimeout, activities.RecordQueueTimeoutParams{
						EngineID: engineID,
						Class:    string(q.Class),
					})
				}
			})
		}
		sel.Select(ctx)
	}
}

// DefaultAdmissionCapacities is the conservative start for a federated
// (Trino) engine; the API resolves the per-engine defaults from the workload
// package and sends them with request or set-capacity.
func DefaultAdmissionCapacities() []AdmissionCapacity {
	return DefaultAdmissionCapacitiesFor("federated")
}

// DefaultAdmissionCapacitiesFor returns the defaults of one engine class
// ("federated" or "direct"), in the fixed class order the API reports.
func DefaultAdmissionCapacitiesFor(engineClass string) []AdmissionCapacity {
	defaults := workload.Defaults(engineClass)
	classes := []queryengine.WorkloadClass{
		queryengine.ClassInteractive,
		queryengine.ClassHeavy,
		queryengine.ClassBackground,
	}
	out := make([]AdmissionCapacity, 0, len(classes))
	for _, class := range classes {
		capacity := defaults[class]
		out = append(out, AdmissionCapacity{
			Class:        class,
			MaxRunning:   capacity.MaxRunning,
			MaxQueued:    capacity.MaxQueued,
			QueueTimeout: capacity.QueueTimeout,
		})
	}
	return out
}

func signalExecution(ctx workflow.Context, executionID, signal string, arg any) error {
	return workflow.SignalExternalWorkflow(ctx, ExecutionWorkflowID(executionID), "", signal, arg).Get(ctx, nil)
}

// recordGauges reports the live depth and running count of every class. The
// activities are fired without waiting: a lost sample must not delay admission.
func recordGauges(ctx workflow.Context, engineID string, state *AdmissionState) {
	for _, class := range []queryengine.WorkloadClass{
		queryengine.ClassInteractive,
		queryengine.ClassHeavy,
		queryengine.ClassBackground,
	} {
		_ = workflow.ExecuteActivity(ctx, activities.RecordQueueState, activities.RecordQueueStateParams{
			EngineID: engineID,
			Class:    string(class),
			Running:  state.runningInClass(class),
			Queued:   state.queuedInClass(class),
		})
	}
}

// request enqueues a run, immediately admits it when a slot is free, or
// reports why it cannot. A duplicate request is a no-op.
func (s *AdmissionState) request(now time.Time, req AdmissionRequest, fail func(AdmissionFailure)) {
	if req.ExecutionID == "" || s.isRunning(req.ExecutionID) || s.isQueued(req.ExecutionID) {
		return
	}
	capacity, ok := s.capacity(req.Class)
	if !ok {
		fail(AdmissionFailure{ExecutionID: req.ExecutionID, Code: AdmissionFailureQueueFull, Message: "unknown workload class"})
		return
	}
	if capacity.MaxRunning == 0 {
		fail(AdmissionFailure{ExecutionID: req.ExecutionID, Code: AdmissionFailureQueueFull, Message: "the class is closed on this engine"})
		return
	}
	// max_queued bounds waiting runs; a run that can start right away does not
	// wait and is not counted. A per-key cap counts as "cannot start" here, so
	// a capped arrival is refused once the queue is full (#589).
	if s.queuedInClass(req.Class) >= capacity.MaxQueued && !s.canAdmitRequest(req) {
		fail(AdmissionFailure{ExecutionID: req.ExecutionID, Code: AdmissionFailureQueueFull, Message: "the queue for this class is full"})
		return
	}
	timeout := req.QueueTimeout
	if timeout <= 0 {
		timeout = capacity.QueueTimeout
	}
	var deadline time.Time
	if timeout > 0 {
		deadline = now.Add(timeout)
	}
	s.Queued = append(s.Queued, AdmissionQueued{
		ExecutionID: req.ExecutionID,
		Class:       req.Class,
		FairnessKey: req.FairnessKey,
		Weight:      req.Weight,
		MaxRunning:  req.MaxRunning,
		EnqueuedAt:  now,
		Deadline:    deadline,
	})
}

// admitAll admits until no class has room.
func (s *AdmissionState) admitAll() []AdmissionLease {
	var leases []AdmissionLease
	for lease, ok := s.admitOne(); ok; lease, ok = s.admitOne() {
		leases = append(leases, lease)
	}
	return leases
}

// admitOne admits one run. Each class that still has a slot offers its best
// waiting run; the one that waited longest wins, so the engine ceiling cannot
// starve a class (#546).
func (s *AdmissionState) admitOne() (AdmissionLease, bool) {
	if s.MaxRunningTotal > 0 && len(s.Running) >= s.MaxRunningTotal {
		return AdmissionLease{}, false
	}
	best := -1
	seen := map[queryengine.WorkloadClass]bool{}
	for _, q := range s.Queued {
		if seen[q.Class] {
			continue
		}
		seen[q.Class] = true
		// Under pressure only interactive work is admitted; heavy and
		// background wait for the gate to lift (#551, #579).
		if s.Throttled && q.Class != queryengine.ClassInteractive {
			continue
		}
		idx := s.bestInClass(q.Class)
		if idx < 0 {
			continue
		}
		if best == -1 || s.Queued[idx].EnqueuedAt.Before(s.Queued[best].EnqueuedAt) {
			best = idx
		}
	}
	if best == -1 {
		return AdmissionLease{}, false
	}
	q := s.Queued[best]
	s.Queued = append(s.Queued[:best], s.Queued[best+1:]...)
	lease := AdmissionLease{ExecutionID: q.ExecutionID, Class: q.Class, FairnessKey: q.FairnessKey, AdmittedAt: q.EnqueuedAt}
	s.Running = append(s.Running, lease)
	return lease, true
}

// bestInClass returns the index of the waiting run a class should admit next,
// or -1 when the class has no free slot. Within a class, the run whose
// fairness key is furthest below its weighted share goes first; ties (and
// runs of the same key) keep FIFO order, which is how a long wait raises a
// run's effective priority (#546).
func (s *AdmissionState) bestInClass(class queryengine.WorkloadClass) int {
	capacity, ok := s.capacity(class)
	if !ok || capacity.MaxRunning <= 0 || s.runningInClass(class) >= capacity.MaxRunning {
		return -1
	}
	runningByKey := map[string]int{}
	for _, lease := range s.Running {
		if lease.Class == class {
			runningByKey[lease.FairnessKey]++
		}
	}
	best := -1
	bestRunning, bestWeight := 0, 1
	for i, q := range s.Queued {
		if q.Class != class {
			continue
		}
		// A per-key cap (an agent's MaxConcurrentQueries, #589) keeps that
		// key's runs waiting even when the class has a free slot.
		if q.MaxRunning > 0 && runningByKey[q.FairnessKey] >= q.MaxRunning {
			continue
		}
		weight := q.Weight
		if weight < 1 {
			weight = 1
		}
		running := runningByKey[q.FairnessKey]
		if best == -1 {
			best, bestRunning, bestWeight = i, running, weight
			continue
		}
		// Compare running/weight without floats: a/b < c/d iff a*d < c*b.
		left := running * bestWeight
		right := bestRunning * weight
		switch {
		case left < right:
			best, bestRunning, bestWeight = i, running, weight
		case left == right && q.EnqueuedAt.Before(s.Queued[best].EnqueuedAt):
			best, bestRunning, bestWeight = i, running, weight
		}
	}
	return best
}

func (s *AdmissionState) release(executionID string) {
	for i, lease := range s.Running {
		if lease.ExecutionID == executionID {
			s.Running = append(s.Running[:i], s.Running[i+1:]...)
			return
		}
	}
}

// cancel drops a queued run and reports whether it was queued.
func (s *AdmissionState) cancel(executionID string) bool {
	for i, q := range s.Queued {
		if q.ExecutionID == executionID {
			s.Queued = append(s.Queued[:i], s.Queued[i+1:]...)
			return true
		}
	}
	return false
}

func (s *AdmissionState) setCapacities(caps AdmissionCapacities) {
	s.MaxRunningTotal = caps.MaxRunningTotal
	s.Capacities = append([]AdmissionCapacity(nil), caps.Classes...)
}

func (s *AdmissionState) capacity(class queryengine.WorkloadClass) (AdmissionCapacity, bool) {
	for _, c := range s.Capacities {
		if c.Class == class {
			return c, true
		}
	}
	return AdmissionCapacity{}, false
}

func (s *AdmissionState) canAdmit(class queryengine.WorkloadClass) bool {
	if s.MaxRunningTotal > 0 && len(s.Running) >= s.MaxRunningTotal {
		return false
	}
	capacity, ok := s.capacity(class)
	return ok && capacity.MaxRunning > 0 && s.runningInClass(class) < capacity.MaxRunning
}

// canAdmitRequest is canAdmit plus the request's per-key cap (#589).
func (s *AdmissionState) canAdmitRequest(req AdmissionRequest) bool {
	if !s.canAdmit(req.Class) {
		return false
	}
	return req.MaxRunning <= 0 || s.runningForKey(req.Class, req.FairnessKey) < req.MaxRunning
}

// runningForKey counts the leases of one fairness key in a class.
func (s *AdmissionState) runningForKey(class queryengine.WorkloadClass, key string) int {
	n := 0
	for _, lease := range s.Running {
		if lease.Class == class && lease.FairnessKey == key {
			n++
		}
	}
	return n
}

func (s *AdmissionState) runningInClass(class queryengine.WorkloadClass) int {
	n := 0
	for _, lease := range s.Running {
		if lease.Class == class {
			n++
		}
	}
	return n
}

func (s *AdmissionState) queuedInClass(class queryengine.WorkloadClass) int {
	n := 0
	for _, q := range s.Queued {
		if q.Class == class {
			n++
		}
	}
	return n
}

func (s *AdmissionState) isRunning(executionID string) bool {
	for _, lease := range s.Running {
		if lease.ExecutionID == executionID {
			return true
		}
	}
	return false
}

func (s *AdmissionState) isQueued(executionID string) bool {
	for _, q := range s.Queued {
		if q.ExecutionID == executionID {
			return true
		}
	}
	return false
}

// expire removes the runs whose deadline passed and returns them.
func (s *AdmissionState) expire(now time.Time) []AdmissionQueued {
	var expired []AdmissionQueued
	kept := s.Queued[:0]
	for _, q := range s.Queued {
		if !q.Deadline.IsZero() && !now.Before(q.Deadline) {
			expired = append(expired, q)
			continue
		}
		kept = append(kept, q)
	}
	s.Queued = kept
	return expired
}

func (s *AdmissionState) nextDeadline() (time.Time, bool) {
	var next time.Time
	for _, q := range s.Queued {
		if q.Deadline.IsZero() {
			continue
		}
		if next.IsZero() || q.Deadline.Before(next) {
			next = q.Deadline
		}
	}
	return next, !next.IsZero()
}

func (s AdmissionState) copy() AdmissionState {
	out := s
	out.Capacities = append([]AdmissionCapacity(nil), s.Capacities...)
	out.Running = append([]AdmissionLease(nil), s.Running...)
	out.Queued = append([]AdmissionQueued(nil), s.Queued...)
	return out
}
