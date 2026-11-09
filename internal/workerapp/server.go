package workerapp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/galileostudio/baleia/internal/metrics"
)

// MetricsMux serves the worker's /metrics and /healthz.
func MetricsMux(reg *metrics.Registry, token string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", reg.Handler(token))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	return mux
}

// ServeMetrics starts the worker's own listener: /metrics (Temporal SDK, pool,
// build) and /healthz. Prometheus scrapes it directly; it is never behind the
// JWT. An empty addr serves nothing. The returned function shuts it down.
func ServeMetrics(ctx context.Context, logger *slog.Logger, addr string, reg *metrics.Registry, token string) func() {
	if addr == "" {
		return func() {}
	}
	srv := &http.Server{Addr: addr, Handler: MetricsMux(reg, token), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(ctx, "worker metrics listener", "addr", addr, "err", err)
		}
	}()
	return func() { _ = srv.Close() }
}

// ServeHealth exposes a liveness endpoint Kubernetes can probe. If the port
// cannot be bound the worker still starts; the probe simply stays down.
func ServeHealth(ctx context.Context, logger *slog.Logger, port string) {
	if _, err := strconv.Atoi(port); err != nil {
		logger.ErrorContext(ctx, "invalid health port", "port", port, "err", err)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		logger.ErrorContext(ctx, "health server", "port", port, "err", err)
	}
}
