package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestRouteTemplate: requests are labelled by the chi pattern they matched,
// never by the raw path; a path that matched nothing is "unmatched".
func TestRouteTemplate(t *testing.T) {
	t.Parallel()
	reg := New("api")
	m := reg.HTTP()
	r := chi.NewRouter()
	r.Use(m.Middleware)
	r.Route("/api/v1", func(r chi.Router) {
		r.NotFound(http.NotFound)
		r.Get("/catalogs/{catalogId}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		r.Post("/workspaces/{id}/executions", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	})
	for _, path := range []string{"/api/v1/catalogs/3f2a9c1e-0000-4000-8000-000000000001", "/api/v1/catalogs/other", "/nope/42", "/api/v1/nope/7"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("BREW", "/api/v1/catalogs/x", nil))
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/3f2a9c1e-0000-4000-8000-000000000002/executions", nil))

	if got := testutil.ToFloat64(m.requests.WithLabelValues("/api/v1/workspaces/{id}/executions", "POST", "2xx")); got != 1 {
		t.Errorf("executions recorded under its template %v times, want 1", got)
	}

	if got := testutil.ToFloat64(m.requests.WithLabelValues("/api/v1/catalogs/{catalogId}", "GET", "2xx")); got != 2 {
		t.Errorf("templated route count = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues("unmatched", "GET", "4xx")); got != 2 {
		t.Errorf("unmatched count = %v, want 2 (no route, and a group's catch-all 404)", got)
	}
	out := scrapeAll(t, reg)
	for _, bad := range []string{"3f2a9c1e", "/nope/42", "catalogs/other", "BREW"} {
		if strings.Contains(out, bad) {
			t.Errorf("scrape carries raw request data %q", bad)
		}
	}
}

// TestNoForbiddenValues drives every producer with a fake request set (UUID
// paths, e-mails in queries) and errors carrying ids, names and SQL, then
// scans the scrape: no label value is a UUID or an e-mail, holds whitespace
// or SQL, or names a catalog.
func TestNoForbiddenValues(t *testing.T) {
	t.Parallel()
	reg := New("api")
	const id, email, name, sql = "9b1d5c3e-1111-4222-8333-444455556666", "ana@example.com", "secret_catalog_name", "SELECT * FROM users"

	r := chi.NewRouter()
	r.Use(reg.HTTP().Middleware)
	r.Route("/api/v1", func(r chi.Router) {
		r.NotFound(http.NotFound)
		r.Get("/workspaces/{id}/executions", func(w http.ResponseWriter, _ *http.Request) {})
		r.Get("/users", func(w http.ResponseWriter, _ *http.Request) {})
	})
	for _, target := range []string{
		"/api/v1/workspaces/" + id + "/executions",
		"/api/v1/users?email=" + email,
		"/api/v1/catalogs/" + name + "/tables",
		"/api/v1/query?sql=" + strings.ReplaceAll(sql, " ", "%20"),
	} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
	}

	reg.Auth().Attempt(AuthPassword, OutcomeInvalidCredentials)
	reg.Authz().Denied("catalog", "not_found")
	reg.Storage().Op("put", errors.New("put "+id+": "+sql), 10)
	eng := reg.Engine()
	eng.Statement("trino", StatementError, "syntax", time.Second, 0, 0)
	eng.Statement("", StatementSuccess, "", time.Second, 5, 100)
	reg.Catalog().DDL("create", errors.New("catalog "+name+" exists"))
	reg.Schedules().Succeeded("lens-query-poller")
	reg.WatchBindingStates(slog.New(slog.DiscardHandler), func(context.Context) (map[string]int64, error) { return map[string]int64{"active": 1}, nil })

	uuid := regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	mail := regexp.MustCompile(`[^@\s"]+@[^@\s"]+\.[a-z]+`)
	fams, err := reg.Prometheus().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fams {
		if !IsBaleia(f.GetName()) {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				v, low := l.GetValue(), strings.ToLower(l.GetValue())
				switch {
				case uuid.MatchString(v), mail.MatchString(v):
					t.Errorf("%s{%s=%q}: identifier in a label", f.GetName(), l.GetName(), v)
				case strings.ContainsAny(v, " \t\n"):
					t.Errorf("%s{%s=%q}: whitespace in a label", f.GetName(), l.GetName(), v)
				case strings.Contains(low, "select") || strings.Contains(low, "from") || strings.Contains(v, name):
					t.Errorf("%s{%s=%q}: SQL or a catalog name in a label", f.GetName(), l.GetName(), v)
				}
			}
		}
	}
	out := scrapeAll(t, reg)
	if !strings.Contains(out, `baleia_engine_statements_total{engine="unknown",outcome="success"} 1`) {
		t.Errorf("empty engine kind should be labelled unknown")
	}
	if !strings.Contains(out, `route="/api/v1/workspaces/{id}/executions"`) {
		t.Errorf("templated route missing from the scrape")
	}
}

func scrapeAll(t *testing.T, reg *Registry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	reg.Handler("").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}
