package obs

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"time"
)

// PprofMux serves the runtime profiles under /debug/pprof/ (#380), on a mux
// of its own. Importing net/http/pprof also registers them on
// http.DefaultServeMux; no Baleia server serves that mux, which
// TestNoDefaultServeMux (internal/arch_test.go) enforces.
func PprofMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}

// ServePprof serves PprofMux on addr (BALEIA_PPROF_ADDR) until ctx ends; an
// empty addr serves nothing. The listener is unauthenticated: bind it to
// loopback or a pod-only port, never to a public interface.
func ServePprof(ctx context.Context, logger *slog.Logger, addr string) {
	if addr == "" {
		return
	}
	srv := &http.Server{Addr: addr, Handler: PprofMux(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	logger.InfoContext(ctx, "pprof listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.ErrorContext(ctx, "pprof listener", "addr", addr, "err", err)
	}
}
