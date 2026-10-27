// Package obs holds the observability plumbing shared by the API and the
// worker. Today it is the OpenTelemetry tracing setup (#47): tracing is off by
// default, and when it is off nothing is installed, so the global provider
// stays the no-op one and there is no exporter or background goroutine.
//
// The configuration follows the OpenTelemetry standard environment variables
// (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_SERVICE_NAME, ...); BALEIA_TRACING_ENABLED
// is the single switch that turns it on.
package obs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/exaring/otelpgx"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	temporalotel "go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
)

// enabled is set by Setup: the helpers are no-ops until it is true.
var enabled bool

// Enabled reports whether tracing was installed by Setup.
func Enabled() bool { return enabled }

// Setup installs the global tracer provider when requested. service names the
// process ("api" or "worker"); it is used as service.name unless
// OTEL_SERVICE_NAME overrides it. The returned function flushes and shuts the
// provider down; it is a no-op when tracing is disabled. A resource that
// cannot be built from the environment is logged, and the default is used.
func Setup(ctx context.Context, logger *slog.Logger, on bool, service string) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	if !on {
		return noop, nil
	}
	exp, err := otlptracegrpc.New(ctx)
	if err != nil {
		return noop, err
	}
	attrs := []resource.Option{resource.WithFromEnv(), resource.WithTelemetrySDK()}
	if os.Getenv("OTEL_SERVICE_NAME") == "" {
		attrs = append(attrs, resource.WithAttributes(semconv.ServiceName(service)))
	}
	res, err := resource.New(ctx, attrs...)
	if err != nil {
		// resource.New returns a usable partial resource with the error when
		// a detector fails; keep it, and say why attributes may be missing.
		logger.WarnContext(ctx, "tracing resource incomplete; some attributes may be missing", "err", err)
		if res == nil {
			res = resource.Default()
		}
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	install(tp)
	return tp.Shutdown, nil
}

// install sets the global provider and propagator and marks tracing enabled.
func install(tp *sdktrace.TracerProvider) {
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	enabled = true
}

// HTTPHandler wraps a handler so each request is a server span. When tracing is
// off it returns the handler unchanged. The span is renamed to the matched chi
// route by RouteName once routing has happened.
func HTTPHandler(next http.Handler, operation string) http.Handler {
	if !enabled {
		return next
	}
	return otelhttp.NewHandler(next, operation)
}

// RouteName renames the current server span to the matched chi route pattern
// once the router has routed. Mount it with mux.Use so it runs inside the span
// HTTPHandler created; a no-op when tracing is off or nothing matched.
func RouteName(next http.Handler) http.Handler {
	if !enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := routeTemplate(r); p != "" {
			trace.SpanFromContext(r.Context()).SetName(r.Method + " " + p)
		}
		next.ServeHTTP(w, r)
	})
}

// routeTemplate returns the matched chi route pattern, or "".
func routeTemplate(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		return rc.RoutePattern()
	}
	return ""
}

// WrapHTTPClient points a client's every request through an instrumented
// transport. When tracing is off it returns the client unchanged.
func WrapHTTPClient(hc *http.Client) *http.Client {
	if !enabled || hc == nil {
		return hc
	}
	hc.Transport = otelhttp.NewTransport(hc.Transport)
	return hc
}

// Transport wraps base with an instrumented transport, or returns base
// unchanged when tracing is off.
func Transport(base http.RoundTripper) http.RoundTripper {
	if !enabled {
		return base
	}
	return otelhttp.NewTransport(base)
}

// TemporalInterceptor returns the tracing interceptor for the Temporal client
// and worker, or nil when tracing is off. It implements both the client and the
// worker interceptor interfaces.
func TemporalInterceptor() interceptor.Interceptor {
	if !enabled {
		return nil
	}
	ti, err := temporalotel.NewTracingInterceptor(temporalotel.TracerOptions{})
	if err != nil {
		return nil
	}
	return ti
}

// ClientInterceptors are the Temporal client interceptors, or nil when tracing
// is off.
func ClientInterceptors() []interceptor.ClientInterceptor {
	ti := TemporalInterceptor()
	if ti == nil {
		return nil
	}
	return []interceptor.ClientInterceptor{ti}
}

// WorkerInterceptors are the Temporal worker interceptors, or nil when tracing
// is off.
func WorkerInterceptors() []interceptor.WorkerInterceptor {
	ti := TemporalInterceptor()
	if ti == nil {
		return nil
	}
	return []interceptor.WorkerInterceptor{ti}
}

// PGXTracer returns the connection tracer to install on a pool: the otelpgx
// tracer fanned out with base (the pool metrics tracer; nil for a direct
// engine's pool, #380), or base unchanged when tracing is off. pgx allows a
// single tracer, so this combines the two. The span never gets the SQL text
// nor a query error's text (see spanSafeError).
func PGXTracer(base pgx.QueryTracer) pgx.QueryTracer {
	if !enabled {
		return base
	}
	return &pgxTracer{base: base, otel: otelpgx.NewTracer(otelpgx.WithDisableSQLStatementInAttributes())}
}

// pgxTracer fans the QueryTracer and pool AcquireTracer calls out to both
// tracers. The richer otelpgx methods (connect, prepare, batch, copy) are not
// forwarded: pgx calls them only when the tracer implements their interfaces,
// which this combiner deliberately does not.
type pgxTracer struct {
	base pgx.QueryTracer
	otel *otelpgx.Tracer
}

func (t *pgxTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if t.base != nil {
		ctx = t.base.TraceQueryStart(ctx, conn, data)
	}
	return t.otel.TraceQueryStart(ctx, conn, data)
}

func (t *pgxTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	if t.base != nil {
		t.base.TraceQueryEnd(ctx, conn, data)
	}
	// otelpgx records the error's text on the span; a PostgreSQL message can
	// quote a value the user sent. The span gets the SQLSTATE only.
	data.Err = spanSafeError(data.Err)
	t.otel.TraceQueryEnd(ctx, conn, data)
}

// spanSafeError replaces err with one naming its SQLSTATE, or its kind, and
// none of its text.
func spanSafeError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("postgres error %s", pgErr.Code)
	}
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	}
	return errors.New("query failed")
}

func (t *pgxTracer) TraceAcquireStart(ctx context.Context, pool *pgxpool.Pool, data pgxpool.TraceAcquireStartData) context.Context {
	if a, ok := t.base.(pgxpool.AcquireTracer); ok {
		ctx = a.TraceAcquireStart(ctx, pool, data)
	}
	return t.otel.TraceAcquireStart(ctx, pool, data)
}

func (t *pgxTracer) TraceAcquireEnd(ctx context.Context, pool *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	if a, ok := t.base.(pgxpool.AcquireTracer); ok {
		a.TraceAcquireEnd(ctx, pool, data)
	}
	t.otel.TraceAcquireEnd(ctx, pool, data)
}

// Tracer is the tracer of one Baleia package, named after its path under
// internal/ (#380). Call it when starting a span, not once into a variable: a
// tracer taken from the global provider stays bound to the first provider
// installed. Until Setup installs a provider its spans are no-ops.
func Tracer(pkg string) trace.Tracer {
	return otel.Tracer("github.com/galileostudio/baleia/internal/" + pkg)
}

// FailSpan marks span failed with reason, a short fixed string. The error's
// text is not recorded: it can carry SQL, object keys or user data.
func FailSpan(span trace.Span, err error, reason string) {
	if err != nil {
		span.SetStatus(codes.Error, reason)
	}
}
