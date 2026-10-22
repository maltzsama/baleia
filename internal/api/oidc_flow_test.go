package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity"
	"github.com/galileostudio/baleia/internal/oidctest"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
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

func oidcConfig(issuer string) auth.OIDCProviderConfig {
	return auth.OIDCProviderConfig{
		IssuerURL:           issuer,
		ClientID:            "client",
		ClientSecret:        "secret",
		AutoCreate:          true,
		AllowedEmailDomains: []string{"example.test"},
	}
}

func oidcProviderWithConfig(t *testing.T, q *db.Queries, cfg auth.OIDCProviderConfig) db.AuthProvider {
	t.Helper()
	ctx := context.Background()
	provider, err := q.CreateAuthProvider(ctx, db.CreateAuthProviderParams{
		Name: "stub", Type: "oidc", ConfigEnc: "", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := auth.SealOIDCConfig(oidcTestKeyring(t), idString(provider.ID), cfg)
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

func oidcProvider(t *testing.T, q *db.Queries, issuer string) db.AuthProvider {
	t.Helper()
	return oidcProviderWithConfig(t, q, oidcConfig(issuer))
}

func oidcHandlers(t *testing.T, q *db.Queries) *Handlers {
	t.Helper()
	return testHandlers(handlerFields{
		queries: q, tx: directTx{q: q},
		keyring:  oidcTestKeyring(t),
		settings: SettingsFromConfig(&config.Config{PublicURL: "http://localhost", JWTSecret: "test-secret"}),
	})
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
	return newOIDCFlowWithConfig(t, func(issuer string) auth.OIDCProviderConfig { return oidcConfig(issuer) })
}

func newOIDCFlowWithConfig(t *testing.T, config func(issuer string) auth.OIDCProviderConfig) *oidcFlowFixture {
	t.Helper()
	pool := testdb.New(t)
	q := db.New(pool)
	idp, err := oidctest.New("", "client")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(idp.Close)
	return &oidcFlowFixture{
		pool: pool, q: q, h: oidcHandlers(t, q), idp: idp,
		provider: oidcProviderWithConfig(t, q, config(idp.URL)),
	}
}

func oidcPtr[T any](v T) *T { return &v }

func (f *oidcFlowFixture) start(t *testing.T) (state string, cookie *http.Cookie) {
	t.Helper()
	result := &OIDCResult{}
	ctx := WithOIDCResult(context.Background(), result)
	resp, err := f.h.OidcRedirect(ctx, OidcRedirectRequestObject{
		ProviderId: openapi_types.UUID(f.provider.ID.Bytes),
		Params:     OidcRedirectParams{Next: oidcPtr("/sql")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(OidcRedirect302Response); !ok {
		t.Fatalf("redirect = %T, want 302", resp)
	}
	if result.Location == "" || result.OIDCCookie == nil {
		t.Fatalf("redirect result = %+v", result)
	}
	u, err := url.Parse(result.Location)
	if err != nil {
		t.Fatal(err)
	}
	state = u.Query().Get("state")
	nonce := u.Query().Get("nonce")
	if state == "" || nonce == "" {
		t.Fatalf("state=%q nonce=%q", state, nonce)
	}
	f.idp.Nonce = nonce
	cookie = &http.Cookie{Name: auth.OIDCCookieName, Value: *result.OIDCCookie}
	if cookie.Value != state {
		t.Fatalf("baleia_oidc cookie = %q, want the state %q", cookie.Value, state)
	}
	return state, cookie
}

func (f *oidcFlowFixture) finish(t *testing.T, state string, cookie *http.Cookie) *OIDCResult {
	t.Helper()
	result := &OIDCResult{}
	ctx := WithOIDCResult(context.Background(), result)
	ctx = auth.WithOIDCCookie(ctx, cookie.Value)
	resp, err := f.h.OidcCallback(ctx, OidcCallbackRequestObject{
		ProviderId: openapi_types.UUID(f.provider.ID.Bytes),
		Params:     OidcCallbackParams{Code: "code-1", State: state},
	})
	if err != nil {
		t.Fatal(err)
	}
	switch resp.(type) {
	case OidcCallback302Response, OidcCallback503JSONResponse:
	default:
		t.Fatalf("callback = %T", resp)
	}
	return result
}

func (f *oidcFlowFixture) failure(t *testing.T, result *OIDCResult) string {
	t.Helper()
	const prefix = "/login?error="
	if !strings.HasPrefix(result.Location, prefix) {
		t.Fatalf("Location = %q, want /login?error=...", result.Location)
	}
	return strings.TrimPrefix(result.Location, prefix)
}

// TestOIDC_NoTokensInRedirect: the callback hands the session off in the
// HttpOnly cookie and redirects to the stored relative path (#252).
func TestOIDC_NoTokensInRedirect(t *testing.T) {
	f := newOIDCFlow(t)
	state, cookie := f.start(t)
	result := f.finish(t, state, cookie)
	if result.Location != "/sql" {
		t.Errorf("Location = %q, want /sql", result.Location)
	}
	if u, err := url.Parse(result.Location); err != nil {
		t.Fatal(err)
	} else if u.Query().Get("token") != "" || u.Query().Get("refresh_token") != "" {
		t.Errorf("tokens leaked in the redirect: %q", result.Location)
	}
	if result.RefreshCookie == nil || *result.RefreshCookie == "" {
		t.Fatal("the baleia_refresh cookie was not set")
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
	if result := f.finish(t, state, cookie); result.Location != "/sql" {
		t.Fatalf("first callback = %q", result.Location)
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
		if got := oidcFailureLocation(code); got != "/login?error="+code {
			t.Errorf("%s: location %q", code, got)
		}
	}
	// An unknown code cannot escape the bounded vocabulary.
	if got := oidcFailureLocation("bogus"); got != "/login?error="+oidcErrorSSOFailed {
		t.Errorf("unknown code location = %q", got)
	}

	pool := testdb.New(t)
	q := db.New(pool)
	h := oidcHandlers(t, q)

	// An unreachable issuer is a 503 IdP outage.
	broken := oidcProvider(t, q, "http://127.0.0.1:1")
	result := &OIDCResult{}
	ctx := WithOIDCResult(context.Background(), result)
	resp, err := h.OidcCallback(ctx, OidcCallbackRequestObject{
		ProviderId: openapi_types.UUID(broken.ID.Bytes),
		Params:     OidcCallbackParams{Code: "code-1", State: "state"},
	})
	if err != nil {
		t.Fatal(err)
	}
	unavailable, ok := resp.(OidcCallback503JSONResponse)
	if !ok || unavailable.Code != oidcErrorIDPUnavailable {
		t.Errorf("unreachable issuer = %T %+v", resp, unavailable)
	}

	// A valid provider with a bad state is an SSO failure.
	idp, err := oidctest.New("", "client")
	if err != nil {
		t.Fatal(err)
	}
	defer idp.Close()
	provider := oidcProvider(t, q, idp.URL)
	result = &OIDCResult{}
	ctx = WithOIDCResult(context.Background(), result)
	resp, err = h.OidcCallback(ctx, OidcCallbackRequestObject{
		ProviderId: openapi_types.UUID(provider.ID.Bytes),
		Params:     OidcCallbackParams{Code: "code-1", State: "bad"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(OidcCallback302Response); !ok {
		t.Fatalf("bad state = %T", resp)
	}
	if result.Location != "/login?error="+oidcErrorSSOFailed {
		t.Errorf("bad state location = %q", result.Location)
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
	if _, _, err := h.loadOIDCProvider(context.Background(), idString(provider.ID)); err == nil {
		t.Fatal("loadOIDCProvider accepted a plaintext config")
	}
}

// TestOIDC_ProviderCache: discovery is cached and a config change invalidates
// it (#280).
func TestOIDC_ProviderCache(t *testing.T) {
	pool := testdb.New(t)
	q := db.New(pool)
	idp, err := oidctest.New("", "client")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(idp.Close)
	provider := oidcProvider(t, q, idp.URL)
	h := oidcHandlers(t, q)
	ctx := context.Background()

	if _, _, err := h.cachedOIDCProvider(ctx, idString(provider.ID)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.cachedOIDCProvider(ctx, idString(provider.ID)); err != nil {
		t.Fatal(err)
	}
	if got := idp.DiscoveryCount(); got != 1 {
		t.Errorf("discovery hits = %d, want 1", got)
	}

	// A config update bumps updated_at, so the cache misses.
	if _, err := q.UpdateAuthProvider(ctx, db.UpdateAuthProviderParams{
		ID: provider.ID, Name: provider.Name, ConfigEnc: provider.ConfigEnc, Enabled: provider.Enabled,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.cachedOIDCProvider(ctx, idString(provider.ID)); err != nil {
		t.Fatal(err)
	}
	if got := idp.DiscoveryCount(); got != 2 {
		t.Errorf("discovery hits after update = %d, want 2", got)
	}
}

// TestOIDC_StrictParity: the redirect/callback are the strict handlers (#280).
func TestOIDC_StrictParity(t *testing.T) {
	f := newOIDCFlow(t)
	result := &OIDCResult{}
	ctx := WithOIDCResult(context.Background(), result)
	resp, err := f.h.OidcRedirect(ctx, OidcRedirectRequestObject{
		ProviderId: openapi_types.UUID(f.provider.ID.Bytes),
		Params:     OidcRedirectParams{Next: oidcPtr("/sql")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(OidcRedirect302Response); !ok {
		t.Fatalf("redirect = %T, want 302", resp)
	}
	if result.Location == "" || result.OIDCCookie == nil {
		t.Fatalf("strict result = %+v", result)
	}
}

// TestOIDC_UnknownUserInviteOnly: unknown subjects are denied by default
// (#251).
func TestOIDC_UnknownUserInviteOnly(t *testing.T) {
	f := newOIDCFlowWithConfig(t, func(issuer string) auth.OIDCProviderConfig {
		cfg := oidcConfig(issuer)
		cfg.AutoCreate = false
		return cfg
	})
	state, cookie := f.start(t)
	if code := f.failure(t, f.finish(t, state, cookie)); code != oidcErrorNotProvisioned {
		t.Errorf("error = %q, want %q", code, oidcErrorNotProvisioned)
	}
}

// TestOIDC_AutoCreateDomainAllowList: auto-create needs a verified e-mail in an
// allowed domain (#251).
func TestOIDC_AutoCreateDomainAllowList(t *testing.T) {
	// Allowed + verified creates the account.
	f := newOIDCFlow(t)
	state, cookie := f.start(t)
	if result := f.finish(t, state, cookie); result.Location != "/sql" {
		t.Fatalf("allowed: %q", result.Location)
	}

	// A different domain is denied.
	f = newOIDCFlow(t)
	f.idp.Email = "user@other.test"
	state, cookie = f.start(t)
	if code := f.failure(t, f.finish(t, state, cookie)); code != oidcErrorNotProvisioned {
		t.Errorf("other domain: %q, want %q", code, oidcErrorNotProvisioned)
	}

	// An unverified e-mail is denied.
	f = newOIDCFlow(t)
	f.idp.EmailVerified = false
	state, cookie = f.start(t)
	if code := f.failure(t, f.finish(t, state, cookie)); code != oidcErrorNotProvisioned {
		t.Errorf("unverified: %q, want %q", code, oidcErrorNotProvisioned)
	}
}

// TestOIDC_NoEmailLinking: an existing local account with the same e-mail is
// never linked to the OIDC subject (#251).
func TestOIDC_NoEmailLinking(t *testing.T) {
	f := newOIDCFlow(t)
	password := "not-used"
	local, err := f.q.CreateUser(context.Background(), db.CreateUserParams{
		Name: "Local", Email: f.idp.Email, PasswordHash: &password, Role: "member", AuthProvider: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, cookie := f.start(t)
	if code := f.failure(t, f.finish(t, state, cookie)); code != oidcErrorNotProvisioned {
		t.Errorf("error = %q, want %q", code, oidcErrorNotProvisioned)
	}
	got, err := f.q.GetUserByID(context.Background(), local.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthProvider != "local" {
		t.Errorf("the local account was linked: auth_provider=%q", got.AuthProvider)
	}
}

// TestOIDC_RoleMappingEachLogin: roles follow the IdP on every login (#251).
func TestOIDC_RoleMappingEachLogin(t *testing.T) {
	f := newOIDCFlowWithConfig(t, func(issuer string) auth.OIDCProviderConfig {
		cfg := oidcConfig(issuer)
		cfg.RoleMapping = []identity.RoleMapping{{Group: "baleia-admins", Role: identity.RoleAdmin}}
		cfg.DefaultRole = identity.RoleMember
		return cfg
	})
	lookup := func() db.User {
		t.Helper()
		user, err := f.q.GetUserByExternalID(context.Background(), db.GetUserByExternalIDParams{
			AuthProviderID: f.provider.ID,
			ExternalID:     &f.idp.Subject,
		})
		if err != nil {
			t.Fatal(err)
		}
		return user
	}

	f.idp.Groups = []string{"baleia-admins"}
	state, cookie := f.start(t)
	if result := f.finish(t, state, cookie); result.Location != "/sql" {
		t.Fatalf("first login: %q", result.Location)
	}
	if user := lookup(); user.Role != identity.RoleAdmin {
		t.Errorf("first login role = %q, want admin", user.Role)
	}

	f.idp.Groups = nil
	state, cookie = f.start(t)
	if result := f.finish(t, state, cookie); result.Location != "/sql" {
		t.Fatalf("second login: %q", result.Location)
	}
	if user := lookup(); user.Role != identity.RoleMember {
		t.Errorf("second login role = %q, want member", user.Role)
	}
}

// TestOIDC_ConfigurableClaims: claim names come from the provider config
// (#279).
func TestOIDC_ConfigurableClaims(t *testing.T) {
	f := newOIDCFlowWithConfig(t, func(issuer string) auth.OIDCProviderConfig {
		cfg := oidcConfig(issuer)
		cfg.Claims = auth.OIDCClaimsConfig{Subject: "user_id", Email: "mail", Name: "display_name", Groups: "memberOf"}
		cfg.RoleMapping = []identity.RoleMapping{{Group: "baleia-admins", Role: identity.RoleAdmin}}
		return cfg
	})
	f.idp.ExtraClaims = map[string]any{
		"user_id":      "custom-subject",
		"mail":         "custom@example.test",
		"display_name": "Custom Name",
		"memberOf":     []string{"baleia-admins"},
	}
	state, cookie := f.start(t)
	if result := f.finish(t, state, cookie); result.Location != "/sql" {
		t.Fatalf("login: %q", result.Location)
	}
	external := "custom-subject"
	user, err := f.q.GetUserByExternalID(context.Background(), db.GetUserByExternalIDParams{
		AuthProviderID: f.provider.ID, ExternalID: &external,
	})
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "custom@example.test" || user.Name != "Custom Name" || user.Role != identity.RoleAdmin {
		t.Errorf("user = %+v", user)
	}
}

// TestOIDC_TeamMapping: mapped teams are upserted on login (#279).
func TestOIDC_TeamMapping(t *testing.T) {
	pool := testdb.New(t)
	q := db.New(pool)
	idp, err := oidctest.New("", "client")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(idp.Close)
	password := "not-used"
	owner, err := q.CreateUser(context.Background(), db.CreateUserParams{
		Name: "Owner", Email: "owner@example.test", PasswordHash: &password, Role: "admin", AuthProvider: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	team, err := q.CreateTeam(context.Background(), db.CreateTeamParams{Name: "Data", CreatedBy: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	cfg := oidcConfig(idp.URL)
	cfg.TeamMapping = []identity.TeamMapping{{Group: "data-eng", TeamID: idString(team.ID), TeamRole: identity.TeamRoleOwner}}
	f := &oidcFlowFixture{
		pool: pool, q: q, h: oidcHandlers(t, q), idp: idp,
		provider: oidcProviderWithConfig(t, q, cfg),
	}
	f.idp.Groups = []string{"data-eng"}
	state, cookie := f.start(t)
	if result := f.finish(t, state, cookie); result.Location != "/sql" {
		t.Fatalf("login: %q", result.Location)
	}
	user, err := f.q.GetUserByExternalID(context.Background(), db.GetUserByExternalIDParams{
		AuthProviderID: f.provider.ID, ExternalID: &f.idp.Subject,
	})
	if err != nil {
		t.Fatal(err)
	}
	role, err := f.q.GetTeamMemberRole(context.Background(), db.GetTeamMemberRoleParams{TeamID: team.ID, UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	if role != identity.TeamRoleOwner {
		t.Errorf("team role = %q, want owner", role)
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
