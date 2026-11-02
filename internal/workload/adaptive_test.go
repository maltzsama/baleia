package workload

import "testing"

func i64(v int64) *int64 { return &v }

func TestAdaptiveThresholds(t *testing.T) {
	t.Parallel()
	def := DefaultAdaptiveThresholds()
	cases := []struct {
		name     string
		pressure ClusterPressure
		want     bool
		reason   string
	}{
		{"calm", ClusterPressure{}, false, ""},
		{"queued at the threshold is not pressure", ClusterPressure{QueuedQueries: def.QueuedQueries}, false, ""},
		{"queued above", ClusterPressure{QueuedQueries: def.QueuedQueries + 1}, true, "cluster:queued"},
		{"blocked above", ClusterPressure{BlockedQueries: def.BlockedQueries + 1}, true, "cluster:blocked"},
		{"low memory headroom", ClusterPressure{TotalMemoryBytes: i64(100), FreeMemoryBytes: i64(5)}, true, "cluster:memory"},
		{"unknown memory does not throttle", ClusterPressure{FreeMemoryBytes: i64(5)}, false, ""},
		{"queued wins over memory", ClusterPressure{QueuedQueries: def.QueuedQueries + 1, TotalMemoryBytes: i64(100), FreeMemoryBytes: i64(1)}, true, "cluster:queued"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, reason := def.Throttled(tc.pressure)
			if got != tc.want || reason != tc.reason {
				t.Fatalf("Throttled(%+v) = %v/%q, want %v/%q", tc.pressure, got, reason, tc.want, tc.reason)
			}
		})
	}
}

func TestAdaptiveThresholdsZeroDisablesTrigger(t *testing.T) {
	t.Parallel()
	zero := AdaptiveThresholds{}
	if got, _ := zero.Throttled(ClusterPressure{QueuedQueries: 1 << 20, BlockedQueries: 1 << 20, TotalMemoryBytes: i64(1), FreeMemoryBytes: i64(0)}); got {
		t.Fatal("a zero threshold must disable the gate")
	}
}
