package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/galileostudio/baleia/internal/agentpolicy"
	"github.com/galileostudio/baleia/internal/api"
	"github.com/galileostudio/baleia/internal/app"
	"github.com/galileostudio/baleia/internal/assistant"
	assistantllm "github.com/galileostudio/baleia/internal/assistant/llm"
	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/mcp"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/obs"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/ws"
	"github.com/galileostudio/baleia/internal/wsticket"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	cfg     *config.Config
	mux     *chi.Mux
	pool    *pgxpool.Pool
	queries *db.Queries
	metrics *metrics.Registry

	handlers *api.API
	// background is the handlers' work that outlives a request; Run waits
	// for it on shutdown.
	background drainer
	strict     api.ServerInterface
	ws         *ws.Handler
	mcp        *mcp.Server // nil unless agents are enabled
	// rl and authLimiter run cleanup goroutines; Run stops them on shutdown.
	rl               *RateLimiter
	authLimiter      *AuthRouteLimiter
	onRefreshLimited func()
}

// drainer waits, until ctx ends, for work left running in the background.
type drainer interface {
	Drain(ctx context.Context) error
}

// New builds the API on the shared graph (internal/app). ctx is the process
// context: background work started here (the assistant's in-process MCP
// session and sweeper) ends with it.
func New(ctx context.Context, core *app.Core) (*Server, error) {
	cfg := core.Config
	s := &Server{cfg: cfg, mux: chi.NewRouter(), pool: core.Pool, queries: core.Queries, metrics: core.Metrics}

	// WebSocket needs a raw connection. The API issues single-use tickets (#26)
	// that the WebSocket handler consumes; the JWT never appears in the URL.
	tickets := wsticket.New()
	// The assistant runner needs the MCP server, which needs these handlers:
	// they get the slot now and the runner once it is built (#363).
	runner := &assistant.RunnerSlot{}
	h, err := api.NewAPI(api.Deps{
		Data:          api.DataDeps{Queries: core.Queries, Pool: core.Pool},
		Engines:       api.EngineDeps{Connections: core.Connections, Engines: core.Engines, Kinds: core.Engines, Dialects: core.Dialects, Binder: core.Binder, Dialer: core.Dialer},
		Observability: api.ObservabilityDeps{Audit: core.Audit, Metrics: core.Metrics},
		Settings:      api.SettingsFromConfig(cfg),
		Keyring:       core.Keyring,
		Temporal:      core.Temporal,
		Storage:       core.Storage,
		WSTickets:     tickets,
		Assistant:     runner,
	})
	if err != nil {
		return nil, err
	}
	s.handlers = h
	s.background = h

	authMetrics := h.AuthMetrics()
	s.onRefreshLimited = func() { authMetrics.Attempt(metrics.AuthRefresh, metrics.OutcomeRateLimited) }
	s.rl = NewRateLimiter(RateLimiterConfig{RequestsPerSecond: cfg.RateLimitRPS, Burst: cfg.RateLimitBurst})
	// Strict per-IP limits on the password-guessing surface (#51). They are
	// per replica: a multi-replica deployment needs a shared limiter to enforce
	// them across the fleet.
	s.authLimiter = NewAuthRouteLimiter([]AuthRouteLimit{
		{Path: "/api/v1/auth/login", Kind: metrics.AuthPassword, Every: time.Minute, Burst: 10},
		{Path: "/api/v1/auth/password-reset/request", Kind: metrics.AuthPasswordResetRequest, Every: 15 * time.Minute, Burst: 5},
		{Path: "/api/v1/auth/password-reset/confirm", Kind: metrics.AuthPasswordResetConfirm, Every: 15 * time.Minute, Burst: 10},
	}, func(kind string) { authMetrics.Attempt(kind, metrics.OutcomeRateLimited) })

	s.ws = ws.New(core.Queries.AsWSQuerier(), cfg.JWTSecret, cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3UseSSL)
	s.ws.SetTickets(tickets)

	s.strict = api.NewStrictHandlerWithOptions(h, []api.StrictMiddlewareFunc{
		authMiddlewareFunc(cfg.JWTSecret, core.Queries),
		csrfMiddleware,
		refreshCookieMiddleware(cfg),
		oidcMiddleware(cfg),
	}, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  strictRequestError,
		ResponseErrorHandlerFunc: strictResponseError,
	})

	// The MCP surface is mounted outside /api/v1: agent tokens are valid only
	// here (#58), and the streamable handler authenticates every request.
	if cfg.AgentsEnabled {
		gate := agentpolicy.NewGate(core.Queries, engineCapabilities{h: h}, h.AgentCeilings(), core.Dialects)
		gate.SetObserver(core.Metrics.Agent())
		s.mcp = mcp.New(h, gate, core.Queries, mcp.NewAuditHooks(core.Audit), cfg.MCPAllowedOrigins)
		startAssistant(ctx, h, s.mcp, core.Dialer, runner)
	}

	s.routes()
	return s, nil
}

