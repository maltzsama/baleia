package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Domain producers. Each is registered by its constructor on the Registry
// and is nil-safe: code built without metrics (tests, tools) holds nil.

// Auth counts authentication attempts.
type Auth struct{ attempts *prometheus.CounterVec }

// Auth registers baleia_auth_attempts_total{kind,outcome}.
func (r *Registry) Auth() *Auth {
	a := &Auth{attempts: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "auth_attempts_total", Help: "Authentication attempts by kind and outcome.",
	}, []string{"kind", "outcome"})}
	r.reg.MustRegister(a.attempts)
	return a
}

// Auth kinds and outcomes (bounded).
const (
	AuthPassword = "password"
	AuthOIDC     = "oidc"
	AuthRefresh  = "refresh"

	OutcomeSuccess            = "success"
	OutcomeInvalidCredentials = "invalid_credentials"
	OutcomeRateLimited        = "rate_limited"
	OutcomeError              = "error"
)

// Attempt records one authentication attempt.
func (a *Auth) Attempt(kind, outcome string) {
	if a == nil {
		return
	}
	a.attempts.WithLabelValues(kind, outcome).Inc()
}

// Authz counts access the checker refused.
type Authz struct{ denials *prometheus.CounterVec }

// Authz registers baleia_authz_denials_total{resource,reason}.
func (r *Registry) Authz() *Authz {
	a := &Authz{denials: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "authz_denials_total", Help: "Authorization denials by resource and reason.",
	}, []string{"resource", "reason"})}
	r.reg.MustRegister(a.denials)
	return a
}

// Denied records a refusal; reason is not_found or forbidden.
func (a *Authz) Denied(resource, reason string) {
	if a == nil {
		return
	}
	a.denials.WithLabelValues(resource, reason).Inc()
}

// Storage counts object-storage operations.
type Storage struct {
	ops   *prometheus.CounterVec
	bytes *prometheus.CounterVec
}

// Storage registers baleia_storage_operations_total{operation,outcome} and
// baleia_storage_bytes_total{operation}.
func (r *Registry) Storage() *Storage {
	s := &Storage{
		ops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "storage_operations_total", Help: "Object storage operations by operation and outcome.",
		}, []string{"operation", "outcome"}),
		bytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "storage_bytes_total", Help: "Bytes moved to or from object storage.",
		}, []string{"operation"}),
	}
	r.reg.MustRegister(s.ops, s.bytes)
	return s
}

// Op records one put, get or delete.
func (s *Storage) Op(operation string, err error, bytes int64) {
	if s == nil {
		return
	}
	outcome := OutcomeSuccess
	if err != nil {
		outcome = OutcomeError
	}
	s.ops.WithLabelValues(operation, outcome).Inc()
	if bytes > 0 {
		s.bytes.WithLabelValues(operation).Add(float64(bytes))
	}
}

// Engine records statements run on query engines.
type Engine struct {
	statements *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	errors     *prometheus.CounterVec
	rows       *prometheus.CounterVec
	bytes      *prometheus.CounterVec
}

// Engine registers the baleia_engine_* metrics, labelled by engine kind.
func (r *Registry) Engine() *Engine {
	e := &Engine{
		statements: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "engine_statements_total", Help: "Statements by engine kind and outcome.",
		}, []string{"engine", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace, Name: "engine_statement_duration_seconds", Help: "Statement duration by engine kind.",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300, 900, 3600},
		}, []string{"engine"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "engine_errors_total", Help: "Statement errors by engine kind and error code.",
		}, []string{"engine", "code"}),
		rows: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "engine_rows_returned_total", Help: "Rows returned by engine kind.",
		}, []string{"engine"}),
		bytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "engine_result_bytes_total", Help: "Result bytes stored by engine kind.",
		}, []string{"engine"}),
	}
	r.reg.MustRegister(e.statements, e.duration, e.errors, e.rows, e.bytes)
	return e
}

// Statement outcomes.
const (
	StatementSuccess   = "success"
	StatementError     = "error"
	StatementCancelled = "cancelled"
	StatementTruncated = "truncated"
)

// Statement records one statement; code is the engine error's code (the
// bounded queryengine codes) for errors.
func (e *Engine) Statement(engine, outcome, code string, d time.Duration, rows, bytes int64) {
	if e == nil {
		return
	}
	if engine == "" {
		engine = "unknown"
	}
	e.statements.WithLabelValues(engine, outcome).Inc()
	e.duration.WithLabelValues(engine).Observe(d.Seconds())
	if outcome == StatementError {
		if code == "" {
			code = "unknown"
		}
		e.errors.WithLabelValues(engine, code).Inc()
	}
	if rows > 0 {
		e.rows.WithLabelValues(engine).Add(float64(rows))
	}
	if bytes > 0 {
		e.bytes.WithLabelValues(engine).Add(float64(bytes))
	}
}

// Catalog records catalog DDL and exports bindings by lifecycle state.
type Catalog struct{ ddl *prometheus.CounterVec }

// Catalog registers baleia_catalog_ddl_total{action,outcome}.
func (r *Registry) Catalog() *Catalog {
	c := &Catalog{ddl: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "catalog_ddl_total", Help: "Catalog DDL sent to engines by action and outcome.",
	}, []string{"action", "outcome"})}
	r.reg.MustRegister(c.ddl)
	return c
}

// DDL records a create, replace or drop.
func (c *Catalog) DDL(action string, err error) {
	if c == nil {
		return
	}
	outcome := OutcomeSuccess
	if err != nil {
		outcome = OutcomeError
	}
	c.ddl.WithLabelValues(action, outcome).Inc()
}

// WatchBindingStates exports baleia_catalogs_by_state{state} from count,
// which returns managed bindings per lifecycle state (#38).
func (r *Registry) WatchBindingStates(count func(ctx context.Context) (map[string]int64, error)) {
	r.reg.MustRegister(&statesCollector{count: count})
}

var catalogsByState = prometheus.NewDesc(Namespace+"_catalogs_by_state", "Managed catalog bindings by lifecycle state.", []string{"state"}, nil)

type statesCollector struct {
	count func(ctx context.Context) (map[string]int64, error)
}

func (c *statesCollector) Describe(ch chan<- *prometheus.Desc) { ch <- catalogsByState }

func (c *statesCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	counts, err := c.count(ctx)
	if err != nil {
		slog.Warn("catalogs_by_state", "err", err)
		return
	}
	for _, state := range []string{"provisioning", "active", "updating", "error", "deprovisioning", "disabled"} {
		ch <- prometheus.MustNewConstMetric(catalogsByState, prometheus.GaugeValue, float64(counts[state]), state)
	}
}

// Schedules records the last successful pass of each recurring job (#85).
type Schedules struct{ last *prometheus.GaugeVec }

// Schedules registers baleia_schedule_last_success_timestamp_seconds{schedule};
// schedule is one of the IDs given to schedules.Ensure (bounded).
func (r *Registry) Schedules() *Schedules {
	s := &Schedules{last: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: Namespace, Name: "schedule_last_success_timestamp_seconds", Help: "Unix time of the last successful pass of a schedule.",
	}, []string{"schedule"})}
	r.reg.MustRegister(s.last)
	return s
}

// Succeeded records a successful pass now.
func (s *Schedules) Succeeded(schedule string) {
	if s == nil {
		return
	}
	s.last.WithLabelValues(schedule).SetToCurrentTime()
}
