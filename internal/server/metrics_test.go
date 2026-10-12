package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

const httpRequestsMetric = "baleia_http_requests_total"

func init() {
	// Ensure the exposition always carries at least one series so the handler
	// assertions are not dependent on test order.
	metrics.HTTPRequests.WithLabelValues("/seed", "GET", "2xx").Inc()
}

func TestMetricsHandlerRequiresToken(t *testing.T) {
	s := &Server{cfg: &config.Config{MetricsEnabled: true, MetricsToken: "s3cr3t-token"}}
	h := s.metricsHandler()

	t.Run("no token is rejected", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("GET", "/metrics", nil))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		require.NotContains(t, rec.Body.String(), httpRequestsMetric)
	})

	t.Run("wrong token is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/metrics", nil)
		req.Header.Set("X-Metrics-Token", "nope")
		rec := httptest.NewRecorder()
		h(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("bearer token is accepted", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/metrics", nil)
		req.Header.Set("Authorization", "Bearer s3cr3t-token")
		rec := httptest.NewRecorder()
		h(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), httpRequestsMetric)
		require.NotContains(t, rec.Body.String(), "s3cr3t-token")
	})

	t.Run("X-Metrics-Token is accepted", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/metrics", nil)
		req.Header.Set("X-Metrics-Token", "s3cr3t-token")
		rec := httptest.NewRecorder()
		h(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestMetricsHandlerWithoutTokenIsOpen(t *testing.T) {
	s := &Server{cfg: &config.Config{MetricsEnabled: true}}
	rec := httptest.NewRecorder()
	s.metricsHandler()(rec, httptest.NewRequest("GET", "/metrics", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), httpRequestsMetric)
}

// TestHTTPMetricsUsesRouteTemplate proves count is recorded against the
// low-cardinality chi pattern, never the concrete path (which carries ids).
func TestHTTPMetricsUsesRouteTemplate(t *testing.T) {
	const template = "/api/v1/workspaces/{id}/executions"
	const concrete = "/api/v1/workspaces/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/executions"

	before := counterValue(t, httpRequestsMetric, map[string]string{
		"route": template, "method": "GET", "status_class": "2xx",
	})

	router := chi.NewRouter()
	router.Use(HTTPMetrics)
	router.Get(template, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", concrete, nil))

	require.Equal(t, before+1, counterValue(t, httpRequestsMetric, map[string]string{
		"route": template, "method": "GET", "status_class": "2xx",
	}), "the counter must be keyed by the route template, not the concrete path")

	// The raw path must not have become a time series of its own.
	require.Equal(t, 0.0, counterValue(t, httpRequestsMetric, map[string]string{
		"route": concrete, "method": "GET", "status_class": "2xx",
	}))
}

// TestHTTPMetricsRecordsStatusClass proves a 4xx is counted as 4xx, and that a
// response with no explicit status still counts as 2xx.
func TestHTTPMetricsRecordsStatusClass(t *testing.T) {
	const template = "/metrics-test/{id}"

	router := chi.NewRouter()
	router.Use(HTTPMetrics)
	router.Get(template, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	before404 := counterValue(t, httpRequestsMetric, map[string]string{
		"route": template, "method": "GET", "status_class": "4xx",
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/metrics-test/x", nil))
	require.Equal(t, before404+1, counterValue(t, httpRequestsMetric, map[string]string{
		"route": template, "method": "GET", "status_class": "4xx",
	}))
}

// counterValue reads a single series back out of the shared registry. It
// returns 0 when the series has not been touched, so tests can diff before and
// after an action instead of asserting on process-wide totals. Labels are
// matched by name because Gather() emits them alphabetically, not in
// declaration order.
func counterValue(t *testing.T, metricName string, want map[string]string) float64 {
	t.Helper()
	families, err := metrics.Registry.Gather()
	require.NoError(t, err)

	for _, mf := range families {
		if mf.GetName() != metricName {
			continue
		}
		for _, m := range mf.GetMetric() {
			got := map[string]string{}
			for _, lp := range m.GetLabel() {
				got[lp.GetName()] = lp.GetValue()
			}
			if len(got) != len(want) {
				continue
			}
			match := true
			for k, v := range want {
				if got[k] != v {
					match = false
					break
				}
			}
			if match && m.GetCounter() != nil {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}
