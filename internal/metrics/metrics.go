// Package metrics is Baleia's metrics catalog (#87): one Prometheus registry
// per process, constructors for the catalog's metrics and the /metrics
// handler. Metric names start with baleia_ and carry only the labels of
// AllowedLabels with bounded values (no identifiers, names or SQL); a metric
// is registered only where something produces it.
package metrics

import (
	"crypto/subtle"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/version"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/uber-go/tally/v4"
	tallyprom "github.com/uber-go/tally/v4/prometheus"
	"go.temporal.io/sdk/client"
	sdktally "go.temporal.io/sdk/contrib/tally"
)

// Namespace prefixes every Baleia metric.
const Namespace = "baleia"

// AllowedLabels are the only label names a Baleia metric may carry
// (TestLabelAllowList).
var AllowedLabels = []string{
	"route", "method", "status_class", "kind", "outcome", "resource", "reason", "engine", "code", "class",
	"schedule", "action", "result", "state", "operation", "process", "version", "commit", "stream",
}

// Registry is one process's metrics.
type Registry struct {
	reg     *prometheus.Registry
	process string // "api" or "worker"
}

// New is a process's registry with the Go runtime and process collectors and
// baleia_build_info.
func New(process string) *Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	r := &Registry{reg: reg, process: process}
	info := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: Namespace, Name: "build_info", Help: "Always 1; the build's version and commit.",
	}, []string{"version", "commit"})
	info.WithLabelValues(version.Version, commit()).Set(1)
	reg.MustRegister(info)
	return r
}

// commit is the VCS revision the binary was built from, short.
func commit() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 12 {
				return s.Value[:12]
			}
		}
	}
	return "unknown"
}

// Prometheus is the underlying registry, for tests and producers.
func (r *Registry) Prometheus() *prometheus.Registry { return r.reg }

// Handler serves the registry. With a token, requests need
// "Authorization: Bearer <token>" (constant-time compare): Prometheus cannot
// log in, so /metrics is never behind the user JWT.
func (r *Registry) Handler(token string) http.Handler {
	h := promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{Registry: r.reg})
	if token == "" {
		return h
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if subtle.ConstantTimeCompare([]byte(req.Header.Get("Authorization")), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, req)
	})
}

// TemporalHandler is the Temporal SDK's metrics (temporal_*: workflow and
// activity counts, latencies, poller stats) reported into this registry
// through the SDK's tally/Prometheus integration. Close flushes it.
func (r *Registry) TemporalHandler() (client.MetricsHandler, func() error) {
	reporter := tallyprom.NewReporter(tallyprom.Options{Registerer: r.reg})
	scope, closer := tally.NewRootScope(tally.ScopeOptions{
		CachedReporter:  reporter,
		Separator:       tallyprom.DefaultSeparator,
		SanitizeOptions: &sdktally.PrometheusSanitizeOptions,
	}, time.Second)
	return sdktally.NewMetricsHandler(sdktally.NewPrometheusNamingScope(scope)), closer.Close
}

// IsBaleia reports whether a metric family belongs to the catalog (the Go
// runtime, process and temporal_* families are defined by their libraries).
func IsBaleia(name string) bool { return strings.HasPrefix(name, Namespace+"_") }
