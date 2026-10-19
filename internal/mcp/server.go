// Package mcp exposes Baleia's governed metadata and query execution over the
// Model Context Protocol (#58). Every tool call goes through the
// agentpolicy.Gate and the same in-process strict-server handlers the UI uses;
// the package never reaches the database, engines or object storage directly.
package mcp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/agentauth"
	"github.com/galileostudio/baleia/internal/agentpolicy"
	"github.com/galileostudio/baleia/internal/api"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/results"
	"github.com/galileostudio/baleia/internal/version"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Outcome is what the audit hook needs to know about one tool invocation
// (#61). Args are intentionally absent: SQL lives in query_executions.
type Outcome struct {
	Allowed   bool
	Reason    agentpolicy.ReasonCode
	Rows      int64
	Truncated bool
	Duration  time.Duration
}

// Hooks receives every tool invocation. #58 installs NopHooks; #61 installs the
// audit recorder.
type Hooks interface {
	OnToolCall(ctx context.Context, c agentpolicy.Call, o Outcome)
}

// NopHooks discards tool-call outcomes.
type NopHooks struct{}

// OnToolCall implements Hooks.
func (NopHooks) OnToolCall(context.Context, agentpolicy.Call, Outcome) {}

// Server is the MCP HTTP surface.
type Server struct {
	handlers *api.Handlers
	gate     *agentpolicy.Gate
	tokens   agentauth.Store
	hooks    Hooks
	origins  map[string]bool
	sdk      *mcpsdk.Server
	http     http.Handler
}

// New builds the MCP server. allowedOrigins is empty by default, which rejects
// any request carrying an Origin header (native MCP clients do not send one).
func New(h *api.Handlers, gate *agentpolicy.Gate, tokens agentauth.Store, hooks Hooks, allowedOrigins []string) *Server {
	if hooks == nil {
		hooks = NopHooks{}
	}
	s := &Server{
		handlers: h,
		gate:     gate,
		tokens:   tokens,
		hooks:    hooks,
		origins:  make(map[string]bool, len(allowedOrigins)),
	}
	for _, o := range allowedOrigins {
		if o != "" {
			s.origins[o] = true
		}
	}
	s.sdk = mcpsdk.NewServer(&mcpsdk.Implementation{Name: "baleia", Version: version.Version}, nil)
	s.registerTools()

	stream := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return s.sdk },
		&mcpsdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, Logger: slog.Default()},
	)
	s.http = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorize(w, r) {
			return
		}
		stream.ServeHTTP(w, r)
	})
	return s
}

// Handler is the http.Handler mounted at /mcp.
func (s *Server) Handler() http.Handler { return s.http }

// authorize validates the Origin (when present) and the agent bearer token,
// then puts the agent principal in the request context. The MCP SDK passes the
// request context to tool handlers, so the in-process API calls see it.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" && !s.origins[origin] {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return false
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	}
	raw := bearer(r)
	if raw == "" {
		unauthorized(w)
		return false
	}
	ident, err := agentauth.Authenticate(r.Context(), s.tokens, raw)
	if err != nil {
		var ae *agentauth.AuthError
		if errors.As(err, &ae) {
			slog.Info("agent token rejected", "prefix", ae.Prefix, "reason", ae.Reason)
		}
		unauthorized(w)
		return false
	}
	*r = *r.WithContext(auth.WithAgentPrincipal(r.Context(), ident.OwnerID.String(), ident.AgentID.String()))
	return true
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return ""
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="baleia-mcp"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

// dispatch is the one path every tool goes through: gate → handler → hook.
func (s *Server) dispatch(ctx context.Context, call agentpolicy.Call, run func(context.Context, agentpolicy.Effective) (any, error)) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return toolError("unauthorized", "agent principal missing")
	}
	dec, err := s.gate.Check(ctx, p, call)
	if err != nil {
		return toolError("internal", "policy evaluation failed")
	}
	if !dec.Allowed {
		s.hooks.OnToolCall(ctx, call, Outcome{Allowed: false, Reason: dec.Reason, Duration: time.Since(start)})
		return refused(dec)
	}
	out, err := run(ctx, dec.Effective)
	if err != nil {
		s.hooks.OnToolCall(ctx, call, Outcome{Allowed: true, Duration: time.Since(start)})
		return toolError("internal", err.Error())
	}
	rows, truncated := summarize(out)
	s.hooks.OnToolCall(ctx, call, Outcome{Allowed: true, Rows: rows, Truncated: truncated, Duration: time.Since(start)})
	return &mcpsdk.CallToolResult{}, out, nil
}

func summarize(out any) (int64, bool) {
	if r, ok := out.(results.Result); ok {
		return r.RowCount, r.Truncated
	}
	return 0, false
}

func refused(dec agentpolicy.Decision) (*mcpsdk.CallToolResult, any, error) {
	errObj := map[string]any{"code": "refused", "reason": string(dec.Reason), "detail": dec.Detail}
	if dec.RetryAfter > 0 {
		errObj["retry_after_seconds"] = int(dec.RetryAfter.Seconds())
	}
	return &mcpsdk.CallToolResult{IsError: true}, map[string]any{"error": errObj}, nil
}

func toolError(code, detail string) (*mcpsdk.CallToolResult, any, error) {
	return &mcpsdk.CallToolResult{IsError: true}, map[string]any{
		"error": map[string]any{"code": code, "detail": detail},
	}, nil
}
