package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/go-chi/chi/v5"
)

// TestRun_CleanShutdown: cancelling the context drains and returns nil, not
// http.ErrServerClosed, so the API exits 0 on SIGTERM (#372), with a drain
// deadline or without one.
func TestRun_CleanShutdown(t *testing.T) {
	t.Parallel()
	for _, timeout := range []time.Duration{5 * time.Second, 0} {
		t.Run(timeout.String(), func(t *testing.T) {
			t.Parallel()
			cleanShutdown(t, timeout)
		})
	}
}

// cleanShutdown runs the server with the given drain deadline (0: none).
func cleanShutdown(t *testing.T, timeout time.Duration) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	_ = l.Close()

	mux := chi.NewRouter()
	mux.Get("/ping", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	s := &Server{cfg: &config.Config{Port: port, HTTPReadHeaderTimeout: time.Second, HTTPShutdownTimeout: timeout}, mux: mux}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get("http://127.0.0.1:" + port + "/ping")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after shutdown = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after shutdown")
	}
}

// drainRecorder records the deadline Drain got.
type drainRecorder struct{ deadlines chan bool }

func (d drainRecorder) Drain(ctx context.Context) error {
	_, ok := ctx.Deadline()
	d.deadlines <- ok
	return nil
}

// TestRun_DrainsBackgroundWork: after the HTTP drain, Run waits for the
// handlers' background work (the password-reset deliveries, #422) under
// the same deadline.
func TestRun_DrainsBackgroundWork(t *testing.T) {
	t.Parallel()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	_ = l.Close()
	bg := drainRecorder{deadlines: make(chan bool, 1)}
	s := &Server{
		cfg:        &config.Config{Port: port, HTTPReadHeaderTimeout: time.Second, HTTPShutdownTimeout: 5 * time.Second},
		mux:        chi.NewRouter(),
		background: bg,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	select {
	case hasDeadline := <-bg.deadlines:
		if !hasDeadline {
			t.Error("Drain got no deadline; want HTTPShutdownTimeout")
		}
	default:
		t.Fatal("Run returned without draining the background work")
	}
}