// startAssistant provisions the assistant (#62) when the agents feature is
// on. It runs turns through an in-memory MCP client, so the same gate and
// audit hooks apply.
func startAssistant(ctx context.Context, h *api.API, mcpServer *mcp.Server, dialer *connections.Dialer, slot *assistant.RunnerSlot) {
	svc := h.AssistantService()
	if svc == nil {
		return
	}
	session, err := mcpServer.InProcessClient(ctx)
	if err != nil {
		slog.WarnContext(ctx, "assistant disabled: in-process MCP client", "err", err)
		return
	}
	runner := assistant.NewRunner(svc, nil, assistant.NewMCPToolCaller(session))
	// Without stored settings the runner starts with no provider; saving the
	// settings sets one.
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	settings, err := svc.Settings(sctx)
	switch {
	case err != nil:
		slog.WarnContext(ctx, "assistant: settings not loaded at startup", "err", err)
	case settings.Enabled && settings.Provider != nil && settings.Model != nil:
		if key, err := svc.APIKey(sctx); err != nil {
			slog.WarnContext(ctx, "assistant: API key not loaded at startup", "err", err)
		} else {
			client := obs.WrapHTTPClient(dialer.HTTPClient(assistantllm.ClientTimeout))
			runner.SetProvider(assistantllm.NewHTTPProvider(client, *settings.Provider, derefString(settings.BaseUrl), key, *settings.Model))
		}
	}
	slot.Store(runner)
	go runner.RunSweeper(ctx, 30*time.Second)
}

// routes mounts what New built; it builds nothing (#362).
func (s *Server) routes() {
	s.mux.Use(middleware.RequestID)
	s.mux.Use(obs.RouteName)               // names the tracing span after chi has routed (#47)
	s.mux.Use(s.metrics.HTTP().Middleware) // route templates, never raw paths (#87)
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
	if s.cfg.MetricsEnabled {
		metricsHandler := s.metrics.Handler(s.cfg.MetricsToken)
		s.mux.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
			metricsHandler.ServeHTTP(w, r)
		})
	}

	s.mux.Get("/api/v1/ws/workspaces/{id}/executions/{execution_id}", s.ws.StreamExecution)

	if s.mcp != nil {
		s.mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
			s.mcp.Handler().ServeHTTP(w, r)
		})
	}

	s.mux.Route("/api/v1", func(r chi.Router) {
		r.Use(audit.Middleware)
		r.Use(refreshCookieContext)
		r.Use(RateLimitRefreshMiddleware(s.rl, s.onRefreshLimited))
		r.Use(s.authLimiter.Middleware)
		r.Use(OptionalJSONBody)
		api.HandlerFromMux(s.strict, r)

		// Non-oapi routes (dynamic catalog browsing) share the chi auth
		// middleware; handlers read the caller from the request context.
		r.Group(func(r chi.Router) {
			r.Use(chiAuthMiddleware(s.cfg.JWTSecret, s.queries))
			r.Get("/workspaces/{id}/catalogs", s.handlers.ListWorkspaceCatalogs)
			r.Get("/connections/{connection_id}/catalogs/{catalog_name}/namespaces", s.handlers.HandleListNamespaces)
			r.Get("/connections/{connection_id}/catalogs/{catalog_name}/namespaces/{namespace}/tables", s.handlers.HandleListTables)
			r.Get("/connections/{connection_id}/catalogs/{catalog_name}/namespaces/{namespace}/tables/{table}/columns", s.handlers.HandleListColumns)
		})
	})

	static := staticHandler(s.queries, "web/build")
	s.mux.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		static.ServeHTTP(w, r)
	}))
}

// staticUsers tells the static handler whether the first admin exists.
type staticUsers interface {
	HasUsers(ctx context.Context) (bool, error)
}

