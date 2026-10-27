package obs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestSetupDisabledIsNoop: with tracing off, nothing is installed and a span
// from the global provider does not record.
func TestSetupDisabledIsNoop(t *testing.T) {
	shutdown, err := Setup(context.Background(), slog.New(slog.DiscardHandler), false, "api")
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if Enabled() {
		t.Fatal("tracing installed while disabled")
	}
	_, span := otel.Tracer("test").Start(context.Background(), "x")
	defer span.End()
	if span.IsRecording() {
		t.Error("a span records while tracing is disabled")
	}
}

// TestTraceAcrossHTTPAndClient: an instrumented server handler and an
// instrumented client share one trace: the downstream request continues the
// upstream trace.
func TestTraceAcrossHTTPAndClient(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	install(tp)
	defer func() { enabled = false }()

	down := httptest.NewServer(HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "downstream"))
	defer down.Close()

	up := httptest.NewServer(HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, down.URL, nil)
		resp, err := WrapHTTPClient(&http.Client{}).Do(req)
		if err != nil {
			t.Errorf("downstream call: %v", err)
			return
		}
		_ = resp.Body.Close()
		w.WriteHeader(http.StatusOK)
	}), "http.server"))
	defer up.Close()

	resp, err := http.Get(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	spans := exp.GetSpans()
	if len(spans) < 3 {
		t.Fatalf("want at least 3 spans (server, client, downstream server), got %d: %+v", len(spans), spans)
	}
	traceID := spans[0].SpanContext.TraceID()
	for _, s := range spans {
		if s.SpanContext.TraceID() != traceID {
			t.Errorf("span %q has trace %s, want %s", s.Name, s.SpanContext.TraceID(), traceID)
		}
	}
}

// TestNoSQLInSpanAttributes: the SQL a statement carries in the request body
// never becomes a span attribute (bodies are not captured).
func TestNoSQLInSpanAttributes(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	install(tp)
	defer func() { enabled = false }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"q","nextUri":"","columns":[{"name":"c","type":"varchar"}],"data":[["x"]]}`))
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	req.Body = http.NoBody
	// The client sends the SQL in the body; simulate it.
	req.Body = io.NopCloser(strings.NewReader("SELECT secret FROM t"))
	resp, err := WrapHTTPClient(&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	for _, s := range exp.GetSpans() {
		for _, a := range s.Attributes {
			if strings.Contains(strings.ToUpper(a.Value.String()), "SELECT SECRET") {
				t.Errorf("span %q leaked the SQL text in attribute %q", s.Name, a.Key)
			}
		}
	}
}

// TestSpanSafeError: a query error reaches the span as its SQLSTATE or kind,
// never with a PostgreSQL message that can quote a submitted value (#380).
func TestSpanSafeError(t *testing.T) {
	pg := &pgconn.PgError{Code: "22P02", Message: `invalid input syntax for type integer: "p4ssw0rd"`}
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"postgres": {fmt.Errorf("query: %w", pg), "postgres error 22P02"},
		"canceled": {context.Canceled, context.Canceled.Error()},
		"other":    {errors.New(`dial "secret-host"`), "query failed"},
	} {
		got := spanSafeError(tc.err)
		if got == nil || got.Error() != tc.want || strings.Contains(got.Error(), "p4ssw0rd") {
			t.Errorf("%s = %v, want %q", name, got, tc.want)
		}
	}
	if spanSafeError(nil) != nil {
		t.Error("nil error was not kept nil")
	}
}

// TestPGXTracer_NilBase: a direct engine's pool has no metrics tracer; the
// combined tracer must not call through a nil base.
func TestPGXTracer_NilBase(t *testing.T) {
	tr := &pgxTracer{otel: otelpgx.NewTracer(otelpgx.WithDisableSQLStatementInAttributes())}
	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: &pgconn.PgError{Code: "42P01"}})
}
