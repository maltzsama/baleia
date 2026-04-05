package server

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/yourusername/baleia/internal/config"
)

type Server struct {
	cfg *config.Config
	mux *chi.Mux
}

func New(cfg *config.Config) *Server {
	s := &Server{cfg: cfg, mux: chi.NewRouter()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.Use(middleware.Logger)
	s.mux.Use(middleware.Recoverer)
	s.mux.Use(middleware.RequestID)

	s.mux.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// v1 API — handlers registrados aqui
	s.mux.Route("/api/v1", func(r chi.Router) {
		// auth, workspaces, lenses, executions, catalog...
	})
}

func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{Addr: ":" + s.cfg.Port, Handler: s.mux}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	return srv.ListenAndServe()
}
