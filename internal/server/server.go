package server

import (
	"context"
	"net/http"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/server/handler"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	cfg     *config.Config
	mux     *chi.Mux
	pool    *pgxpool.Pool
	queries *db.Queries
}

func New(cfg *config.Config, pool *pgxpool.Pool) *Server {
	queries := db.New(pool)
	s := &Server{cfg: cfg, mux: chi.NewRouter(), pool: pool, queries: queries}
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

	authHandler := handler.NewAuthHandler(s.queries, s.cfg)

	s.mux.Route("/api/v1", func(r chi.Router) {
		// rotas públicas
		r.Post("/auth/setup", authHandler.Setup)
		r.Post("/auth/login", authHandler.Login)
		r.Get("/auth/oidc/{provider}", authHandler.OIDCRedirect)
		r.Get("/auth/oidc/{provider}/callback", authHandler.OIDCCallback)

		// rotas autenticadas
		r.Group(func(r chi.Router) {
			r.Use(auth.Middleware(s.cfg.JWTSecret))
			r.Get("/auth/me", authHandler.Me)
		})
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