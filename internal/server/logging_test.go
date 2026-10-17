package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

func logEntry(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &entry); err != nil {
		t.Fatalf("log line is not valid JSON: %v (%s)", err, buf.String())
	}
	return entry
}

func TestStructuredLoggerFieldsAndRedaction(t *testing.T) {
	logger, buf := newTestLogger()

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(RequestIDHeader)
	router.Use(StructuredLogger(logger))
	router.Get("/api/v1/workspaces/{id}/executions", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(11 * time.Millisecond)
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("ok"))
	})

	req := httptest.NewRequest("GET", "/api/v1/workspaces/ws-1/executions?token=supersecret", nil)
	req.Header.Set("X-Request-ID", "req-123")
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKeyUserID, "user-42"))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "req-123" {
		t.Fatalf("X-Request-ID response header = %q, want req-123", got)
	}

	entry := logEntry(t, buf)
	if entry["request_id"] != "req-123" {
		t.Fatalf("request_id = %v", entry["request_id"])
	}
	if entry["route"] != "/api/v1/workspaces/{id}/executions" {
		t.Fatalf("route = %v (want the low-cardinality pattern)", entry["route"])
	}
	if entry["status"] != float64(http.StatusTeapot) {
		t.Fatalf("status = %v", entry["status"])
	}
	if entry["actor_id"] != "user-42" {
		t.Fatalf("actor_id = %v", entry["actor_id"])
	}
	if entry["method"] != "GET" {
		t.Fatalf("method = %v", entry["method"])
	}

	// duration_ms must actually reflect elapsed time (the handler slept 11ms).
	dur, ok := entry["duration_ms"].(float64)
	if !ok {
		t.Fatalf("duration_ms = %v (not numeric)", entry["duration_ms"])
	}
	if dur < 11 {
		t.Fatalf("duration_ms = %v, want >= 11 (handler slept 11ms)", dur)
	}

	// Query parameters (which can carry secrets) must never reach the log.
	if strings.Contains(buf.String(), "supersecret") {
		t.Fatalf("query secret leaked into the log: %s", buf.String())
	}
}

func TestStructuredLoggerRecordsFinalStatusAfterInformational(t *testing.T) {
	logger, buf := newTestLogger()

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(StructuredLogger(logger))
	router.Get("/slow", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusEarlyHints) // 103, informational
		w.WriteHeader(http.StatusOK)         // final
	})

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/slow", nil))

	if got := logEntry(t, buf)["status"]; got != float64(http.StatusOK) {
		t.Fatalf("status = %v, want 200 (the final status, not the 103)", got)
	}
}

// TestResponseRecorderAllowsUpgrades: the logger wrapper must let a WebSocket
// upgrade hijack the connection (the execution event channel).
func TestResponseRecorderAllowsUpgrades(t *testing.T) {
	var _ http.Hijacker = &responseRecorder{}
	var _ http.Flusher = &responseRecorder{}
}

func TestStructuredLoggerRecordsActorFromHandler(t *testing.T) {
	logger, buf := newTestLogger()

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(StructuredLogger(logger))
	router.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		// A handler that resolves the actor itself, like the WebSocket flow.
		auth.SetLogActor(r.Context(), "ws-user-7")
		w.WriteHeader(http.StatusSwitchingProtocols) // 101
	})

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/ws", nil))

	entry := logEntry(t, buf)
	if entry["actor_id"] != "ws-user-7" {
		t.Fatalf("actor_id = %v, want ws-user-7", entry["actor_id"])
	}
	if entry["status"] != float64(http.StatusSwitchingProtocols) {
		t.Fatalf("status = %v, want 101", entry["status"])
	}
}

// TestStructuredLoggerCarriesTraceID: a request handled inside a span logs the
// span's trace_id and span_id (#47).
func TestStructuredLoggerCarriesTraceID(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	logger, buf := newTestLogger()
	inner := StructuredLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	outer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tp.Tracer("test").Start(r.Context(), "op")
		defer span.End()
		inner.ServeHTTP(w, r.WithContext(ctx))
	})

	rec := httptest.NewRecorder()
	outer.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	if !strings.Contains(buf.String(), spans[0].SpanContext.TraceID().String()) {
		t.Errorf("log does not carry the trace id: %s", buf.String())
	}
	if !strings.Contains(buf.String(), spans[0].SpanContext.SpanID().String()) {
		t.Errorf("log does not carry the span id: %s", buf.String())
	}
}
