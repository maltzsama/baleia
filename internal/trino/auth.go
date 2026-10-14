package trino

import "net/http"

// WithBasicAuth returns a copy of hc that authenticates every request as
// user/password (Trino password authentication). Trino accepts passwords only
// over HTTPS; the authenticated user must be allowed to impersonate the
// X-Trino-User identities the client sends.
func WithBasicAuth(hc *http.Client, user, password string) *http.Client {
	out := *hc
	base := hc.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	out.Transport = basicAuth{base: base, user: user, password: password}
	return &out
}

type basicAuth struct {
	base           http.RoundTripper
	user, password string
}

func (b basicAuth) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.SetBasicAuth(b.user, b.password)
	return b.base.RoundTrip(r)
}
