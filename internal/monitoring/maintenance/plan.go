package maintenance

import (
	"errors"
	"time"
)

// Metrics is what the health pass measured on a table. A nil metric could not
// be read and never fires a trigger.
type Metrics struct {
	DataFiles         *int64
	SmallFiles        *int64
	DeleteFiles       *int64
	Manifests         *int64
	Snapshots         *int64
	OldestSnapshotAge *time.Duration
}

// State is what the table's runs say: the last successful run of each
// operation, and whether any run is in progress.
type State struct {
	LastSuccess map[string]time.Time
	Running     bool
	// Since is when the parameters took effect: an operation that never ran
	// counts its maximum interval from here. Zero counts it as passed.
	Since time.Time
}

// Kind is what asks for a plan.
type Kind string

// Plan kinds.
const (
	Automatic Kind = "automatic"
	Manual    Kind = "manual"
	DryRun    Kind = "dry_run"
)

// ErrMaintainedElsewhere is returned for a table its own writer maintains:
// Baleia only analyzes it.
var ErrMaintainedElsewhere = errors.New("table is maintained elsewhere")

// Why an operation is not due.
const (
	BlockedDisabled    = "disabled"
	BlockedMode        = "mode"
	BlockedNotFired    = "not_fired"
	BlockedMinInterval = "min_interval"
	BlockedWindow      = "window"
	BlockedRunning     = "running"
)

// Reason explains a step: which trigger fired (with the measured value and
// its threshold, or the elapsed time against the maximum interval), or what
// blocks the operation.
type Reason struct {
	Fired              string     `json:"fired,omitempty"`
	Measured           *float64   `json:"measured,omitempty"`
	Threshold          *float64   `json:"threshold,omitempty"`
	ElapsedSeconds     *int64     `json:"elapsed_seconds,omitempty"`
	MaxIntervalSeconds *int64     `json:"max_interval_seconds,omitempty"`
	MinIntervalSeconds *int64     `json:"min_interval_seconds,omitempty"`
	LastSuccessAt      *time.Time `json:"last_success_at,omitempty"`
	Blocked            string     `json:"blocked,omitempty"`
}

// Step is one operation in a plan.
type Step struct {
	Operation string `json:"operation"`
	Due       bool   `json:"due"`
	// WouldRun marks the one operation this round runs on the table.
	WouldRun bool   `json:"would_run"`
	Reason   Reason `json:"reason"`
	// WaitsFor names the operation that runs first when this one is due too.
	WaitsFor string `json:"waits_for,omitempty"`
}

// Plan is every operation of a table, in priority order.
type Plan struct {
	Steps []Step `json:"plan"`
}

// Next is the operation the plan runs, if any.
func (p Plan) Next() (Step, bool) {
	for _, s := range p.Steps {
		if s.WouldRun {
			return s, true
		}
	}
	return Step{}, false
}

// Evaluate decides, at now, which operations of a table are due and which one
// runs. An overdue operation (its maximum interval passed) runs before one
// whose trigger merely fired, so an operation due every round cannot starve
// the others; within each group Priority decides. An automatic round needs
// managed mode; a manual or dry run also accepts off; neither touches a
// table maintained elsewhere.
func Evaluate(p Policy, m Metrics, st State, kind Kind, now time.Time) (Plan, error) {
	if p.Mode == ModeMaintainedElsewhere {
		return Plan{}, ErrMaintainedElsewhere
	}
	modeOK := p.Mode == ModeManaged || (kind != Automatic && p.Mode == ModeOff)
	plan := Plan{Steps: make([]Step, 0, len(Priority))}
	for _, op := range Priority {
		s := p.Operation(op)
		step := Step{Operation: op}
		last, ran := st.LastSuccess[op]
		if ran {
			at := last
			step.Reason.LastSuccessAt = &at
		}
		switch {
		case !s.Enabled:
			step.Reason.Blocked = BlockedDisabled
		case !modeOK:
			step.Reason.Blocked = BlockedMode
		default:
			step.Reason = fired(op, s, m, last, ran, st.Since, now, step.Reason)
			switch {
			case step.Reason.Fired == "":
				step.Reason.Blocked = BlockedNotFired
			case ran && now.Sub(last) < seconds(s.MinIntervalSeconds):
				step.Reason.Blocked = BlockedMinInterval
				minInterval := s.MinIntervalSeconds
				step.Reason.MinIntervalSeconds = &minInterval
			case !inWindow(s.Window, now):
				step.Reason.Blocked = BlockedWindow
			case st.Running:
				step.Reason.Blocked = BlockedRunning
			default:
				step.Due = true
			}
		}
		plan.Steps = append(plan.Steps, step)
	}
	choose(plan.Steps)
	return plan, nil
}