// engineCapabilities adapts the API's engine registry to agentpolicy.
type engineCapabilities struct{ h *api.API }

func (e engineCapabilities) ReadOnly(ctx context.Context, id pgtype.UUID) (queryengine.ReadOnlySupport, error) {
	return e.h.EngineReadOnly(ctx, id)
}

// staticHandler serves the SPA. The root is the app shell, never the directory
// listing of the build, and a prerendered .html file redirects to its
// extensionless route so the client router matches it.
func staticHandler(users staticUsers, dir string) http.Handler {
	fileServer := http.FileServer(http.Dir(dir))
	// Once the first admin exists the install never goes back to setup, so
	// the answer is cached and the database is no longer asked (#375).
	var setUp atomic.Bool
	configured := func(ctx context.Context) bool {
		if setUp.Load() {
			return true
		}
		ok, err := users.HasUsers(ctx)
		if err == nil && ok {
			setUp.Store(true)
		}
		return err == nil && ok
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if strings.HasPrefix(path, "/api/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if path == "/" || path == "/setup" || strings.HasSuffix(path, ".html") {
			configured := configured(r.Context())

			if !configured && path != "/setup" {
				http.Redirect(w, r, "/setup", http.StatusFound)
				return
			}
			if configured && path == "/setup" {
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
			if path == "/" {
				http.ServeFile(w, r, filepath.Join(dir, "app.html"))
				return
			}
			if strings.HasSuffix(path, ".html") && path != "/app.html" {
				http.Redirect(w, r, strings.TrimSuffix(path, ".html"), http.StatusFound)
				return
			}
		}

		// A route that is also a build directory (/connections holds the
		// prerendered connections/new.html) is the app, never a listing.
		fullPath := dir + path
		if info, err := os.Stat(fullPath); os.IsNotExist(err) || (err == nil && info.IsDir()) {
			http.ServeFile(w, r, filepath.Join(dir, "app.html"))
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func authMiddlewareFunc(secret string, queries db.Querier) api.StrictMiddlewareFunc {
	publicOps := map[string]bool{
		"GetSetupStatus":       true,
		"CreateFirstAdmin":     true,
		"Login":                true,
		"ListAuthProviders":    true,
		"ListOIDCProviders":    true,
		"OidcRedirect":         true,
		"OidcCallback":         true,
		"RequestPasswordReset": true,
		"ConfirmPasswordReset": true,
		"RefreshToken":         true,
		// Logout authenticates by the refresh cookie it revokes: it must work
		// without an access token, which the browser does not send (#263).
		"Logout": true,
	}

	return func(f api.StrictHandlerFunc, operationID string) api.StrictHandlerFunc {
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, args any) (any, error) {
			// API keys are checked before the public operations so a key can
			// never reach /auth/* (#276).
			if raw := r.Header.Get("X-Api-Key"); raw != "" {
				if apiKeyForbidden(r.URL.Path) {
					writeAuthError(w, http.StatusForbidden, "forbidden", "api keys cannot call this route")
					return nil, nil
				}
				apiCtx, ok := authenticateAPIKey(ctx, queries, raw)
				if !ok {
					writeAuthError(w, http.StatusUnauthorized, "unauthorized", "invalid api key")
					return nil, nil
				}
				return f(apiCtx, w, r, args)
			}

			if publicOps[operationID] {
				return f(ctx, w, r, args)
			}

			token := extractToken(r)
			if token == "" {
				writeAuthError(w, http.StatusUnauthorized, "unauthorized", "missing token")
				return nil, nil
			}
			// Agent tokens are valid only on /mcp (#59). Every REST surface
			// rejects them explicitly instead of parsing them as a JWT.
			if auth.IsAgentToken(token) {
				writeAuthError(w, http.StatusUnauthorized, "agent_token_not_allowed", "agent tokens are only valid on /mcp")
				return nil, nil
			}

			claims, err := auth.ParseToken(token, secret)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
				return nil, nil
			}

			ctx = context.WithValue(ctx, auth.ContextKeyUserID, claims.UserID)
			ctx = context.WithValue(ctx, auth.ContextKeyRole, claims.Role)
			ctx = audit.WithAuthMethod(ctx, audit.AuthAccessToken)
			return f(ctx, w, r, args)
		}
	}
}

func chiAuthMiddleware(secret string, queries db.Querier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if raw := r.Header.Get("X-Api-Key"); raw != "" {
				if apiKeyForbidden(r.URL.Path) {
					writeAuthError(w, http.StatusForbidden, "forbidden", "api keys cannot call this route")
					return
				}
				ctx, ok := authenticateAPIKey(r.Context(), queries, raw)
				if !ok {
					writeAuthError(w, http.StatusUnauthorized, "unauthorized", "invalid api key")
					return
				}
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			token := extractToken(r)
			if token == "" {
				writeAuthError(w, http.StatusUnauthorized, "unauthorized", "missing token")
				return
			}
			if auth.IsAgentToken(token) {
				writeAuthError(w, http.StatusUnauthorized, "agent_token_not_allowed", "agent tokens are only valid on /mcp")
				return
			}
			claims, err := auth.ParseToken(token, secret)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
				return
			}
			ctx := context.WithValue(r.Context(), auth.ContextKeyUserID, claims.UserID)
			ctx = context.WithValue(ctx, auth.ContextKeyRole, claims.Role)
			ctx = audit.WithAuthMethod(ctx, audit.AuthAccessToken)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func apiKeyForbidden(path string) bool {
	return strings.HasPrefix(path, "/api/v1/auth/") ||
		path == "/api/v1/me/password" ||
		strings.HasPrefix(path, "/api/v1/api-keys") ||
		// Agent management and token minting accept user JWTs only (#59): no
		// API key and no agent token may mint another agent token.
		strings.HasPrefix(path, "/api/v1/agents")
}

func authenticateAPIKey(ctx context.Context, queries db.Querier, raw string) (context.Context, bool) {
	key, err := queries.GetAPIKeyByHash(ctx, auth.HashAPIKey(raw))
	if err != nil {
		return ctx, false
	}
	if key.ExpiresAt.Valid && !key.ExpiresAt.Time.After(time.Now()) {
		return ctx, false
	}
	if !slices.Contains(key.Scopes, "*") {
		return ctx, false
	}
	user, err := queries.GetUserByID(ctx, key.UserID)
	if err != nil {
		return ctx, false
	}
	// A suspended user cannot use an API key (#283). The response stays the
	// uniform "invalid api key" so the key's owner is not revealed.
	if err := auth.RequireActive(user.Status); err != nil {
		return ctx, false
	}
	ctx = context.WithValue(ctx, auth.ContextKeyUserID, user.ID.String())
	ctx = context.WithValue(ctx, auth.ContextKeyRole, user.Role)
	ctx = audit.WithAuthMethod(ctx, audit.AuthAPIKey)
	// Best effort; never log the key or its hash.
	// Best effort: a failed touch must not reject a valid key.
	_ = queries.TouchAPIKey(ctx, key.ID)
	return ctx, true
}

func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"code":"` + code + `","message":"` + message + `"}`))
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

// Run serves until ctx ends, then drains in-flight requests, and then the
// handlers' background work, for at most HTTPShutdownTimeout, or without a
// deadline when it is zero (#372). A clean shutdown returns nil; background
// work cut at the deadline is logged, not an exit failure. The rate limiters
// stop when Run returns.
func (s *Server) Run(ctx context.Context) error {
	defer s.stopLimiters()
	srv := &http.Server{
		Addr:    ":" + s.cfg.Port,
		Handler: obs.HTTPHandler(s.mux, "http.server"),
		// No ReadTimeout/WriteTimeout: downloads, SSE and WebSockets are
		// long-lived. Slow headers and idle keep-alives are bounded.
		ReadHeaderTimeout: s.cfg.HTTPReadHeaderTimeout,
		IdleTimeout:       s.cfg.HTTPIdleTimeout,
	}
	shutdown := make(chan error, 1)
	go func() {
		<-ctx.Done()
		// Zero drains with no deadline.
		sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		if s.cfg.HTTPShutdownTimeout > 0 {
			sctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), s.cfg.HTTPShutdownTimeout)
		}
		defer cancel()
		err := srv.Shutdown(sctx)
		if s.background != nil {
			if derr := s.background.Drain(sctx); derr != nil {
				slog.WarnContext(sctx, "background work still running at shutdown", "err", derr)
			}
		}
		shutdown <- err
	}()
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return <-shutdown
}

// stopLimiters ends the limiters' cleanup goroutines.
func (s *Server) stopLimiters() {
	if s.rl != nil {
		s.rl.Stop()
	}
	if s.authLimiter != nil {
		s.authLimiter.Stop()
	}
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
