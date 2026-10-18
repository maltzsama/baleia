package server

import (
	"context"
	"net/http"

	baleiaapi "github.com/galileostudio/baleia/internal/api"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
)

// refreshCookieContext exposes the baleia_refresh cookie to the generated
// handlers, which do not receive the *http.Request.
func refreshCookieContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(auth.RefreshCookieName); err == nil && c.Value != "" {
			r = r.WithContext(auth.WithRefreshCookie(r.Context(), c.Value))
		}
		next.ServeHTTP(w, r)
	})
}

// setRefreshCookie writes the browser session cookie (#51). Secure is always
// set: browsers treat http://localhost as a trustworthy origin, so local
// development still works.
func setRefreshCookie(w http.ResponseWriter, cfg *config.Config, token string) {
	if token == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.RefreshCookieName,
		Value:    token,
		Path:     auth.RefreshCookiePath,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(cfg.RefreshTTL().Seconds()),
	})
}

func clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.RefreshCookieName,
		Value:    "",
		Path:     auth.RefreshCookiePath,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// refreshCookieMiddleware sets or clears the refresh cookie around token
// operations. It also hides the JSON refresh token from browsers; API clients
// opt in with X-Baleia-Client: api.
func refreshCookieMiddleware(cfg *config.Config) baleiaapi.StrictMiddlewareFunc {
	return func(f baleiaapi.StrictHandlerFunc, operationID string) baleiaapi.StrictHandlerFunc {
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, args interface{}) (interface{}, error) {
			resp, err := f(ctx, w, r, args)
			if err != nil {
				return resp, err
			}
			apiClient := r.Header.Get(auth.ClientHeader) == auth.ClientAPI
			switch operationID {
			case "Login":
				if v, ok := resp.(baleiaapi.Login200JSONResponse); ok {
					if v.RefreshToken != nil {
						setRefreshCookie(w, cfg, *v.RefreshToken)
						if !apiClient {
							v.RefreshToken = nil
						}
					}
					return v, nil
				}
			case "CreateFirstAdmin":
				if v, ok := resp.(baleiaapi.CreateFirstAdmin201JSONResponse); ok {
					if v.RefreshToken != nil {
						setRefreshCookie(w, cfg, *v.RefreshToken)
						if !apiClient {
							v.RefreshToken = nil
						}
					}
					return v, nil
				}
			case "RefreshToken":
				if v, ok := resp.(baleiaapi.RefreshToken200JSONResponse); ok {
					if v.RefreshToken != nil {
						setRefreshCookie(w, cfg, *v.RefreshToken)
						if !apiClient {
							v.RefreshToken = nil
						}
					}
					return v, nil
				}
				// A failed refresh leaves the cookie alone: with concurrent
				// tabs the winner sets a fresh cookie and a loser's 401 must
				// not clear it.
			case "Logout":
				clearRefreshCookie(w)
			}
			return resp, nil
		}
	}
}
