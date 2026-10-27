package trino

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
)

const registryTestKey = "registry-key-0123456789abcdef0123456789"

// propsStore captures the properties of the last registry upsert.
type propsStore struct {
	props []byte
	row   db.GetTrinoCatalogRegistryStateRow
}

func (s *propsStore) UpsertTrinoCatalogRegistry(_ context.Context, arg db.UpsertTrinoCatalogRegistryParams) error {
	s.props = arg.Column4
	s.row = db.GetTrinoCatalogRegistryStateRow{CatalogVersion: arg.CatalogVersion, Enabled: true}
	return nil
}

func (*propsStore) SoftDeleteTrinoCatalogRegistry(context.Context, db.SoftDeleteTrinoCatalogRegistryParams) error {
	return nil
}

func (s *propsStore) GetTrinoCatalogRegistryState(context.Context, db.GetTrinoCatalogRegistryStateParams) (db.GetTrinoCatalogRegistryStateRow, error) {
	return s.row, nil
}

func registryKeyring(t *testing.T) *crypto.Keyring {
	t.Helper()
	kr, err := crypto.NewRegistryKeyring(registryTestKey, "")
	if err != nil {
		t.Fatalf("NewRegistryKeyring: %v", err)
	}
	return kr
}

func stageProps(t *testing.T, b *Binder, p bindings.Projection) map[string]string {
	t.Helper()
	s := &propsStore{}
	if err := b.WithStore(s).Stage(context.Background(), bindings.EngineRef{}, p); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	var props map[string]string
	if err := json.Unmarshal(s.props, &props); err != nil {
		t.Fatalf("unmarshal registry properties: %v", err)
	}
	return props
}

// TestStageSealsRegistrySecrets: with a registry keyring, a secret value is
// written as @baleia-enc[...] and opens back to the plaintext; the version is
// untouched.
func TestStageSealsRegistrySecrets(t *testing.T) {
	t.Parallel()
	kr := registryKeyring(t)
	b := New(slog.New(slog.DiscardHandler), nil, nil).WithRegistryKeyring(kr)
	p := bindings.Projection{
		EngineCatalog: "lake",
		Connector:     "iceberg",
		Properties:    map[string]string{"connection-password": "s3cr3t", "connection-user": "u"},
		SecretKeys:    []string{"connection-password"},
		Version:       "deadbeef",
	}
	props := stageProps(t, b, p)
	sealed := props["connection-password"]
	if !strings.HasPrefix(sealed, registryEnvelopePrefix) || strings.Contains(sealed, "s3cr3t") {
		t.Fatalf("secret was not sealed: %q", sealed)
	}
	if props["connection-user"] != "u" {
		t.Fatalf("non-secret property changed: %q", props["connection-user"])
	}
	// The caller's projection is not mutated (the DDL path still needs the
	// plaintext value).
	if p.Properties["connection-password"] != "s3cr3t" {
		t.Fatalf("Stage mutated the projection: %q", p.Properties["connection-password"])
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(sealed, registryEnvelopePrefix), "]")
	plain, err := kr.Open(inner, "")
	if err != nil || string(plain) != "s3cr3t" {
		t.Fatalf("Open = %q, %v", plain, err)
	}
}

// TestStageKeepsPlaintextWithoutKeyring: Phase A stays the default; no keyring
// means the registry keeps its current shape.
func TestStageKeepsPlaintextWithoutKeyring(t *testing.T) {
	t.Parallel()
	b := New(slog.New(slog.DiscardHandler), nil, nil)
	props := stageProps(t, b, bindings.Projection{
		EngineCatalog: "lake",
		Connector:     "iceberg",
		Properties:    map[string]string{"connection-password": "s3cr3t"},
		SecretKeys:    []string{"connection-password"},
	})
	if props["connection-password"] != "s3cr3t" {
		t.Fatalf("plaintext expected without keyring, got %q", props["connection-password"])
	}
}

// TestStageLeavesReferencesAlone: an external reference is not a resolved
// secret and must survive unchanged.
func TestStageLeavesReferencesAlone(t *testing.T) {
	t.Parallel()
	b := New(slog.New(slog.DiscardHandler), nil, nil).WithRegistryKeyring(registryKeyring(t))
	props := stageProps(t, b, bindings.Projection{
		EngineCatalog: "lake",
		Connector:     "iceberg",
		Properties:    map[string]string{"iceberg.rest-catalog.oauth2.token": "@baleia-secret[env:TOKEN]"},
	})
	if props["iceberg.rest-catalog.oauth2.token"] != "@baleia-secret[env:TOKEN]" {
		t.Fatalf("reference changed: %q", props["iceberg.rest-catalog.oauth2.token"])
	}
}
