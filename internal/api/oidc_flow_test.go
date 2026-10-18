package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/oidctest"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSafeRelativePath(t *testing.T) {
	cases := map[string]bool{
		"/sql":              true,
		"/sql?tab=1":        true,
		"":                  false,
		"//evil.example":    false,
		"/\\evil.example":   false,
		"https://evil.test": false,
		"/a\nb":             false,
		"/a\tb":             false,
	}
	for next, want := range cases {
		if got := safeRelativePath(next); got != want {
			t.Errorf("safeRelativePath(%q) = %v, want %v", next, got, want)
		}
	}
}

func oidcAttemptFixture(t *testing.T) (*db.Queries, db.AuthProvider) {
	t.Helper()
	pool := testdb.New(t)
	q := db.New(pool)
	provider, err := q.CreateAuthProvider(context.Background(), db.CreateAuthProviderParams{
		Name:      "test",
		Type:      "oidc",
		ConfigEnc: "{}",
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return q, provider
}

// TestOIDC_AttemptSingleUse: consuming a state deletes the row, so a replayed
// callback finds nothing (#250).
func TestOIDC_AttemptSingleUse(t *testing.T) {
	q, provider := oidcAttemptFixture(t)
	ctx := context.Background()
	const state = "state-1"
	if err := q.CreateOIDCLoginAttempt(ctx, db.CreateOIDCLoginAttemptParams{
		State:        state,
		ProviderID:   provider.ID,
		CodeVerifier: "verifier-1",
		ExpiresAt:    pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}

	first, err := q.ConsumeOIDCLoginAttempt(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	if first.CodeVerifier != "verifier-1" {
		t.Errorf("verifier = %q, want verifier-1", first.CodeVerifier)
	}
	if _, err := q.ConsumeOIDCLoginAttempt(ctx, state); err == nil {
		t.Fatal("a replayed state was consumed twice")
	}
}

// TestOIDC_RedirectConstraint: the database refuses unsafe redirects even if a
// future writer forgets to validate them (#252).
func TestOIDC_RedirectConstraint(t *testing.T) {
	q, provider := oidcAttemptFixture(t)
	ctx := context.Background()
	expires := pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true}
	create := func(state string, redirect *string) error {
		return q.CreateOIDCLoginAttempt(ctx, db.CreateOIDCLoginAttemptParams{
			State:        state,
			ProviderID:   provider.ID,
			CodeVerifier: "verifier",
			Redirect:     redirect,
			ExpiresAt:    expires,
		})
	}

	for i, bad := range []string{"//evil.example", "/\\evil.example", "https://evil.example", "/a\nb"} {
		bad := bad
		if err := create(fmt.Sprintf("bad-%d", i), &bad); err == nil {
			t.Errorf("redirect %q was stored", bad)
		}
	}
	good := "/sql?tab=1"
	if err := create("good", &good); err != nil {
		t.Fatalf("relative redirect rejected: %v", err)
	}
}

func oidcTestKeyring(t *testing.T) *crypto.Keyring {
	t.Helper()
	kr, err := crypto.NewKeyring(testSecret, "")
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

func oidcProvider(t *testing.T, q *db.Queries, issuer string) db.AuthProvider {
	t.Helper()
	ctx := context.Background()
	provider, err := q.CreateAuthProvider(ctx, db.CreateAuthProviderParams{
		Name: "stub", Type: "oidc", ConfigEnc: "", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := auth.SealOIDCConfig(oidcTestKeyring(t), idString(provider.ID), auth.OIDCProviderConfig{
		IssuerURL: issuer, ClientID: "client", ClientSecret: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	provider, err = q.UpdateAuthProvider(ctx, db.UpdateAuthProviderParams{
		ID: provider.ID, Name: provider.Name, ConfigEnc: sealed, Enabled: provider.Enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func oidcHandlers(t *testing.T, q *db.Queries) *Handlers {
	t.Helper()
	return &Handlers{
		queries: q,
		keyring: oidcTestKeyring(t),
		cfg:     &config.Config{PublicURL: "http://localhost", JWTSecret: "test-secret"},
	}
}

type oidcFlowFixture struct {
	pool     *pgxpool.Pool
	q        *db.Queries
	h        *Handlers
	idp      *oidctest.StubIDP
	provider db.AuthProvider
}

func newOIDCFlow(t *testing.T) *oidcFlowFixture {
	t.Helper()
	pool := testdb.New(t)
	q := db.New(pool)
	idp, err := oidctest.New("", "client")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(idp.Close)
	return &oidcFlowFixture{pool: pool, q: q, h: oidcHandlers(t, q), idp: idp, provider: oidcProvider(t, q, idp.URL)}
}

func (f *oidcFlowFixture) start(t *testing.T) (state string, cookie *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/x/login?next=/sql", nil)
	req.SetPathValue("provider_id", idString(f.provider.ID))
	rec := httptest.NewRecorder()
	f.h.HandleOIDCRedirect(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("redirect = %d, want 302", rec.Code)
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state = u.Query().Get("state")
	nonce := u.Query().Get("nonce")
	if state == "" || nonce == "" {
		t.Fatalf("state=%q nonce=%q", state, nonce)
	}
	f.idp.Nonce = nonce
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.OIDCCookieName {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value != state {
		t.Fatalf("baleia_oidc cookie = %+v, want the state", cookie)
	}
	return state, cookie
}

func (f *oidcFlowFixture) finish(t *testing.T, state string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/x/callback?code=code-1&state="+url.QueryEscape(state), nil)
	req.SetPathValue("provider_id", idString(f.provider.ID))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	f.h.HandleOIDCCallback(rec, req)
	return rec
}

func (f *oidcFlowFixture) failure(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	const prefix = "/login?error="
	if !strings.HasPrefix(loc, prefix) {
		t.Fatalf("Location = %q, want /login?error=...", loc)
	}
	return strings.TrimPrefix(loc, prefix)
}

// TestOIDC_NoTokensInRedirect: the callback hands the session off in the
// HttpOnly cookie and redirects to the stored relative path (#252).
func TestOIDC_NoTokensInRedirect(t *testing.T) {
	f := newOIDCFlow(t)
	state, cookie := f.start(t)
	rec := f.finish(t, state, cookie)
	if rec.Code != http.StatusFound {
		t.Fatalf("callback = %d, want 302", rec.Code)
	}
	target := rec.Header().Get("Location")
	if target != "/sql" {
		t.Errorf("Location = %q, want /sql", target)
	}
	if u, err := url.Parse(target); err != nil {
		t.Fatal(err)
	} else if u.Query().Get("token") != "" || u.Query().Get("refresh_token") != "" {
		t.Errorf("tokens leaked in the redirect: %q", target)
	}
	var refreshCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.RefreshCookieName {
			refreshCookie = c
		}
	}
	if refreshCookie == nil || refreshCookie.Value == "" {
		t.Fatal("the baleia_refresh cookie was not set")
	}
	if !refreshCookie.HttpOnly || !refreshCookie.Secure || refreshCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("refresh cookie flags = %+v", refreshCookie)
	}
}

func TestOIDC_StateMismatch(t *testing.T) {
	f := newOIDCFlow(t)
	_, cookie := f.start(t)
	if code := f.failure(t, f.finish(t, "different-state", cookie)); code != oidcErrorSSOFailed {
		t.Errorf("error = %q, want %q", code, oidcErrorSSOFailed)
	}
}

func TestOIDC_StateReplay(t *testing.T) {
	f := newOIDCFlow(t)
	state, cookie := f.start(t)
	if rec := f.finish(t, state, cookie); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/sql" {
		t.Fatalf("first callback = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if code := f.failure(t, f.finish(t, state, cookie)); code != oidcErrorSSOFailed {
		t.Errorf("replay error = %q, want %q", code, oidcErrorSSOFailed)
	}
}

func TestOIDC_Expired(t *testing.T) {
	f := newOIDCFlow(t)
	state, cookie := f.start(t)
	if _, err := f.pool.Exec(context.Background(), "UPDATE oidc_login_attempts SET expires_at = now() - interval '1 minute' WHERE state = $1", state); err != nil {
		t.Fatal(err)
	}
	if code := f.failure(t, f.finish(t, state, cookie)); code != oidcErrorSSOFailed {
		t.Errorf("expired error = %q, want %q", code, oidcErrorSSOFailed)
	}
}

func TestOIDC_NonceMismatch(t *testing.T) {
	f := newOIDCFlow(t)
	state, cookie := f.start(t)
	f.idp.Nonce = "wrong-nonce"
	if code := f.failure(t, f.finish(t, state, cookie)); code != oidcErrorSSOFailed {
		t.Errorf("nonce error = %q, want %q", code, oidcErrorSSOFailed)
	}
}

// TestOIDC_RedirectRelativeOnly: only a stored same-origin path is honoured.
func TestOIDC_RedirectRelativeOnly(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		in   *string
		want string
	}{
		{nil, "/"},
		{str("/sql"), "/sql"},
		{str("//evil.example"), "/"},
		{str("/\\evil.example"), "/"},
		{str("https://evil.example"), "/"},
	}
	for _, tc := range cases {
		if got := oidcRedirectTarget(tc.in); got != tc.want {
			t.Errorf("redirect target = %q, want %q", got, tc.want)
		}
	}
}

// TestOIDC_FailureRedirects: every failure lands on /login?error=<code>.
func TestOIDC_FailureRedirects(t *testing.T) {
	for _, code := range []string{oidcErrorNotProvisioned, oidcErrorAccountSuspended, oidcErrorIDPUnavailable, oidcErrorSSOFailed} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/callback", nil)
		oidcFailure(rec, req, code)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login?error="+code {
			t.Errorf("%s: status %d location %q", code, rec.Code, rec.Header().Get("Location"))
		}
	}
	// An unknown code cannot escape the bounded vocabulary.
	rec := httptest.NewRecorder()
	oidcFailure(rec, httptest.NewRequest(http.MethodGet, "/callback", nil), "bogus")
	if got := rec.Header().Get("Location"); got != "/login?error="+oidcErrorSSOFailed {
		t.Errorf("unknown code location = %q", got)
	}

	pool := testdb.New(t)
	q := db.New(pool)
	h := oidcHandlers(t, q)

	// An unreachable issuer is an IdP outage.
	broken := oidcProvider(t, q, "http://127.0.0.1:1")
	req := httptest.NewRequest(http.MethodGet, "/callback", nil)
	req.SetPathValue("provider_id", idString(broken.ID))
	rec = httptest.NewRecorder()
	h.HandleOIDCCallback(rec, req)
	if got := rec.Header().Get("Location"); got != "/login?error="+oidcErrorIDPUnavailable {
		t.Errorf("unreachable issuer location = %q", got)
	}

	// A valid provider with a bad state is an SSO failure.
	idp, err := oidctest.New("", "client")
	if err != nil {
		t.Fatal(err)
	}
	defer idp.Close()
	provider := oidcProvider(t, q, idp.URL)
	req = httptest.NewRequest(http.MethodGet, "/callback?state=bad", nil)
	req.SetPathValue("provider_id", idString(provider.ID))
	rec = httptest.NewRecorder()
	h.HandleOIDCCallback(rec, req)
	if got := rec.Header().Get("Location"); got != "/login?error="+oidcErrorSSOFailed {
		t.Errorf("bad state location = %q", got)
	}
}

// TestOIDC_ConfigEncrypted: the provider config is sealed with the keyring and
// bound to the provider id (#278).
func TestOIDC_ConfigEncrypted(t *testing.T) {
	q := db.New(testdb.New(t))
	provider := oidcProvider(t, q, "http://issuer.example")
	stored, err := q.GetAuthProviderByID(context.Background(), provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored.ConfigEnc, "v1:") {
		t.Errorf("config_enc = %q, want a v1 envelope", stored.ConfigEnc)
	}
	if strings.Contains(stored.ConfigEnc, "secret") || strings.Contains(stored.ConfigEnc, "issuer_url") {
		t.Error("the provider config is still plaintext")
	}
}

// TestOIDC_ConfigDecryptFailsClosed: a plaintext config is refused, never
// parsed as JSON (#278).
func TestOIDC_ConfigDecryptFailsClosed(t *testing.T) {
	q := db.New(testdb.New(t))
	plaintext := `{"issuer_url":"http://issuer.example","client_id":"client","client_secret":"secret"}`
	provider, err := q.CreateAuthProvider(context.Background(), db.CreateAuthProviderParams{
		Name: "plain", Type: "oidc", ConfigEnc: plaintext, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := oidcHandlers(t, q)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/x/login", nil)
	if _, err := h.loadOIDCProvider(req, idString(provider.ID)); err == nil {
		t.Fatal("loadOIDCProvider accepted a plaintext config")
	}
}

// TestOIDC_AttemptExpired: an attempt older than its TTL is refused (#250).
func TestOIDC_AttemptExpired(t *testing.T) {
	q, provider := oidcAttemptFixture(t)
	ctx := context.Background()
	if err := q.CreateOIDCLoginAttempt(ctx, db.CreateOIDCLoginAttemptParams{
		State:        "state-expired",
		ProviderID:   provider.ID,
		CodeVerifier: "verifier-1",
		ExpiresAt:    pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ConsumeOIDCLoginAttempt(ctx, "state-expired"); err == nil {
		t.Fatal("an expired attempt was consumed")
	}
}
