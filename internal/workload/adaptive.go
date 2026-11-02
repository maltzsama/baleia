package workload

// Adaptive admission (#551, #579): when the Trino cluster is under pressure,
// the admission workflow only admits interactive work until the pressure
// drops. The mode is driven by a signal, so it is reversible without a
// restart.

// AdmissionThrottleSignal is the signal that sets the adaptive gate. It lives
// here (not in the workflows package) so the Lens poll activity, which cannot
// import workflows, can send it.
const AdmissionThrottleSignal = "set-throttle"

// AdmissionThrottle is the adaptive gate's mode and why it changed.
type AdmissionThrottle struct {
	Throttled bool
	Reason    string
}

// AdmissionWorkflowID is the id of an engine's admission workflow (#399). The
// workflows package re-exports it from here so the activity and the workflow
// agree without an import cycle.
func AdmissionWorkflowID(engineID string) string { return "admission-" + engineID }

// AdaptiveThresholds are the conservative defaults of the adaptive gate. A
// zero field disables that trigger.
type AdaptiveThresholds struct {
	// QueuedQueries throttles when the cluster's queued queries exceed it.
	QueuedQueries int
	// BlockedQueries throttles when queries held by locks/contention exceed it.
	BlockedQueries int
	// MemoryHeadroom throttles when free/total memory falls below it (0..1).
	MemoryHeadroom float64
}

// DefaultAdaptiveThresholds are the shipped defaults: the queued-query gate is
// the primary trigger (#551); the blocked and memory gates catch saturation
// that the queue length does not show (#579).
func DefaultAdaptiveThresholds() AdaptiveThresholds {
	return AdaptiveThresholds{QueuedQueries: 20, BlockedQueries: 10, MemoryHeadroom: 0.10}
}

// ClusterPressure is the slice of the latest cluster snapshot the gate reads.
type ClusterPressure struct {
	QueuedQueries    int
	BlockedQueries   int
	TotalMemoryBytes *int64
	FreeMemoryBytes  *int64
}

// Throttled reports whether the snapshot crosses a threshold, and the reason
// stored on the gate. The triggers are evaluated in a fixed order so the
// reason is deterministic.
func (t AdaptiveThresholds) Throttled(p ClusterPressure) (bool, string) {
	if t.QueuedQueries > 0 && p.QueuedQueries > t.QueuedQueries {
		return true, "cluster:queued"
	}
	if t.BlockedQueries > 0 && p.BlockedQueries > t.BlockedQueries {
		return true, "cluster:blocked"
	}
	if t.MemoryHeadroom > 0 && p.TotalMemoryBytes != nil && p.FreeMemoryBytes != nil && *p.TotalMemoryBytes > 0 {
		headroom := float64(*p.FreeMemoryBytes) / float64(*p.TotalMemoryBytes)
		if headroom < t.MemoryHeadroom {
			return true, "cluster:memory"
		}
	}
	return false, ""
}
