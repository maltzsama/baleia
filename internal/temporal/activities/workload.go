package activities

import (
	"context"
	"time"
)

// RecordQueueStateParams is the live depth and running count of one
// (engine, class) queue (#399).
type RecordQueueStateParams struct {
	EngineID string
	Class    string
	Running  int
	Queued   int
}

// RecordQueueState sets the queue's depth and running gauges. Best effort:
// the admission workflow reports after every state change.
func RecordQueueState(ctx context.Context, params RecordQueueStateParams) error {
	defaultActivities.recordQueueState(params)
	return nil
}

func (a *Activities) recordQueueState(params RecordQueueStateParams) {
	a.metrics.workload.State(params.EngineID, params.Class, params.Running, params.Queued)
}

// RecordQueueAdmissionParams is one run admitted from an engine's queue.
type RecordQueueAdmissionParams struct {
	EngineID string
	Class    string
	WaitMs   int64
}

// RecordQueueAdmission counts one admission and observes its queue wait.
func RecordQueueAdmission(ctx context.Context, params RecordQueueAdmissionParams) error {
	defaultActivities.recordQueueAdmission(params)
	return nil
}

func (a *Activities) recordQueueAdmission(params RecordQueueAdmissionParams) {
	a.metrics.workload.Admitted(params.EngineID, params.Class, time.Duration(params.WaitMs)*time.Millisecond)
}

// RecordQueueTimeoutParams is one run that timed out waiting.
type RecordQueueTimeoutParams struct {
	EngineID string
	Class    string
}

// RecordQueueTimeout counts one queue timeout.
func RecordQueueTimeout(ctx context.Context, params RecordQueueTimeoutParams) error {
	defaultActivities.recordQueueTimeout(params)
	return nil
}

func (a *Activities) recordQueueTimeout(params RecordQueueTimeoutParams) {
	a.metrics.workload.Timeout(params.EngineID, params.Class)
}