// choose marks the step that runs: the first overdue one, else the first due
// one; every other due step waits for it.
func choose(steps []Step) {
	pick := -1
	for i, s := range steps {
		if s.Due && s.Reason.Fired == FiredMaxInterval {
			pick = i
			break
		}
	}
	if pick < 0 {
		for i, s := range steps {
			if s.Due {
				pick = i
				break
			}
		}
	}
	if pick < 0 {
		return
	}
	steps[pick].WouldRun = true
	for i := range steps {
		if steps[i].Due && i != pick {
			steps[i].WaitsFor = steps[pick].Operation
		}
	}
}

// FiredMaxInterval is the reason of an overdue operation.
const FiredMaxInterval = "max_interval"

// fired returns the reason with the first trigger that fired, or the maximum
// interval when it has passed (counted from the last success, or from since
// when the operation never ran); Fired stays empty when nothing calls for op.
func fired(op string, s Settings, m Metrics, last time.Time, ran bool, since, now time.Time, r Reason) Reason {
	t := s.Trigger
	check := func(name string, measured *int64, threshold *int64) bool {
		if measured == nil || threshold == nil || *measured < *threshold {
			return false
		}
		r.Fired, r.Measured, r.Threshold = name, f64(float64(*measured)), f64(float64(*threshold))
		return true
	}
	switch op {
	case OpOptimize:
		if t.SmallFileRatio != nil && m.SmallFiles != nil && m.DataFiles != nil && *m.DataFiles > 0 {
			ratio := float64(*m.SmallFiles) / float64(*m.DataFiles)
			if ratio >= *t.SmallFileRatio {
				r.Fired, r.Measured, r.Threshold = "small_file_ratio", f64(ratio), f64(*t.SmallFileRatio)
				return r
			}
		}
		if check("delete_files", m.DeleteFiles, t.DeleteFiles) {
			return r
		}
	case OpOptimizeManifests:
		if check("manifests", m.Manifests, t.Manifests) {
			return r
		}
	case OpExpireSnapshots:
		if check("snapshots", m.Snapshots, t.Snapshots) {
			return r
		}
		if m.OldestSnapshotAge != nil && t.OldestAgeSeconds != nil {
			age := int64(*m.OldestSnapshotAge / time.Second)
			if check("oldest_age_seconds", &age, t.OldestAgeSeconds) {
				return r
			}
		}
	}
	if maxInterval := s.MaxIntervalSeconds; maxInterval != nil {
		from := last
		if !ran {
			from = since
		}
		if from.IsZero() {
			r.Fired, r.MaxIntervalSeconds = FiredMaxInterval, maxInterval
			return r
		}
		if elapsed := now.Sub(from); elapsed >= seconds(*maxInterval) {
			e := int64(elapsed / time.Second)
			r.Fired, r.ElapsedSeconds, r.MaxIntervalSeconds = FiredMaxInterval, &e, maxInterval
		}
	}
	return r
}

// inWindow reports whether now falls in w, in w's time zone; nil is always.
func inWindow(w *Window, now time.Time) bool {
	if w == nil {
		return true
	}
	loc, err := time.LoadLocation(w.TZ)
	if err != nil {
		return false
	}
	h := now.In(loc).Hour()
	if w.StartHour <= w.EndHour {
		return h >= w.StartHour && h <= w.EndHour
	}
	return h >= w.StartHour || h <= w.EndHour
}

func seconds(n int64) time.Duration { return time.Duration(n) * time.Second }

func f64(v float64) *float64 { return &v }
