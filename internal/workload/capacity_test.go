package workload

import (
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/queryengine"
)

func TestDefaultsAreConservativeAndOrdered(t *testing.T) {
	t.Parallel()
	for _, engineClass := range []string{"federated", "direct"} {
		defaults := Defaults(engineClass)
		interactive := defaults[queryengine.ClassInteractive]
		heavy := defaults[queryengine.ClassHeavy]
		background := defaults[queryengine.ClassBackground]

		if !(interactive.MaxRunning > heavy.MaxRunning && heavy.MaxRunning >= background.MaxRunning) {
			t.Errorf("%s: max_running interactive=%d heavy=%d background=%d",
				engineClass, interactive.MaxRunning, heavy.MaxRunning, background.MaxRunning)
		}
		if !(interactive.QueueTimeout <= heavy.QueueTimeout && heavy.QueueTimeout <= background.QueueTimeout) {
			t.Errorf("%s: queue timeouts must not shrink for heavier work", engineClass)
		}
		if !(interactive.RuntimeTimeout <= heavy.RuntimeTimeout && heavy.RuntimeTimeout <= background.RuntimeTimeout) {
			t.Errorf("%s: runtime timeouts must not shrink for heavier work", engineClass)
		}
		// The point of #576: a run that may wait 24h must be allowed to run
		// longer than the global 30m statement timeout.
		if background.RuntimeTimeout <= 30*time.Minute {
			t.Errorf("%s: background runtime %s must exceed the 30m global cap", engineClass, background.RuntimeTimeout)
		}
	}
}

func TestResolveFallsBackOnlyWhenMissing(t *testing.T) {
	t.Parallel()
	direct := Defaults("direct")

	got := Resolve("direct", queryengine.ClassInteractive, nil)
	if got != direct[queryengine.ClassInteractive] {
		t.Errorf("missing row = %+v, want the default %+v", got, direct[queryengine.ClassInteractive])
	}

	configured := Capacity{MaxRunning: 1, MaxQueued: 2, QueueTimeout: time.Minute}
	if got := Resolve("direct", queryengine.ClassInteractive, &configured); got != configured {
		t.Errorf("configured row = %+v, want %+v", got, configured)
	}
}
