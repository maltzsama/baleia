package maintenance

import (
	"errors"
	"testing"
	"time"
)

var now = time.Date(2026, 6, 4, 10, 30, 0, 0, time.UTC)

// due returns the operations a plan finds due, and the one it runs.
func due(p Plan) (dueOps []string, runs string) {
	for _, s := range p.Steps {
		if s.Due {
			dueOps = append(dueOps, s.Operation)
		}
	}
	next, _ := p.Next()
	return dueOps, next.Operation
}

func step(p Plan, op string) Step {
	for _, s := range p.Steps {
		if s.Operation == op {
			return s
		}
	}
	return Step{}
}

func TestPlan_Next(t *testing.T) {
	t.Parallel()
	if _, ok := (Plan{}).Next(); ok {
		t.Fatalf("an empty plan runs nothing")
	}
}

func TestEvaluate(t *testing.T) {
	t.Parallel()
	smallFiles := Metrics{DataFiles: ptr(int64(100)), SmallFiles: ptr(int64(42)), Snapshots: ptr(int64(140))}
	tests := []struct {
		name     string
		change   func(*Policy)
		metrics  Metrics
		state    State
		kind     Kind
		wantDue  []string
		wantRuns string
		check    func(t *testing.T, p Plan)
	}{
		{
			// Parameters set just now: compaction and expiry fire, orphan
			// cleanup's week has not passed; compaction wins by priority.
			name: "priority picks one operation", metrics: smallFiles, kind: Automatic,
			state:    State{Since: now},
			wantDue:  []string{OpOptimize, OpExpireSnapshots},
			wantRuns: OpOptimize,
			check: func(t *testing.T, p Plan) {
				s := step(p, OpOptimize)
				if s.Reason.Fired != "small_file_ratio" || *s.Reason.Measured != 0.42 || *s.Reason.Threshold != 0.3 {
					t.Errorf("optimize reason = %+v", s.Reason)
				}
				if w := step(p, OpExpireSnapshots).WaitsFor; w != OpOptimize {
					t.Errorf("expiry waits for %q, want optimize", w)
				}
				if r := step(p, OpRemoveOrphanFiles).Reason; r.Blocked != BlockedNotFired {
					t.Errorf("orphan reason = %+v", r)
				}
				if step(p, OpOptimizeManifests).Reason.Blocked != BlockedDisabled {
					t.Errorf("manifest rewrite is disabled")
				}
			},
		},
		{
			// Expiry is a day overdue while compaction is due every round:
			// the overdue one goes first, so it cannot starve.
			name: "an overdue operation runs before a fired one", metrics: smallFiles, kind: Automatic,
			state: State{Since: now.Add(-48 * time.Hour), LastSuccess: map[string]time.Time{
				OpOptimize: now.Add(-2 * time.Hour), OpExpireSnapshots: now.Add(-25 * time.Hour), OpRemoveOrphanFiles: now,
			}},
			change:   func(p *Policy) { p.ExpireSnapshots.Trigger = Trigger{Snapshots: ptr(int64(1000))} },
			wantDue:  []string{OpOptimize, OpExpireSnapshots},
			wantRuns: OpExpireSnapshots,
			check: func(t *testing.T, p Plan) {
				if w := step(p, OpOptimize).WaitsFor; w != OpExpireSnapshots {
					t.Errorf("compaction waits for %q, want expire_snapshots", w)
				}
			},
		},
		{
			name: "never run, its maximum interval counts from the parameters", kind: Automatic,
			state:    State{Since: now.Add(-8 * 24 * time.Hour)},
			metrics:  Metrics{Snapshots: ptr(int64(1))},
			wantDue:  []string{OpExpireSnapshots, OpRemoveOrphanFiles},
			wantRuns: OpExpireSnapshots,
			check: func(t *testing.T, p Plan) {
				if r := step(p, OpRemoveOrphanFiles).Reason; r.Fired != FiredMaxInterval || *r.ElapsedSeconds != 8*24*3600 {
					t.Errorf("orphan reason = %+v", r)
				}
			},
		},
		{
			name: "min interval holds a fired trigger back", metrics: smallFiles, kind: Automatic,
			state: State{LastSuccess: map[string]time.Time{
				OpOptimize: now.Add(-30 * time.Minute), OpRemoveOrphanFiles: now.Add(-time.Hour),
			}},
			wantDue: []string{OpExpireSnapshots}, wantRuns: OpExpireSnapshots,
			check: func(t *testing.T, p Plan) {
				r := step(p, OpOptimize).Reason
				if r.Blocked != BlockedMinInterval || *r.MinIntervalSeconds != 3600 || r.LastSuccessAt == nil {
					t.Errorf("optimize reason = %+v", r)
				}
			},
		},
		{
			name: "max interval runs without a trigger", kind: Automatic,
			metrics: Metrics{Snapshots: ptr(int64(3))},
			state: State{LastSuccess: map[string]time.Time{
				OpExpireSnapshots: now.Add(-25 * time.Hour), OpRemoveOrphanFiles: now.Add(-time.Hour),
			}},
			wantDue: []string{OpExpireSnapshots}, wantRuns: OpExpireSnapshots,
			check: func(t *testing.T, p Plan) {
				r := step(p, OpExpireSnapshots).Reason
				if r.Fired != "max_interval" || *r.ElapsedSeconds != 90000 || *r.MaxIntervalSeconds != 86400 {
					t.Errorf("expiry reason = %+v", r)
				}
			},
		},
		{
			name: "unreadable metrics never fire", kind: Automatic, metrics: Metrics{},
			state: State{LastSuccess: map[string]time.Time{
				OpExpireSnapshots: now.Add(-time.Hour), OpRemoveOrphanFiles: now.Add(-time.Hour),
			}},
			check: func(t *testing.T, p Plan) {
				if r := step(p, OpOptimize).Reason; r.Blocked != BlockedNotFired {
					t.Errorf("optimize reason = %+v", r)
				}
			},
		},
		{
			name: "delete files fire compaction", kind: Automatic,
			metrics: Metrics{DataFiles: ptr(int64(10)), SmallFiles: ptr(int64(0)), DeleteFiles: ptr(int64(250))},
			state:   State{LastSuccess: map[string]time.Time{OpRemoveOrphanFiles: now, OpExpireSnapshots: now}},
			wantDue: []string{OpOptimize}, wantRuns: OpOptimize,
			check: func(t *testing.T, p Plan) {
				if r := step(p, OpOptimize).Reason; r.Fired != "delete_files" || *r.Measured != 250 {
					t.Errorf("optimize reason = %+v", r)
				}
			},
		},
		{
			name: "oldest snapshot age fires expiry", kind: Automatic,
			change: func(p *Policy) {
				p.ExpireSnapshots.Trigger = Trigger{OldestAgeSeconds: ptr(int64(259200))}
				p.ExpireSnapshots.MaxIntervalSeconds = nil
			},
			metrics: Metrics{OldestSnapshotAge: ptr(4 * 24 * time.Hour)},
			state:   State{LastSuccess: map[string]time.Time{OpRemoveOrphanFiles: now}},
			wantDue: []string{OpExpireSnapshots}, wantRuns: OpExpireSnapshots,
		},
		{
			name: "outside the window", metrics: smallFiles, kind: Automatic,
			change: func(p *Policy) {
				w := &Window{StartHour: 22, EndHour: 6, TZ: "UTC"}
				p.Optimize.Window, p.ExpireSnapshots.Window, p.RemoveOrphanFiles.Window = w, w, w
			},
			check: func(t *testing.T, p Plan) {
				if r := step(p, OpOptimize).Reason; r.Blocked != BlockedWindow {
					t.Errorf("optimize reason = %+v", r)
				}
			},
		},
		{
			// 10:30 UTC is 07:30 in São Paulo: inside 07–08 there.
			name: "window in its own time zone", metrics: smallFiles, kind: Automatic,
			change:   func(p *Policy) { p.Optimize.Window = &Window{StartHour: 7, EndHour: 8, TZ: "America/Sao_Paulo"} },
			state:    State{Since: now},
			wantDue:  []string{OpOptimize, OpExpireSnapshots},
			wantRuns: OpOptimize,
		},
		{
			name: "nothing runs while a run is in progress", metrics: smallFiles, kind: Automatic,
			state: State{Running: true},
			check: func(t *testing.T, p Plan) {
				if r := step(p, OpOptimize).Reason; r.Blocked != BlockedRunning {
					t.Errorf("optimize reason = %+v", r)
				}
			},
		},
		{
			name: "an automatic round skips mode off", metrics: smallFiles, kind: Automatic,
			change: func(p *Policy) { p.Mode = ModeOff },
			check: func(t *testing.T, p Plan) {
				if r := step(p, OpOptimize).Reason; r.Blocked != BlockedMode {
					t.Errorf("optimize reason = %+v", r)
				}
			},
		},
		{
			name: "a dry run plans mode off", metrics: smallFiles, kind: DryRun,
			change:   func(p *Policy) { p.Mode = ModeOff },
			state:    State{Since: now},
			wantDue:  []string{OpOptimize, OpExpireSnapshots},
			wantRuns: OpOptimize,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := validPolicy()
			if tt.change != nil {
				tt.change(&p)
			}
			plan, err := Evaluate(p, tt.metrics, tt.state, tt.kind, now)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if len(plan.Steps) != len(Priority) {
				t.Fatalf("plan has %d steps, want one per operation", len(plan.Steps))
			}
			gotDue, gotRuns := due(plan)
			if !equal(gotDue, tt.wantDue) || gotRuns != tt.wantRuns {
				t.Errorf("due = %q runs %q, want %q runs %q", gotDue, gotRuns, tt.wantDue, tt.wantRuns)
			}
			if tt.check != nil {
				tt.check(t, plan)
			}
		})
	}
}

