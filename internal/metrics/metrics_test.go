package metrics

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// registry has every metric this package produces registered, with the pool
// collector on a pool that never connects.
func registry(t *testing.T) *Registry {
	t.Helper()
	r := New("api")
	handler, flush := r.TemporalHandler()
	handler.Counter("temporal_workflow_completed").Inc(1) // the SDK's own name
	if err := flush(); err != nil {
		t.Fatal(err)
	}
	r.Metadata().Synced(time.Second, nil)
	r.Audit().WriteFailed()
	r.WatchMetadataStaleness(slog.New(slog.DiscardHandler), func(context.Context) (float64, error) { return 42, nil })
	workload := r.Workload()
	workload.State("e1", "interactive", 1, 2)
	workload.Admitted("e1", "interactive", time.Second)
	workload.Timeout("e1", "interactive")
	memory := 0.5
	r.Monitoring().Cluster("trino", 3, 2, 1, 0, &memory)
	search := r.Search()
	search.Result(SearchMetadata, 3)
	search.Result(SearchContent, 0)
	return r
}

// scrape renders the handler for one request and returns the status and body.
func scrape(t *testing.T, h http.Handler, auth string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Body)
	return rec.Code, string(b)
}

func TestLabelAllowList(t *testing.T) {
	t.Parallel()
	families, err := registry(t).Prometheus().Gather()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range families {
		if !IsBaleia(f.GetName()) {
			continue
		}
		n++
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if !slices.Contains(AllowedLabels, l.GetName()) {
					t.Errorf("%s has label %q outside the allow-list", f.GetName(), l.GetName())
				}
			}
		}
	}
	if n < 5 { // build_info, the three pool gauges, the acquire histogram
		t.Errorf("only %d baleia_ families registered", n)
	}
}

// TestMetadataMetrics: the sync's freshness gauge and duration histogram are
// exported under the names the alert rules use, without catalog labels.
func TestMetadataMetrics(t *testing.T) {
	t.Parallel()
	_, body := scrape(t, registry(t).Handler(""), "")
	for _, want := range []string{
		"baleia_metadata_staleness_seconds_max 42",
		`baleia_metadata_sync_duration_seconds_count{outcome="success"} 1`,
		"baleia_audit_write_failures_total 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape lacks %q", want)
		}
	}
}

// TestSearchMetrics: searches are counted by surface and by whether they
// found anything, never by query text.
func TestSearchMetrics(t *testing.T) {
	t.Parallel()
	_, body := scrape(t, registry(t).Handler(""), "")
	for _, want := range []string{
		`baleia_search_requests_total{kind="metadata",outcome="hit"} 1`,
		`baleia_search_requests_total{kind="content",outcome="empty"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape lacks %q", want)
		}
	}
	var nilSearch *Search
	nilSearch.Result(SearchDocs, 0) // a nil producer is a no-op
}

// TestWorkloadMetrics: the queue gauges, counter and histogram are exported
// under the names the alert rules use, with only engine and class labels.
func TestWorkloadMetrics(t *testing.T) {
	t.Parallel()
	_, body := scrape(t, registry(t).Handler(""), "")
	for _, want := range []string{
		`baleia_execution_queue_depth{class="interactive",engine="e1"} 2`,
		`baleia_execution_queue_running{class="interactive",engine="e1"} 1`,
		`baleia_execution_admissions_total{class="interactive",engine="e1"} 1`,
		`baleia_execution_queue_timeouts_total{class="interactive",engine="e1"} 1`,
		`baleia_execution_queue_wait_seconds_count{class="interactive",engine="e1"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape lacks %q", want)
		}
	}
}

// TestMonitoringMetrics: the cluster gauges are exported under the names the
// dashboard and the alert rules use, labelled only by engine kind.
func TestMonitoringMetrics(t *testing.T) {
	t.Parallel()
	_, body := scrape(t, registry(t).Handler(""), "")
	for _, want := range []string{
		`baleia_cluster_active_workers{engine="trino"} 3`,
		`baleia_cluster_running_queries{engine="trino"} 2`,
		`baleia_cluster_queued_queries{engine="trino"} 1`,
		`baleia_cluster_blocked_queries{engine="trino"} 0`,
		`baleia_cluster_memory_used_ratio{engine="trino"} 0.5`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape lacks %q", want)
		}
	}
}

func TestScrape(t *testing.T) {
	t.Parallel()
	code, body := scrape(t, registry(t).Handler(""), "")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{
		`baleia_build_info{commit=`, "temporal_workflow_completed_total 1", "go_goroutines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape is missing %s", want)
		}
	}
}

func TestTokenAuth(t *testing.T) {
	t.Parallel()
	h := registry(t).Handler("s3cret")
	for auth, want := range map[string]int{"": 401, "Bearer nope": 401, "s3cret": 401, "Bearer s3cret": 200} {
		if code, _ := scrape(t, h, auth); code != want {
			t.Errorf("Authorization %q: %d, want %d", auth, code, want)
		}
	}
}
