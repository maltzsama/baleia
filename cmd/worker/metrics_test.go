package main

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/metrics"
)

func TestMetricsListener(t *testing.T) {
	t.Parallel()
	reg := metrics.New("worker")
	handler, flush := reg.TemporalHandler()
	handler.Counter("temporal_activity_execution_failed").Inc(1) // the SDK's own name
	_ = flush()
	srv := httptest.NewServer(metricsMux(reg, ""))
	defer srv.Close()
	for path, want := range map[string]string{"/metrics": "temporal_activity_execution_failed_total 1", "/healthz": `"ok"`} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(b), want) {
			t.Errorf("%s: %d, missing %s", path, resp.StatusCode, want)
		}
	}
}
