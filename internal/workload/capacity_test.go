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
