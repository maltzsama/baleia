package metrics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// registry has every metric this package produces registered, with the pool
// collector on a pool that never connects.
func registry(t *testing.T) *Registry {
	t.Helper()
	r := New("api")
	tracer := r.NewPoolTracer()
	cfg, err := pgxpool.ParseConfig("postgres://u@127.0.0.1:1/db")
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg) // lazy: no connection is opened
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	r.WatchPool(pool)
	ctx := tracer.TraceAcquireStart(context.Background(), pool, pgxpool.TraceAcquireStartData{})
	tracer.TraceAcquireEnd(ctx, pool, pgxpool.TraceAcquireEndData{})
	handler, flush := r.TemporalHandler()
	handler.Counter("temporal_workflow_completed").Inc(1) // the SDK's own name
	if err := flush(); err != nil {
		t.Fatal(err)
	}
	return r
}

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

func TestScrape(t *testing.T) {
	code, body := scrape(t, registry(t).Handler(""), "")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{
		`baleia_build_info{commit=`, `baleia_db_pool_max_conns{process="api"}`, `baleia_db_pool_acquired_conns{process="api"} 0`,
		`baleia_db_pool_acquire_duration_seconds_count{process="api"} 1`, "temporal_workflow_completed_total 1", "go_goroutines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape is missing %s", want)
		}
	}
}

func TestTokenAuth(t *testing.T) {
	h := registry(t).Handler("s3cret")
	for auth, want := range map[string]int{"": 401, "Bearer nope": 401, "s3cret": 401, "Bearer s3cret": 200} {
		if code, _ := scrape(t, h, auth); code != want {
			t.Errorf("Authorization %q: %d, want %d", auth, code, want)
		}
	}
}
