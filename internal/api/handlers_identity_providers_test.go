package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
)

func idpTestHandlers(t *testing.T) *Handlers {
	t.Helper()
	pool := testdb.New(t)
	return &Handlers{
		queries: db.New(pool),
		pool:    pool,
		keyring: testKeyring(t),
	}
}

func idpAdminCtx() context.Context {
	return context.WithValue(context.Background(), auth.ContextKeyRole, "admin")
}

func idpMemberCtx() context.Context {
	return context.WithValue(context.Background(), auth.ContextKeyRole, "member")
}

func validOIDCConfig() *OIDCConfig {
	return &OIDCConfig{
		IssuerUrl:    "https://idp.example.test",
		ClientId:     "baleia",
		ClientSecret: "oidc-client-secret",
	}
}

func validLDAPConfig() *LDAPConfig {
	return &LDAPConfig{
		Url:          "ldaps://ldap.example.test",
		BindDn:       "cn=svc,dc=example,dc=test",
		BindPassword: "ldap-bind-secret",
		BaseDn:       "dc=example,dc=test",
		UserFilter:   "(uid={{username}})",
	}
}

// TestIdP_SecretsNeverReturned: list/create/get work and no response carries a
// secret; the stored value is an encrypted envelope.
func TestIdP_SecretsNeverReturned(t *testing.T) {
	h := idpTestHandlers(t)
	ctx := idpAdminCtx()

	created, err := h.CreateIdentityProvider(ctx, CreateIdentityProviderRequestObject{Body: &CreateIdentityProviderRequest{
		Name: "okta", Type: IdentityProviderTypeOidc, Oidc: validOIDCConfig(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	oidcCreated, ok := created.(CreateIdentityProvider201JSONResponse)
	if !ok {
		t.Fatalf("create = %T", created)
	}
	if !oidcCreated.HasClientSecret {
		t.Error("has_client_secret = false")
	}
	if oidcCreated.Oidc == nil || oidcCreated.Oidc.IssuerUrl == nil || *oidcCreated.Oidc.IssuerUrl != "https://idp.example.test" {
		t.Errorf("redacted oidc config = %+v", oidcCreated.Oidc)
	}

	created, err = h.CreateIdentityProvider(ctx, CreateIdentityProviderRequestObject{Body: &CreateIdentityProviderRequest{
		Name: "corp-ldap", Type: IdentityProviderTypeLdap, Ldap: validLDAPConfig(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	ldapCreated, ok := created.(CreateIdentityProvider201JSONResponse)
	if !ok {
		t.Fatalf("create ldap = %T", created)
	}
	if !ldapCreated.HasBindPassword || ldapCreated.Ldap == nil {
		t.Errorf("ldap redaction = %+v", ldapCreated)
	}

	// The plaintext secret never reaches a response.
	for name, v := range map[string]any{"oidc create": oidcCreated, "ldap create": ldapCreated} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"oidc-client-secret", "ldap-bind-secret"} {
			if strings.Contains(string(raw), secret) {
				t.Errorf("%s leaked %q: %s", name, secret, raw)
			}
		}
	}

	// Get returns the redacted config too.
	got, err := h.GetIdentityProvider(ctx, GetIdentityProviderRequestObject{Id: oidcCreated.Id})
	if err != nil {
		t.Fatal(err)
	}
	detail, ok := got.(GetIdentityProvider200JSONResponse)
	if !ok {
		t.Fatalf("get = %T", got)
	}
	raw, _ := json.Marshal(detail)
	if strings.Contains(string(raw), "oidc-client-secret") || strings.Contains(string(raw), `"client_secret":`) {
		t.Errorf("get leaked the secret: %s", raw)
	}

	// List returns every provider and no config.
	list, err := h.ListIdentityProviders(ctx, ListIdentityProvidersRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	items, ok := list.(ListIdentityProviders200JSONResponse)
	if !ok {
		t.Fatalf("list = %T", list)
	}
	if len(items) != 2 {
		t.Fatalf("list len = %d, want 2", len(items))
	}
	for _, item := range items {
		raw, _ := json.Marshal(item)
		if strings.Contains(string(raw), `"client_secret"`) || strings.Contains(string(raw), `"bind_password"`) {
			t.Errorf("list item leaked a secret field: %s", raw)
		}
	}

	// At rest, the config is sealed, not plaintext JSON.
	var enc string
	if err := h.pool.QueryRow(ctx, `SELECT config_enc FROM auth_providers WHERE name = 'okta'`).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(enc, "oidc-client-secret") || !strings.HasPrefix(enc, "v1:") {
		t.Errorf("config_enc is not a sealed envelope: %q", enc)
	}
}

// TestIdP_NonAdmin403: every admin operation refuses a member.
func TestIdP_NonAdmin403(t *testing.T) {
	h := idpTestHandlers(t)
	ctx := idpMemberCtx()

	if got, err := h.ListIdentityProviders(ctx, ListIdentityProvidersRequestObject{}); err != nil {
		t.Fatal(err)
	} else if _, ok := got.(ListIdentityProviders403JSONResponse); !ok {
		t.Errorf("list = %T, want 403", got)
	}

	if got, err := h.GetIdentityProvider(ctx, GetIdentityProviderRequestObject{}); err != nil {
		t.Fatal(err)
	} else if _, ok := got.(GetIdentityProvider403JSONResponse); !ok {
		t.Errorf("get = %T, want 403", got)
	}

	got, err := h.CreateIdentityProvider(ctx, CreateIdentityProviderRequestObject{Body: &CreateIdentityProviderRequest{
		Name: "nope", Type: IdentityProviderTypeOidc, Oidc: validOIDCConfig(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(CreateIdentityProvider403JSONResponse); !ok {
		t.Errorf("create = %T, want 403", got)
	}
}

// TestIdP_CreateValidates: the type-specific validator rejects incomplete or
// insecure configurations before any row is written.
func TestIdP_CreateValidates(t *testing.T) {
	h := idpTestHandlers(t)
	ctx := idpAdminCtx()

	badOIDC := func(mut func(*OIDCConfig)) *CreateIdentityProviderRequest {
		cfg := validOIDCConfig()
		mut(cfg)
		return &CreateIdentityProviderRequest{Name: "bad", Type: IdentityProviderTypeOidc, Oidc: cfg}
	}
	badLDAP := func(mut func(*LDAPConfig)) *CreateIdentityProviderRequest {
		cfg := validLDAPConfig()
		mut(cfg)
		return &CreateIdentityProviderRequest{Name: "bad", Type: IdentityProviderTypeLdap, Ldap: cfg}
	}

	cases := map[string]*CreateIdentityProviderRequest{
		"missing name":                  {Type: IdentityProviderTypeOidc, Oidc: validOIDCConfig()},
		"oidc without config":           {Name: "x", Type: IdentityProviderTypeOidc},
		"oidc without issuer":           badOIDC(func(c *OIDCConfig) { c.IssuerUrl = "" }),
		"oidc plain http issuer":        badOIDC(func(c *OIDCConfig) { c.IssuerUrl = "http://idp.example.test" }),
		"oidc without secret":           badOIDC(func(c *OIDCConfig) { c.ClientSecret = "" }),
		"oidc auto-create no domains":   badOIDC(func(c *OIDCConfig) { c.AutoCreate = boolPtr(true) }),
		"ldap without config":           {Name: "x", Type: IdentityProviderTypeLdap},
		"ldap plain scheme no starttls": badLDAP(func(c *LDAPConfig) { c.Url = "ldap://ldap.example.test" }),
		"ldap without filter placeholder": badLDAP(func(c *LDAPConfig) {
			c.UserFilter = "(uid=admin)"
		}),
		"mismatched config": {Name: "x", Type: IdentityProviderTypeOidc, Ldap: validLDAPConfig()},
		"unknown type":      {Name: "x", Type: IdentityProviderType("saml")},
	}

	for name, body := range cases {
		got, err := h.CreateIdentityProvider(ctx, CreateIdentityProviderRequestObject{Body: body})
		if err != nil {
			t.Fatalf("%s: err = %v", name, err)
		}
		if _, ok := got.(CreateIdentityProvider422JSONResponse); !ok {
			t.Errorf("%s: %T, want 422", name, got)
		}
	}

	// Nothing was written.
	var n int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM auth_providers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("auth_providers rows = %d, want 0", n)
	}
}

func boolPtr(b bool) *bool { return &b }
