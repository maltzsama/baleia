package bigateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity/bikeys"
)

// rewritePage rewrites the URIs of one Trino page to the gateway, signing
// each with the shared HMAC key and the authenticated key's id.
func (g *Gateway) rewritePage(r *http.Request, cred bikeys.Credential, execID db.UUID, body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	var page map[string]json.RawMessage
	if err := json.Unmarshal(body, &page); err != nil {
		// Not a protocol page (an error body, or a non-JSON response): pass it
		// through untouched.
		return body
	}
	qid := pageString(page, "id")
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
		rewritten, ok := g.rewriteURI(r, cred, qid, execID, value)
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

// pageString reads one string field of a protocol page.
func pageString(page map[string]json.RawMessage, field string) string {
	raw, ok := page[field]
	if !ok {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

// rewriteURI turns an absolute Trino URI into the gateway's equivalent. The
// path and query stay the same so follow can proxy them; the query id, key id,
// expiry and HMAC travel as parameters.
func (g *Gateway) rewriteURI(r *http.Request, cred bikeys.Credential, qid string, execID db.UUID, raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return "", false
	}
	original := u.Query()
	expiry := g.now().Add(uriTTL)
	sig := g.sign(qid, u.Path, original, cred.Key.ID, expiry, execID)
	q := cloneValues(original)
	q.Set("baleia_exec", execID.String())
	q.Set("baleia_qid", qid)
	q.Set("baleia_key", cred.Key.ID.String())
	q.Set("baleia_exp", strconv.FormatInt(expiry.Unix(), 10))
	q.Set("baleia_sig", sig)
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

// sign returns the URL-safe HMAC of one rewritten URI. The payload covers the
// Trino query id, the original path and query, the key id and the expiry, so a
// replica that shares the secret can validate it without per-query state.
func (g *Gateway) sign(qid, path string, query url.Values, keyID db.UUID, expiry time.Time, execID db.UUID) string {
	mac := hmac.New(sha256.New, g.deps.Secret)
	_, _ = mac.Write([]byte(canonicalURI(qid, path, query, keyID, expiry, execID)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// canonicalURI is the exact string the signature covers. url.Values.Encode
// sorts the keys, so rewrite and verify agree.
func canonicalURI(qid, path string, query url.Values, keyID db.UUID, expiry time.Time, execID db.UUID) string {
	return "baleia-bi-uri-v1\n" +
		qid + "\n" +
		path + "\n" +
		query.Encode() + "\n" +
		keyID.String() + "\n" +
		strconv.FormatInt(expiry.Unix(), 10) + "\n" +
		execID.String()
}

// validQueryURI checks the key binding, expiry and HMAC of a follow URI.
func (g *Gateway) validQueryURI(q url.Values, path string, keyID db.UUID) (db.UUID, bool) {
	sig := q.Get("baleia_sig")
	if sig == "" || q.Get("baleia_key") != keyID.String() {
		return db.UUID{}, false
	}
	var execID db.UUID
	if err := execID.Scan(q.Get("baleia_exec")); err != nil || !execID.Valid {
		return db.UUID{}, false
	}
	exp, err := strconv.ParseInt(q.Get("baleia_exp"), 10, 64)
	if err != nil || !g.now().Before(time.Unix(exp, 0)) {
		return db.UUID{}, false
	}
	clean := cloneValues(q)
	for _, name := range []string{"baleia_exec", "baleia_qid", "baleia_key", "baleia_exp", "baleia_sig"} {
		clean.Del(name)
	}
	want := g.sign(q.Get("baleia_qid"), path, clean, keyID, time.Unix(exp, 0), execID)
	return execID, hmac.Equal([]byte(sig), []byte(want))
}

// cloneValues copies a query so verification never mutates the request's.
func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for key, values := range v {
		out[key] = append([]string(nil), values...)
	}
	return out
}
