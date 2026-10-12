package server

import (
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/galileostudio/baleia/internal/metrics"
)

// HTTPMetrics records request count and latency per low-cardinality route
// template. It reuses routePattern from logging.go so the metric label and the
// log field stay aligned, and sits outside StructuredLogger so a recovered
// panic is still counted as a 5xx.
func HTTPMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		route := routePattern(r)
		metrics.HTTPRequests.WithLabelValues(route, r.Method, metrics.StatusClass(status)).Inc()
		metrics.HTTPDuration.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
	})
}

// metricsHandler serves the Prometheus registry. Scraping is gated on the
// configured token so /metrics is not world-readable on a shared network.
func (s *Server) metricsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.MetricsToken != "" {
			presented := r.Header.Get("X-Metrics-Token")
			if presented == "" {
				bearer := r.Header.Get("Authorization")
				if len(bearer) > 7 && bearer[:7] == "Bearer " {
					presented = bearer[7:]
				}
			}
			if subtle.ConstantTimeCompare([]byte(presented), []byte(s.cfg.MetricsToken)) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"code":"unauthorized","message":"invalid metrics token"}`))
				return
			}
		}
		metrics.Handler().ServeHTTP(w, r)
	}
}
