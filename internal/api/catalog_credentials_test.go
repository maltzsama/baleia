package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/jackc/pgx/v5/pgtype"
)

// testSecret is a valid-length encryption key for the credential tests.
const testSecret = "test-secret-0123456789abcdef-0123456789"

func testKeyring(t *testing.T) *crypto.Keyring {
	t.Helper()
	kr, err := crypto.NewKeyring(testSecret, "")
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

var testConnID = pgtype.UUID{Bytes: [16]byte{0xC0, 0xFF, 0xEE}, Valid: true}

func TestEncodeCredentialsNil(t *testing.T) {
	got, err := encodeCredentials(testKeyring(t), nil, testConnID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil for nil credentials, got %q", *got)
	}
}

// TestEncodeCredentialsRequiresKeyring: without a keyring nothing is stored.
func TestEncodeCredentialsRequiresKeyring(t *testing.T) {
	_, err := encodeCredentials(nil, map[string]any{"password": "s3cr3t-value"}, testConnID)
	if !errors.Is(err, errNoKeyring) {
		t.Fatalf("want errNoKeyring, got %v", err)
	}
}

// TestEncodeCredentialsEncrypts pins the security invariant of the write path:
// the stored value is an envelope that never contains the secret in cleartext,
// and it round-trips back to the original credentials for the same row.
func TestEncodeCredentialsEncrypts(t *testing.T) {
	kr := testKeyring(t)
	creds := map[string]any{"username": "alice", "password": "s3cr3t-value"}

	enc, err := encodeCredentials(kr, creds, testConnID)
	if err != nil {
		t.Fatalf("encodeCredentials: %v", err)
	}
	if enc == nil || !strings.HasPrefix(*enc, "v1:") {
		t.Fatalf("want a v1 envelope, got %v", enc)
	}
	if strings.Contains(*enc, "s3cr3t-value") || strings.Contains(*enc, "alice") {
		t.Fatalf("stored value leaks the secret in cleartext: %q", *enc)
	}

	plain, err := kr.Open(*enc, catalogs.CredentialsAAD(testConnID))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(plain, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["username"] != "alice" || decoded["password"] != "s3cr3t-value" {
		t.Fatalf("round-trip mismatch: %v", decoded)
	}
}

// TestEncodeCredentialsBoundToRow: a ciphertext copied to another connection
// must not open there.
func TestEncodeCredentialsBoundToRow(t *testing.T) {
	kr := testKeyring(t)
	enc, err := encodeCredentials(kr, map[string]any{"password": "s3cr3t-value"}, testConnID)
	if err != nil {
		t.Fatal(err)
	}
	other := pgtype.UUID{Bytes: [16]byte{0xDE, 0xAD}, Valid: true}
	if _, err := kr.Open(*enc, catalogs.CredentialsAAD(other)); !errors.Is(err, crypto.ErrCorrupt) {
		t.Fatalf("envelope opened for a different connection: %v", err)
	}
}

func TestCreateCredentials(t *testing.T) {
	t.Setenv("POLARIS_CLIENT_ID", "")
	t.Setenv("POLARIS_CLIENT_SECRET", "")
	generic := map[string]interface{}{"client_id": "id", "client_secret": "secret"}

	t.Run("generic credentials are used for every type", func(t *testing.T) {
		for _, typ := range []CreateCatalogConnectionRequestType{"polaris", "nessie", "glue", "hive", "iceberg", "postgresql", "mysql"} {
			got := createCredentials(&CreateCatalogConnectionRequest{Type: typ, Credentials: &generic})
			if got == nil {
				t.Errorf("%s: generic credentials dropped", typ)
			}
		}
	})
	t.Run("typed credentials win over generic", func(t *testing.T) {
		typed := &SqlCredentials{}
		got := createCredentials(&CreateCatalogConnectionRequest{Type: "postgresql", PostgresqlCredentials: typed, Credentials: &generic})
		if got != typed {
			t.Fatalf("got %v, want the typed credentials", got)
		}
	})
	t.Run("no credentials is an untyped nil", func(t *testing.T) {
		if got := createCredentials(&CreateCatalogConnectionRequest{Type: "postgresql"}); got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})
	t.Run("polaris falls back to the environment", func(t *testing.T) {
		t.Setenv("POLARIS_CLIENT_ID", "env-id")
		t.Setenv("POLARIS_CLIENT_SECRET", "env-secret")
		got, ok := createCredentials(&CreateCatalogConnectionRequest{Type: "polaris"}).(map[string]string)
		if !ok || got["client_id"] != "env-id" {
			t.Fatalf("got %v", got)
		}
		if createCredentials(&CreateCatalogConnectionRequest{Type: "nessie"}) != nil {
			t.Fatal("env fallback must be polaris-only")
		}
	})
}
