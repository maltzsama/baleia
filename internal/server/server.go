package server

import (
	"context"
	"net/http"

	baleiaapi "github.com/galileostudio/baleia/internal/api"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
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

	s.mux.Get("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "api/swagger.yaml")
	})

	h := baleiaapi.NewHandlers(s.queries, s.cfg)

	// OIDC precisa de cookie + redirect
	s.mux.Get("/api/v1/auth/oidc/{provider_id}", h.HandleOIDCRedirect)
	s.mux.Get("/api/v1/auth/oidc/{provider_id}/callback", h.HandleOIDCCallback)

	strictHandler := baleiaapi.NewStrictHandler(h, []baleiaapi.StrictMiddlewareFunc{
		authMiddlewareFunc(s.cfg.JWTSecret),
	})

	s.mux.Route("/api/v1", func(r chi.Router) {
		baleiaapi.HandlerFromMux(strictHandler, r)
	})
}

func authMiddlewareFunc(secret string) baleiaapi.StrictMiddlewareFunc {
	publicOps := map[string]bool{
		"getSetupStatus":       true,
		"createFirstAdmin":     true,
		"login":                true,
		"listOIDCProviders":    true,
		"oidcRedirect":         true,
		"oidcCallback":         true,
		"requestPasswordReset": true,
		"confirmPasswordReset": true,
	}

	return func(f baleiaapi.StrictHandlerFunc, operationID string) baleiaapi.StrictHandlerFunc {
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, args interface{}) (interface{}, error) {
			if publicOps[operationID] {
				return f(ctx, w, r, args)
			}

			token := extractToken(r)
			if token == "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"code":"unauthorized","message":"missing token"}`))
				return nil, nil
			}

			claims, err := auth.ParseToken(token, secret)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"code":"unauthorized","message":"invalid token"}`))
				return nil, nil
			}

			ctx = context.WithValue(ctx, auth.ContextKeyUserID, claims.UserID)
			ctx = context.WithValue(ctx, auth.ContextKeyRole, claims.Role)
			return f(ctx, w, r, args)
		}
	}
}

func extractToken(r *http.Request) string {
	bearer := r.Header.Get("Authorization")
	if len(bearer) > 7 && bearer[:7] == "Bearer " {
		return bearer[7:]
	}
	if c, err := r.Cookie("token"); err == nil {
		return c.Value
	}
	return ""
}

func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{Addr: ":" + s.cfg.Port, Handler: s.mux}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	return srv.ListenAndServe()
}