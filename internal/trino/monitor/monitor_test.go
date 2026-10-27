package monitor

import "testing"

// TestObservable: user statements are observed; catalog DDL and the cluster
// poller's own statement are not (I4/CR-017, #406).
func TestObservable(t *testing.T) {
	t.Parallel()
	for sql, want := range map[string]bool{
		"SELECT count(*) FROM orders":                                          true,
		"CREATE CATALOG lake USING iceberg WITH (\"s3.aws-secret-key\" = 'x')": false,
		"DROP CATALOG lake":                                                    false,
		ClusterNodesSQL:                                                        false,
	} {
		if got := Observable(sql); got != want {
			t.Errorf("Observable(%q) = %v, want %v", sql, got, want)
		}
	}
}

func TestSnapshotState(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"FINISHED": "finished", "FAILED": "failed", "RUNNING": "running", "QUEUED": "running", "PLANNING": "running",
	} {
		if got := SnapshotState(in); got != want {
			t.Errorf("SnapshotState(%q) = %q, want %q", in, got, want)
		}
	}
}
