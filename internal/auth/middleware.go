package auth

import (
	"context"
)

type contextKey string

const (
	ContextKeyUserID        contextKey = "user_id"
	ContextKeyRole          contextKey = "role"
	ContextKeyActorKind     contextKey = "actor_kind"
	ContextKeyAgentID       contextKey = "agent_id"
	ContextKeyRefreshCookie contextKey = "refresh_cookie"
	ContextKeyOIDCCookie    contextKey = "oidc_cookie"
)

// ActorAgent marks a request whose caller is an agent acting for its owner
// (#59). The zero value means a human user.
const ActorAgent = "agent"

// Browser session cookie for the refresh token (#51). It is scoped to the auth
// routes so it is never sent with ordinary API requests.
const (
	RefreshCookieName = "baleia_refresh"
	RefreshCookiePath = "/api/v1/auth"
)

// ClientHeader lets non-browser clients opt into the JSON refresh token.
const (
	ClientHeader = "X-Baleia-Client"
	ClientAPI    = "api"
)

// CSRFHeader is required on the cookie-authenticated refresh/logout POSTs
// (#275). A cross-site form cannot set a custom header.
const (
	CSRFHeader = "X-Requested-With"
	CSRFValue  = "baleia"
)

// WithRefreshCookie makes the refresh cookie value available to a handler.
func WithRefreshCookie(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, ContextKeyRefreshCookie, token)
}

// RefreshCookieFromContext returns the refresh cookie value, or "".
func RefreshCookieFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextKeyRefreshCookie).(string)
	return v
}

// WithOIDCCookie makes the OIDC browser binding available to a handler.
func WithOIDCCookie(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, ContextKeyOIDCCookie, token)
}

// OIDCCookieFromContext returns the OIDC cookie value, or "".
func OIDCCookieFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextKeyOIDCCookie).(string)
	return v
}

func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextKeyUserID).(string)
	return v
}

func RoleFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextKeyRole).(string)
	return v
}

// ActorKindFromContext returns ActorAgent for an agent request, or "" for a
// human user.
func ActorKindFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextKeyActorKind).(string)
	return v
}

// AgentIDFromContext returns the agent id for an agent request, or "".
func AgentIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextKeyAgentID).(string)
	return v
}

// WithAgentPrincipal puts an agent principal in ctx. UserID is the owner and
// the role is always member, even when the owner is a global admin (#59).
func WithAgentPrincipal(ctx context.Context, ownerID, agentID string) context.Context {
	ctx = context.WithValue(ctx, ContextKeyUserID, ownerID)
	ctx = context.WithValue(ctx, ContextKeyRole, "member")
	ctx = context.WithValue(ctx, ContextKeyActorKind, ActorAgent)
	ctx = context.WithValue(ctx, ContextKeyAgentID, agentID)
	return ctx
}
