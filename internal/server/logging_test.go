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

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func TestStructuredLoggerFieldsAndRedaction(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(RequestIDHeader)
	router.Use(StructuredLogger(logger))
	router.Get("/api/v1/workspaces/{id}/executions", func(w http.ResponseWriter, r *http.Request) {
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

	out := buf.String()
	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &entry); err != nil {
		t.Fatalf("log line is not valid JSON: %v (%s)", err, out)
	}
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
	if _, ok := entry["duration_ms"]; !ok {
		t.Fatal("duration_ms missing")
	}

	// Query parameters (which can carry secrets) must never reach the log.
	if strings.Contains(out, "supersecret") {
		t.Fatalf("query secret leaked into the log: %s", out)
	}
}
