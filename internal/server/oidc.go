package server

import (
	"context"
	"net/http"

	"github.com/galileostudio/baleia/internal/api"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
)

// oidcMiddleware injects the strict OIDC result and, after the handler, writes
// its redirect and cookies (#280). The strict handler does not receive the
// ResponseWriter.
func oidcMiddleware(cfg *config.Config) api.StrictMiddlewareFunc {
	return func(f api.StrictHandlerFunc, operationID string) api.StrictHandlerFunc {
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, args any) (any, error) {
			res := &api.OIDCResult{}
			ctx = api.WithOIDCResult(ctx, res)
			resp, err := f(ctx, w, r, args)
			if err != nil {
				return resp, err
			}
			if res.Location != "" {
				w.Header().Set("Location", res.Location)
			}
			if res.OIDCCookie != nil {
				if *res.OIDCCookie == "" {
					auth.ClearOIDCCookie(w)
				} else {
					auth.SetOIDCCookie(w, *res.OIDCCookie, auth.OIDCAttemptTTL)
				}
			}
			if res.RefreshCookie != nil {
				auth.SetRefreshCookie(w, *res.RefreshCookie, cfg.RefreshTTL())
			}
			return resp, nil
		}
	}
}
