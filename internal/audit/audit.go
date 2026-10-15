// Package audit records security- and data-relevant operations in an
// append-only store (#45). Writes are best-effort and loud: a failed write is
// logged with error_code audit_write_failed and counted
// (baleia_audit_write_failures_total), and never fails the user's request.
// Instrumenting the call sites is #43.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/go-chi/chi/v5/middleware"
)

// Outcome is how the audited operation ended.
type Outcome string

const (
	Allowed Outcome = "allowed"
	Denied  Outcome = "denied"
	Error   Outcome = "error"
)

// Bounded reasons for a denied or failed operation.
const (
	ReasonNotFound    = "not_found"
	ReasonForbidden   = "forbidden"
	ReasonValidation  = "validation"
	ReasonEngineError = "engine_error"
	ReasonConflict    = "conflict"
	ReasonInternal    = "internal"
)

// Auth methods recorded with an event.
const (
	AuthPassword    = "password"
	AuthOIDC        = "oidc"
	AuthRefreshTok  = "refresh"
	AuthAPIKey      = "api_key"
	AuthAgentToken  = "agent_token"
	AuthAccessToken = "access_token" // a request carrying an access token
)

// Event is one audited operation. Actor, auth method, correlation and the
// client come from the context.
type Event struct {
	Action       Action
	ResourceType string
	ResourceID   string
	WorkspaceID  string
	Outcome      Outcome
	Reason       string // bounded: ReasonNotFound, ReasonForbidden, …
	// Changes are field changes; values of secret fields are stored as
	// "***". Never put SQL text or credentials here.
	Changes map[string]Change
}

// Change is one field's value before and after.
type Change struct {
	From any `json:"from"`
	To   any `json:"to"`
}

// Recorder writes audit events.
type Recorder interface {
	// Record stores e. It returns the write error for tests and logs; callers
	// never fail a request on it.
	Record(ctx context.Context, e Event) error
}

// Nop discards events (tests, and code paths before #43 wires a recorder).
type Nop struct{}

// Record discards e.
func (Nop) Record(context.Context, Event) error { return nil }

type ctxKey int

const (
	keyRequest ctxKey = iota
	keyService
	keyAuthMethod
	keyAction
	keyAnonymous
)

// WithAnonymous identifies an unauthenticated caller (a failed login) by an
// opaque id, e.g. EmailFingerprint; never a raw e-mail.
func WithAnonymous(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyAnonymous, id)
}

// AsUser makes userID the actor, for operations that authenticate the caller
// themselves (login, refresh, password reset).
func AsUser(ctx context.Context, userID, method string) context.Context {
	ctx = context.WithValue(ctx, auth.ContextKeyUserID, userID)
	return WithAuthMethod(ctx, method)
}

// EmailFingerprint is the audit id of an e-mail submitted by an
// unauthenticated caller: the first 16 hex characters of its sha256.
func EmailFingerprint(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])[:16]
}

// WithAction names the operation a handler is performing, so a refusal by
// the authorization checker is recorded under it (#43). Read-only handlers
// set none: refusals of reads are not audited (no read actions exist).
func WithAction(ctx context.Context, a Action) context.Context {
	return context.WithValue(ctx, keyAction, a)
}

// ActionFrom returns the operation set by WithAction, if any.
func ActionFrom(ctx context.Context) (Action, bool) {
	a, ok := ctx.Value(keyAction).(Action)
	return a, ok
}

// RequestInfo is the client of an HTTP request.
type RequestInfo struct {
	ClientIP  netip.Addr
	UserAgent string
}

// WithRequest attaches the client of a request to ctx.
func WithRequest(ctx context.Context, ri RequestInfo) context.Context {
	return context.WithValue(ctx, keyRequest, ri)
}

// Middleware records the client of every request for the audit trail: the
// peer address (proxies' X-Forwarded-For is not trusted) and the user agent.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ri := RequestInfo{UserAgent: truncate(r.UserAgent(), 512)}
		host := r.RemoteAddr
		if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			host = h
		}
		if ip, err := netip.ParseAddr(host); err == nil {
			ri.ClientIP = ip.Unmap()
		}
		next.ServeHTTP(w, r.WithContext(WithRequest(r.Context(), ri)))
	})
}

// WithService marks work done by Baleia itself (a schedule, the worker) as
// the actor name.
func WithService(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, keyService, name)
}

// WithAuthMethod records how the caller authenticated (login and refresh
// handlers set it; any other request with a user is an access token).
func WithAuthMethod(ctx context.Context, method string) context.Context {
	return context.WithValue(ctx, keyAuthMethod, method)
}

// actor resolves who acts in ctx.
func actor(ctx context.Context) (kind, id, method string) {
	if s, ok := ctx.Value(keyService).(string); ok && s != "" {
		return "service", s, ""
	}
	method, _ = ctx.Value(keyAuthMethod).(string)
	if uid := auth.UserIDFromContext(ctx); uid != "" {
		if method == "" {
			method = AuthAccessToken
		}
		return "user", uid, method
	}
	anon, _ := ctx.Value(keyAnonymous).(string)
	return "anonymous", anon, method
}

func requestOf(ctx context.Context) (RequestInfo, string) {
	ri, _ := ctx.Value(keyRequest).(RequestInfo)
	return ri, middleware.GetReqID(ctx)
}

// sensitiveKeyParts mark a change key as secret whatever the provider
// declares.
var sensitiveKeyParts = []string{"password", "secret", "token", "credential", "private_key", "api_key"}

// redact replaces the values of secret fields with "***".
func redact(changes map[string]Change, secret map[string]bool) map[string]Change {
	if len(changes) == 0 {
		return nil
	}
	out := make(map[string]Change, len(changes))
	for k, c := range changes {
		if isSecret(k, secret) {
			if c.From != nil {
				c.From = "***"
			}
			if c.To != nil {
				c.To = "***"
			}
		}
		out[k] = c
	}
	return out
}

func isSecret(key string, secret map[string]bool) bool {
	if secret[key] {
		return true
	}
	k := strings.ToLower(key)
	for _, p := range sensitiveKeyParts {
		if strings.Contains(k, p) {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Allowed returns e with the operation done.
func (e Event) Allowed() Event {
	e.Outcome, e.Reason = Allowed, ""
	return e
}

// Denied returns e refused for reason (ReasonForbidden, ReasonNotFound).
func (e Event) Denied(reason string) Event {
	e.Outcome, e.Reason = Denied, reason
	return e
}

// Failed returns e with the operation refused or failed for reason
// (ReasonValidation, ReasonConflict, ReasonEngineError, …).
func (e Event) Failed(reason string) Event {
	e.Outcome, e.Reason = Error, reason
	return e
}

// ActorOf returns who acts in ctx, as Record would store it.
func ActorOf(ctx context.Context) (kind, id, method string) { return actor(ctx) }
