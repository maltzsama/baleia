package auth

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const (
	ContextKeyUserID        contextKey = "user_id"
	ContextKeyRole          contextKey = "role"
	ContextKeyRefreshCookie contextKey = "refresh_cookie"
)

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

// WithRefreshCookie makes the refresh cookie value available to a handler.
func WithRefreshCookie(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, ContextKeyRefreshCookie, token)
}

// RefreshCookieFromContext returns the refresh cookie value, or "".
func RefreshCookieFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextKeyRefreshCookie).(string)
	return v
}

func Middleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractToken(r)
			if token == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			claims, err := ParseToken(token, secret)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), ContextKeyUserID, claims.UserID)
			ctx = context.WithValue(ctx, ContextKeyRole, claims.Role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, _ := r.Context().Value(ContextKeyRole).(string)
		if role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextKeyUserID).(string)
	return v
}

func RoleFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextKeyRole).(string)
	return v
}

func extractToken(r *http.Request) string {
	bearer := r.Header.Get("Authorization")
	if strings.HasPrefix(bearer, "Bearer ") {
		return strings.TrimPrefix(bearer, "Bearer ")
	}
	// fallback: cookie (useful for SSE/WebSocket)
	if c, err := r.Cookie("token"); err == nil {
		return c.Value
	}
	return ""
}