func TestEvaluate_MaintainedElsewhere(t *testing.T) {
	t.Parallel()
	p := validPolicy()
	p.Mode = ModeMaintainedElsewhere
	for _, kind := range []Kind{Automatic, Manual, DryRun} {
		if _, err := Evaluate(p, Metrics{}, State{}, kind, now); !errors.Is(err, ErrMaintainedElsewhere) {
			t.Errorf("Evaluate(%s) error = %v, want ErrMaintainedElsewhere", kind, err)
		}
	}
}

// The issue's two examples, over a week of hourly rounds on parameters set a
// week earlier, with every operation succeeding when it runs.
func TestEvaluate_Rounds(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	week := func(p Policy, metrics func(hour int) Metrics) map[string]int {
		st := State{LastSuccess: map[string]time.Time{}, Since: start.Add(-7 * 24 * time.Hour)}
		runs := map[string]int{}
		for h := range 7 * 24 {
			at := start.Add(time.Duration(h) * time.Hour)
			plan, err := Evaluate(p, metrics(h), st, Automatic, at)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if next, ok := plan.Next(); ok {
				runs[next.Operation]++
				st.LastSuccess[next.Operation] = at
			}
		}
		return runs
	}

	t.Run("cdc table", func(t *testing.T) {
		t.Parallel()
		// Small files and delete files pile up every hour, so compaction is
		// due every round; expiry still runs daily and orphan cleanup weekly.
		runs := week(validPolicy(), func(int) Metrics {
			return Metrics{DataFiles: ptr(int64(500)), SmallFiles: ptr(int64(300)), DeleteFiles: ptr(int64(400)), Snapshots: ptr(int64(40))}
		})
		want := map[string]int{OpOptimize: 7*24 - 8, OpExpireSnapshots: 7, OpRemoveOrphanFiles: 1}
		for op, n := range want {
			if runs[op] != n {
				t.Fatalf("runs = %v, want %v", runs, want)
			}
		}
	})

	t.Run("batch table", func(t *testing.T) {
		t.Parallel()
		// One load a day at 02:00 leaves small files that compaction clears.
		p := validPolicy()
		p.Optimize.Params.SkipFilesNewerThanSeconds = nil
		runs := week(p, func(h int) Metrics {
			small := int64(0)
			if h%24 == 2 {
				small = 80
			}
			return Metrics{DataFiles: ptr(int64(100)), SmallFiles: &small, Snapshots: ptr(int64(10))}
		})
		if runs[OpOptimize] != 7 || runs[OpExpireSnapshots] != 7 || runs[OpRemoveOrphanFiles] != 1 {
			t.Fatalf("runs = %v, want compaction once per load, daily expiry, weekly orphan cleanup", runs)
		}
	})
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
