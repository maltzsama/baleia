package queryengine

import "testing"

func TestWorkloadClassValid(t *testing.T) {
	t.Parallel()
	for _, c := range []WorkloadClass{ClassInteractive, ClassHeavy, ClassBackground} {
		if !c.Valid() {
			t.Errorf("%q is not valid", c)
		}
	}
	if WorkloadClass("").Valid() || WorkloadClass("nonsense").Valid() {
		t.Error("empty or unknown class reported valid")
	}
}

func TestWorkloadClassQueryPriority(t *testing.T) {
	t.Parallel()
	interactive, ok := ClassInteractive.QueryPriority()
	if !ok {
		t.Fatal("interactive has no query priority")
	}
	heavy, ok := ClassHeavy.QueryPriority()
	if !ok {
		t.Fatal("heavy has no query priority")
	}
	background, ok := ClassBackground.QueryPriority()
	if !ok {
		t.Fatal("background has no query priority")
	}
	// Trino treats larger numbers as higher priority; interactive must outrank
	// background so a queued preview is not starved by retriever refreshes.
	if !(interactive > heavy && heavy > background) {
		t.Errorf("priority order = interactive %d, heavy %d, background %d", interactive, heavy, background)
	}
	if _, ok := WorkloadClass("nonsense").QueryPriority(); ok {
		t.Error("unknown class returned a priority")
	}
}
