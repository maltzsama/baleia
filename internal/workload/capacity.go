// Package workload holds the parts of workload management (#399, ADR 0014)
// that are pure policy: the default capacities and the rule that a missing
// per-engine row falls back to them. The admission workflow and the API use
// these values; nothing here talks to the database or to Temporal.
package workload

import (
	"time"

	"github.com/galileostudio/baleia/internal/queryengine"
)

// Capacity is how one (engine, class) queue is allowed to run and wait.
//
// QueueTimeout bounds the *wait* before a run enters the engine; RuntimeTimeout
// bounds how long it may *execute* once admitted. The second is what stops a
// background run that waited 24h from being killed by the global statement
// timeout after 30 minutes.
type Capacity struct {
	MaxRunning     int
	MaxQueued      int
	QueueTimeout   time.Duration
	RuntimeTimeout time.Duration
}

// Defaults returns the conservative capacity of every class for an engine
// class ("direct" or "federated"). A federated engine is Trino today, so its
// defaults are Trino's. A missing row in engine_queue_capacity means these.
func Defaults(engineClass string) map[queryengine.WorkloadClass]Capacity {
	if engineClass == "direct" {
		return map[queryengine.WorkloadClass]Capacity{
			queryengine.ClassInteractive: {MaxRunning: 4, MaxQueued: 20, QueueTimeout: 5 * time.Minute, RuntimeTimeout: 5 * time.Minute},
			queryengine.ClassHeavy:       {MaxRunning: 1, MaxQueued: 10, QueueTimeout: 30 * time.Minute, RuntimeTimeout: 30 * time.Minute},
			queryengine.ClassBackground:  {MaxRunning: 1, MaxQueued: 20, QueueTimeout: 24 * time.Hour, RuntimeTimeout: 24 * time.Hour},
		}
	}
	return map[queryengine.WorkloadClass]Capacity{
		queryengine.ClassInteractive: {MaxRunning: 20, MaxQueued: 200, QueueTimeout: 5 * time.Minute, RuntimeTimeout: 5 * time.Minute},
		queryengine.ClassHeavy:       {MaxRunning: 3, MaxQueued: 50, QueueTimeout: 30 * time.Minute, RuntimeTimeout: 30 * time.Minute},
		queryengine.ClassBackground:  {MaxRunning: 2, MaxQueued: 100, QueueTimeout: 24 * time.Hour, RuntimeTimeout: 24 * time.Hour},
	}
}

// Resolve returns the configured capacity when one exists, otherwise the
// engine class's default for that workload class. A configured row may leave
// the runtime bound unset (NULL); it then falls back to the default. The
// schema forbids 0, so 0 is unambiguously "unset".
func Resolve(engineClass string, class queryengine.WorkloadClass, configured *Capacity) Capacity {
	defaults := Defaults(engineClass)[class]
	if configured == nil {
		return defaults
	}
	out := *configured
	if out.RuntimeTimeout == 0 {
		out.RuntimeTimeout = defaults.RuntimeTimeout
	}
	return out
}
