// Package metrics owns the Prometheus registry and the application's metric
// definitions. Every label here is intentionally low-cardinality and bounded:
// route template (not the concrete path), method, status class, engine,
// adapter, operation, outcome, and a fixed set of error codes. Labels must
// never carry SQL, user ids, execution ids, emails, table names or catalog
// names, so the time series stay bounded under real traffic.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is the single registry the API serves on /metrics.
var Registry = prometheus.NewRegistry()

var (
	HTTPRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "baleia_http_requests_total",
		Help: "HTTP requests handled by the API, by route template, method and status class.",
	}, []string{"route", "method", "status_class"})

	HTTPDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "baleia_http_request_duration_seconds",
		Help:    "HTTP request duration in seconds, by route template and method.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"route", "method"})

	AuthAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "baleia_auth_attempts_total",
		Help: "Authentication attempts, by outcome (success, missing_token, invalid_token, rate_limited).",
	}, []string{"outcome"})

	EngineQueries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "baleia_engine_queries_total",
		Help: "Statements issued to a query engine, by engine, adapter and outcome (success, error).",
	}, []string{"engine", "adapter", "outcome"})

	EngineErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "baleia_engine_errors_total",
		Help: "Query engine failures, by engine and a bounded error code.",
	}, []string{"engine", "code"})

	StorageOperations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "baleia_storage_operations_total",
		Help: "Object-storage operations, by operation and outcome (success, error).",
	}, []string{"operation", "outcome"})

	StorageBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "baleia_storage_bytes_total",
		Help: "Bytes transferred through object storage, by operation.",
	}, []string{"operation"})

	CacheLookups = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "baleia_cache_lookups_total",
		Help: "In-process cache lookups, by cache and outcome (hit, miss).",
	}, []string{"cache", "outcome"})
)

func init() {
	Registry.MustRegister(
		HTTPRequests,
		HTTPDuration,
		AuthAttempts,
		EngineQueries,
		EngineErrors,
		StorageOperations,
		StorageBytes,
		CacheLookups,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
}

// StatusClass buckets an HTTP status code into a bounded label value.
func StatusClass(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500:
		return "5xx"
	default:
		return "other"
	}
}

// Handler serves the registry in the Prometheus text exposition format.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{})
}
