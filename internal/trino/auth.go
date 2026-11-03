package trino

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
)

// WithBasicAuth returns a copy of hc that authenticates every request as
// user/password (Trino password authentication). Trino accepts passwords only
// over HTTPS; the authenticated user must be allowed to impersonate the
// X-Trino-User identities the client sends. Redirects are not followed, so the
// password never reaches another host or a plain-HTTP URL: a 3xx answer
// returns to the caller.
func WithBasicAuth(hc *http.Client, user, password string) *http.Client {
	out := *hc
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
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

// ErrInvalidCA means a CA bundle holds no PEM certificate.
var ErrInvalidCA = errors.New("trino: the CA bundle holds no PEM certificate")

// WithRootCAs returns a copy of hc that trusts the PEM certificates in caPEM
// besides the system roots, for a coordinator whose certificate a private CA
// signed. hc's transport must be an *http.Transport (the dialer's is).
func WithRootCAs(hc *http.Client, caPEM string) (*http.Client, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		return nil, ErrInvalidCA
	}
	base, ok := hc.Transport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("trino: cannot set CAs on a %T transport", hc.Transport)
	}
	t := base.Clone()
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	t.TLSClientConfig.RootCAs = pool
	// A custom TLS config turns off HTTP/2 and ALPN in net/http; Trino's
	// HTTPS port then answers a client that offers no protocol with HTTP/2,
	// which an HTTP/1.1 client cannot read. Negotiate it.
	t.ForceAttemptHTTP2 = true
	out := *hc
	out.Transport = t
	return &out, nil
}

// Authenticated applies a coordinator's CA bundle and password login to hc;
// empty values are skipped. The CA goes first: it needs the plain transport.
func Authenticated(hc *http.Client, user, password, caPEM string) (*http.Client, error) {
	if caPEM != "" {
		var err error
		if hc, err = WithRootCAs(hc, caPEM); err != nil {
			return nil, err
		}
	}
	if password != "" {
		hc = WithBasicAuth(hc, user, password)
	}
	return hc, nil
}
