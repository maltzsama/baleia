package server

import (
	"context"
	"net/http"

	"github.com/galileostudio/baleia/internal/api"
	"github.com/galileostudio/baleia/internal/auth"
)

// csrfMiddleware requires X-Requested-With: baleia on the cookie-authenticated
// refresh and logout POSTs (#275). SameSite=Lax is a second line of defence,
// not the only one: a cross-site form cannot set a custom header.
func csrfMiddleware(f api.StrictHandlerFunc, operationID string) api.StrictHandlerFunc {
	if operationID != "RefreshToken" && operationID != "Logout" {
		return f
	}
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, args any) (any, error) {
		if r.Header.Get(auth.CSRFHeader) != auth.CSRFValue {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"forbidden","message":"missing X-Requested-With header"}`))
			return nil, nil
		}
		return f(ctx, w, r, args)
	}
}
