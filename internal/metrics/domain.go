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
	// Password-reset operations (#51), bounded so the kind label stays low
	// cardinality.
	AuthPasswordResetRequest = "password_reset_request"
	AuthPasswordResetConfirm = "password_reset_confirm"

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

// Metadata records the metadata sync (#17). No metric carries a catalog
// label: the allow-list forbids concrete names.
type Metadata struct{ duration *prometheus.HistogramVec }

// Metadata registers baleia_metadata_sync_duration_seconds{outcome}.
func (r *Registry) Metadata() *Metadata {
	m := &Metadata{duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: Namespace, Name: "metadata_sync_duration_seconds", Help: "Duration of one catalog metadata pass by outcome.",
		Buckets: []float64{0.5, 1, 5, 15, 60, 300, 900, 3600},
	}, []string{"outcome"})}
	r.reg.MustRegister(m.duration)
	return m
}

// Synced records one catalog pass.
func (m *Metadata) Synced(d time.Duration, err error) {
	if m == nil {
		return
	}
	outcome := OutcomeSuccess
	if err != nil {
		outcome = OutcomeError
	}
	m.duration.WithLabelValues(outcome).Observe(d.Seconds())
}

// WatchMetadataStaleness exports baleia_metadata_staleness_seconds_max: the
// seconds since the stalest enabled catalog last finished a metadata pass,
// read from max at each scrape.
func (r *Registry) WatchMetadataStaleness(max func(ctx context.Context) (float64, error)) {
	r.reg.MustRegister(&stalenessCollector{max: max})
}

var metadataStaleness = prometheus.NewDesc(Namespace+"_metadata_staleness_seconds_max", "Seconds since the stalest enabled catalog last finished a metadata pass.", nil, nil)

type stalenessCollector struct {
	max func(ctx context.Context) (float64, error)
}

func (c *stalenessCollector) Describe(ch chan<- *prometheus.Desc) { ch <- metadataStaleness }

func (c *stalenessCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	v, err := c.max(ctx)
	if err != nil {
		slog.Warn("metadata_staleness_seconds_max", "err", err)
		return
	}
	ch <- prometheus.MustNewConstMetric(metadataStaleness, prometheus.GaugeValue, v)
}

// Audit counts audit events that could not be written (#45): the trail is
// best-effort, so gaps must be visible.
type Audit struct{ failures prometheus.Counter }

// Audit registers baleia_audit_write_failures_total.
func (r *Registry) Audit() *Audit {
	a := &Audit{failures: prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: Namespace, Name: "audit_write_failures_total", Help: "Audit events that could not be written.",
	})}
	r.reg.MustRegister(a.failures)
	return a
}

// WriteFailed counts one lost audit event.
func (a *Audit) WriteFailed() {
	if a == nil {
		return
	}
	a.failures.Inc()
}

// AuditExport observes the audit export to OpenSearch (#50).
type AuditExport struct {
	events *prometheus.CounterVec
	lag    prometheus.Gauge
}

// AuditExport registers baleia_audit_export_events_total{outcome} and
// baleia_audit_export_lag_seconds.
func (r *Registry) AuditExport() *AuditExport {
	a := &AuditExport{
		events: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Name: "audit_export_events_total", Help: "Audit events exported, per outcome.",
		}, []string{"outcome"}),
		lag: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace, Name: "audit_export_lag_seconds", Help: "Age of the newest audit event not yet exported.",
		}),
	}
	r.reg.MustRegister(a.events, a.lag)
	return a
}

// Observe counts one export batch's outcome.
func (a *AuditExport) Observe(outcome string, n int) {
	a.events.WithLabelValues(outcome).Add(float64(n))
}

// SetLag records the current export lag in seconds.
func (a *AuditExport) SetLag(seconds float64) { a.lag.Set(seconds) }
