package api

import "context"

// OIDCResult carries the redirect and cookies a strict OIDC handler wants the
// response middleware to write. The strict handler does not receive the
// ResponseWriter (#280).
type OIDCResult struct {
	Location string
	// OIDCCookie: nil = leave it, "" = clear it, other = set it.
	OIDCCookie *string
	// RefreshCookie is set on a successful callback.
	RefreshCookie *string
}

type oidcResultKey struct{}

// WithOIDCResult injects the per-request OIDC result.
func WithOIDCResult(ctx context.Context, r *OIDCResult) context.Context {
	return context.WithValue(ctx, oidcResultKey{}, r)
}

// OIDCResultFromContext returns the per-request OIDC result, or nil.
func OIDCResultFromContext(ctx context.Context) *OIDCResult {
	r, _ := ctx.Value(oidcResultKey{}).(*OIDCResult)
	return r
}
