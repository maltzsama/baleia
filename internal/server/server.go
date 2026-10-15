package server

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	baleiaapi "github.com/galileostudio/baleia/internal/api"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/connections/sqlite"
	"github.com/galileostudio/baleia/internal/queryengine"
	// Engine kinds register themselves.
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/metrics"
	_ "github.com/galileostudio/baleia/internal/queryengine/trino"
	"github.com/galileostudio/baleia/internal/storage"
	"github.com/galileostudio/baleia/internal/trino"
	"github.com/galileostudio/baleia/internal/ws"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
)

type Server struct {
	cfg      *config.Config
	mux      *chi.Mux
	pool     *pgxpool.Pool
	queries  *db.Queries
	rl       *RateLimiter
	keyring  *crypto.Keyring
	temporal client.Client
	metrics  *metrics.Registry
}

// New builds the API server. The Temporal client is dialed by the caller
// (with retries, internal/temporal/dial); metrics may be nil.
func New(cfg *config.Config, pool *pgxpool.Pool, keyring *crypto.Keyring, temporal client.Client, reg *metrics.Registry) *Server {
	queries := db.New(pool)
	rl := NewRateLimiter(RateLimiterConfig{
		RequestsPerSecond: cfg.RateLimitRPS,
		Burst:             cfg.RateLimitBurst,
	})
	s := &Server{cfg: cfg, mux: chi.NewRouter(), pool: pool, queries: queries, rl: rl, keyring: keyring, temporal: temporal, metrics: reg}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.Use(middleware.RequestID)
	if s.metrics != nil {
		s.mux.Use(s.metrics.HTTP().Middleware) // route templates, never raw paths (#87)
	}
	s.mux.Use(RequestIDHeader)
	s.mux.Use(StructuredLogger(slog.Default()))
	s.mux.Use(middleware.Recoverer)

	s.mux.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Readiness: the API is ready when the database answers a ping. Kubernetes
	// and the smoke script poll this before sending traffic.
	s.mux.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.pool.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"unavailable"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	s.mux.Get("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "api/swagger.yaml")
	})

	// Prometheus scrape endpoint (#87): no tenant data, gated by the optional
	// BALEIA_METRICS_TOKEN instead of the user JWT.
	if s.cfg.MetricsEnabled && s.metrics != nil {
		metricsHandler := s.metrics.Handler(s.cfg.MetricsToken)
		s.mux.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
			metricsHandler.ServeHTTP(w, r)
		})
	}
	temporalClient := s.temporal

	s3Client := storage.New(s.cfg.S3Endpoint, s.cfg.S3AccessKey, s.cfg.S3SecretKey, s.cfg.S3Bucket, s.cfg.S3UseSSL)

	// Every connection provider dials through one SSRF-safe dialer
	// (docs/adr/0011).
	dialer, err := connections.NewDialer(connections.DialerOptions{
		DenyCIDRs:     s.cfg.ConnectionsDenyCIDRs,
		AllowCIDRs:    s.cfg.ConnectionsAllowCIDRs,
		AllowLoopback: s.cfg.ConnectionsAllowLoopback,
	})
	if err != nil {
		panic("invalid connection network settings: " + err.Error())
	}
	conns, err := providers.New(dialer, providers.Options{
		SQLite: sqlite.Options{Root: s.cfg.SQLiteRoot, AllowWrite: s.cfg.SQLiteAllowWrite},
	})
	if err != nil {
		panic("connection providers: " + err.Error())
	}

	// Engines run SQL (docs/adr/0002); catalog DDL runs as the Trino admin
	// identity (docs/adr/0005). Both dial through the connections dialer.
	engines := queryengine.NewManager(s.queries, s.keyring, conns, dialer)
	trinoAdmin := trino.NewWithHTTPClient(s.cfg.TrinoURL, s.cfg.TrinoAdminUser, dialer.HTTPClient(60*time.Second))
	binder := bindtrino.New(trinoAdmin, s.queries)

	h := baleiaapi.NewHandlers(s.queries, s.pool, s.cfg, s.keyring, temporalClient, s3Client, conns, engines, binder)
	var onRefreshLimited func()
	if s.metrics != nil {
		h.SetMetrics(s.metrics)
		s3Client.SetMetrics(s.metrics.Storage())
		auth := h.AuthMetrics()
		onRefreshLimited = func() { auth.Attempt(metrics.AuthRefresh, metrics.OutcomeRateLimited) }
	}

	// OIDC needs cookie + redirect
	s.mux.Get("/api/v1/auth/oidc/{provider_id}/login", h.HandleOIDCRedirect)
	s.mux.Get("/api/v1/auth/oidc/{provider_id}/callback", h.HandleOIDCCallback)

	// WebSocket needs a raw connection
	wsHandler := ws.New(s.queries.AsWSQuerier(), s.cfg.JWTSecret, s.cfg.S3Endpoint, s.cfg.S3AccessKey, s.cfg.S3SecretKey, s.cfg.S3Bucket, s.cfg.S3UseSSL)
	s.mux.Get("/api/v1/ws/workspaces/{id}/executions/{execution_id}", wsHandler.StreamExecution)

	strictHandler := baleiaapi.NewStrictHandler(h, []baleiaapi.StrictMiddlewareFunc{
		authMiddlewareFunc(s.cfg.JWTSecret),
	})

	s.mux.Route("/api/v1", func(r chi.Router) {
		r.Use(RateLimitRefreshMiddleware(s.rl, onRefreshLimited))
		baleiaapi.HandlerFromMux(strictHandler, r)

		// Non-oapi routes (Lens and dynamic catalog browsing) share the chi auth
		// middleware; handlers read the caller from the request context.
		r.Group(func(r chi.Router) {
			r.Use(chiAuthMiddleware(s.cfg.JWTSecret))
			baleiaapi.RegisterLensRoutes(r, s.queries, s.cfg, temporalClient, engines)
			r.Get("/workspaces/{id}/catalogs", h.ListWorkspaceCatalogs)
			r.Get("/connections/{connection_id}/catalogs/{catalog_name}/namespaces", h.HandleListNamespaces)
			r.Get("/connections/{connection_id}/catalogs/{catalog_name}/namespaces/{namespace}/tables", h.HandleListTables)
			r.Get("/connections/{connection_id}/catalogs/{catalog_name}/namespaces/{namespace}/tables/{table}/columns", h.HandleListColumns)
		})
	})

	fileServer := http.FileServer(http.Dir("web/build"))
	s.mux.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if strings.HasPrefix(path, "/api/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if path == "/" || path == "/setup" {
			users, err := s.queries.ListUsers(r.Context())
			configured := err == nil && len(users) > 0

			if !configured && path == "/" {
				http.Redirect(w, r, "/setup", http.StatusFound)
				return
			}
			if configured && path == "/setup" {
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
		}

		fullPath := "web/build" + path
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			http.ServeFile(w, r, "web/build/app.html")
			return
		}

		fileServer.ServeHTTP(w, r)
	}))
}

func authMiddlewareFunc(secret string) baleiaapi.StrictMiddlewareFunc {
	publicOps := map[string]bool{
		"GetSetupStatus":       true,
		"CreateFirstAdmin":     true,
		"Login":                true,
		"ListOIDCProviders":    true,
		"OidcRedirect":         true,
		"OidcCallback":         true,
		"RequestPasswordReset": true,
		"ConfirmPasswordReset": true,
		"RefreshToken":         true,
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

func chiAuthMiddleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractToken(r)
			if token == "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"code":"unauthorized","message":"missing token"}`))
				return
			}
			claims, err := auth.ParseToken(token, secret)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"code":"unauthorized","message":"invalid token"}`))
				return
			}
			ctx := context.WithValue(r.Context(), auth.ContextKeyUserID, claims.UserID)
			ctx = context.WithValue(ctx, auth.ContextKeyRole, claims.Role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
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
