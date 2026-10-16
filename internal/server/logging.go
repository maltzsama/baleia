package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/trace"
)

// responseRecorder captures the status code and body size for logging.
type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *responseRecorder) WriteHeader(code int) {
	// Informational responses (1xx) precede the final status; only the final
	// status is recorded. 101 Switching Protocols is a terminal status for an
	// upgrade (WebSocket), so it is recorded.
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		r.ResponseWriter.WriteHeader(code)
		return
	}
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// StructuredLogger emits one JSON log line per request with the fields operators
// filter on. It is deliberately conservative: it never logs request bodies,
// headers, query parameters, or credentials, so secrets cannot reach logs here.
func StructuredLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &responseRecorder{ResponseWriter: w}

			// Give handlers a place to record an actor resolved downstream (the
			// WebSocket token flow) for the request log.
			r = r.WithContext(auth.WithLogActorHolder(r.Context()))

			next.ServeHTTP(rec, r)

			if rec.status == 0 {
				rec.status = http.StatusOK
			}

			attrs := []any{
				"request_id", middleware.GetReqID(r.Context()),
				"method", r.Method,
				"route", routePattern(r),
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"bytes", rec.bytes,
			}
			actor := auth.LogActor(r.Context())
			if actor == "" {
				actor = auth.UserIDFromContext(r.Context())
			}
			if actor != "" {
				attrs = append(attrs, "actor_id", actor)
			}
			if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
				attrs = append(attrs, "trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
			}

			logger.Info("http_request", attrs...)
		})
	}
}

// RequestIDHeader copies the chi request id into the X-Request-ID response
// header so callers can correlate a response with the server log line.
func RequestIDHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := middleware.GetReqID(r.Context()); id != "" {
			w.Header().Set("X-Request-ID", id)
		}
		next.ServeHTTP(w, r)
	})
}

// routePattern returns the matched chi route template (low cardinality) when
// available, otherwise the raw path. The query string is never included.
func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if pattern := rc.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return r.URL.Path
}
