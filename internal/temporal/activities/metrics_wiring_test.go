package activities

import (
	"testing"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/storage"
)

// TestSetMetrics_LeavesStorageToTheWiring: the composition root registers the
// storage metrics; SetMetrics on the same registry must not register them
// again (a duplicate registration panics at worker boot, #362).
func TestSetMetrics_LeavesStorageToTheWiring(t *testing.T) {
	s3, err := storage.New(storage.Options{Endpoint: "localhost:9000", Bucket: "results"})
	if err != nil {
		t.Fatal(err)
	}
	reg := metrics.New("worker")
	s3.SetMetrics(reg.Storage())
	(&Activities{dialects: dialect.NewRegistry(), s3: s3}).SetMetrics(reg)
}
