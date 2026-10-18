package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/crypto"
)

type fakeProviderStore struct {
	rows    []ProviderRow
	updated map[string]string
}

func (f *fakeProviderStore) ListProviders(context.Context) ([]ProviderRow, error) {
	return f.rows, nil
}

func (f *fakeProviderStore) UpdateProvider(_ context.Context, id, enc string) error {
	f.updated[id] = enc
	return nil
}

func TestMigrateOIDCProviders(t *testing.T) {
	current := strings.Repeat("c", 32)
	previous := strings.Repeat("p", 32)
	kr, err := crypto.NewKeyring(current, previous)
	if err != nil {
		t.Fatal(err)
	}
	old, err := crypto.NewKeyring(previous, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := auth.OIDCProviderConfig{IssuerURL: "https://idp", ClientID: "c", ClientSecret: "s"}
	currentEnc, err := auth.SealOIDCConfig(kr, "id-current", cfg)
	if err != nil {
		t.Fatal(err)
	}
	oldEnc, err := auth.SealOIDCConfig(old, "id-old", cfg)
	if err != nil {
		t.Fatal(err)
	}

	store := &fakeProviderStore{updated: map[string]string{}, rows: []ProviderRow{
		{ID: "id-plain", Enc: `{"issuer_url":"https://idp","client_id":"c","client_secret":"s"}`},
		{ID: "id-current", Enc: currentEnc},
		{ID: "id-old", Enc: oldEnc},
		{ID: "id-bad", Enc: "not-json"},
		{ID: "id-empty", Enc: ""},
	}}
	res, err := MigrateOIDCProviders(context.Background(), store, kr)
	if err != nil {
		t.Fatal(err)
	}
	if res.Migrated != 1 || res.Resealed != 1 || res.Skipped != 2 || len(res.Failed) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if !strings.HasPrefix(store.updated["id-plain"], "v1:") {
		t.Errorf("plaintext row was not sealed: %q", store.updated["id-plain"])
	}
	if !kr.IsCurrent(store.updated["id-old"]) {
		t.Errorf("previous-key row was not re-sealed with the current key: %q", store.updated["id-old"])
	}
	if _, ok := store.updated["id-bad"]; ok {
		t.Error("an unparseable row was written")
	}
	if _, ok := store.updated["id-current"]; ok {
		t.Error("an already-current row was rewritten")
	}
}
