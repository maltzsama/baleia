package obs

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestSetupDisabledIsNoop: with tracing off, nothing is installed and a span
// from the global provider does not record.
func TestSetupDisabledIsNoop(t *testing.T) {
	shutdown, err := Setup(context.Background(), false, "api")
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
