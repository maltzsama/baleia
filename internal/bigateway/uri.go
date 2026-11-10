package bigateway

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/galileostudio/baleia/internal/identity/bikeys"
)

// rewritePage rewrites the URIs of one Trino page to the gateway, binding
// each to the authenticated key and an expiry. #710 adds the HMAC signature.
func (g *Gateway) rewritePage(r *http.Request, cred bikeys.Credential, body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	var page map[string]json.RawMessage
	if err := json.Unmarshal(body, &page); err != nil {
		// Not a protocol page (an error body, or a non-JSON response): pass it
		// through untouched.
		return body
	}
	changed := false
	for _, field := range []string{"nextUri", "infoUri", "partialCancelUri"} {
		raw, ok := page[field]
		if !ok {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil || value == "" {
			continue
		}
		rewritten, ok := g.rewriteURI(r, cred, value)
		if !ok {
			continue
		}
		page[field], _ = json.Marshal(rewritten)
		changed = true
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(page)
	if err != nil {
		return body
	}
	return out
}

// rewriteURI turns an absolute Trino URI into the gateway's equivalent. The
// path stays the same so follow can proxy it; the key id and expiry are
// query parameters (and #710's signature will cover them).
func (g *Gateway) rewriteURI(r *http.Request, cred bikeys.Credential, raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return "", false
	}
	q := u.Query()
	q.Set("baleia_key", cred.Key.ID.String())
	q.Set("baleia_exp", strconv.FormatInt(g.now().Add(uriTTL).Unix(), 10))
	return g.publicBase(r) + u.Path + "?" + q.Encode(), true
}

// publicBase is the address the BI tool uses back. A configured URL wins;
// otherwise the request's own scheme and Host are used.
func (g *Gateway) publicBase(r *http.Request) string {
	if g.deps.PublicURL != "" {
		return strings.TrimRight(g.deps.PublicURL, "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host
}
