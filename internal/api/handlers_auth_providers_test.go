package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
)

// TestListAuthProviders_OnlyEnabled: the public login list contains enabled
// OIDC and LDAP providers and no configuration.
func TestListAuthProviders_OnlyEnabled(t *testing.T) {
	pool := testdb.New(t)
	q := db.New(pool)
	h := testAPI(handlerFields{queries: q, tx: directTx{q: q}})
	ctx := context.Background()

	create := func(name, kind string, enabled bool) {
		t.Helper()
		if _, err := q.CreateAuthProvider(ctx, db.CreateAuthProviderParams{
			Name: name, Type: kind, ConfigEnc: "sealed", Enabled: enabled,
		}); err != nil {
			t.Fatal(err)
		}
	}
	create("okta", "oidc", true)
	create("old-idp", "oidc", false)
	create("corp-ldap", "ldap", true)

	resp, err := h.ListAuthProviders(ctx, ListAuthProvidersRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	items, ok := resp.(ListAuthProviders200JSONResponse)
	if !ok {
		t.Fatalf("list = %T, want 200", resp)
	}
	if len(items) != 2 {
		t.Fatalf("providers = %d, want 2", len(items))
	}

	byName := map[string]AuthProvider{}
	for _, item := range items {
		byName[item.Name] = item
	}
	if byName["okta"].Type != AuthProviderType("oidc") {
		t.Errorf("okta type = %q", byName["okta"].Type)
	}
	if byName["corp-ldap"].Type != AuthProviderType("ldap") {
		t.Errorf("ldap type = %q", byName["corp-ldap"].Type)
	}
	if _, found := byName["old-idp"]; found {
		t.Error("a disabled provider was listed")
	}

	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"config", "config_enc", "secret", "password"} {
		if strings.Contains(string(raw), key) {
			t.Errorf("response leaked %q: %s", key, raw)
		}
	}
}
