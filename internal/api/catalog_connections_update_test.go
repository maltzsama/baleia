package api

import (
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestBuildConnectionUpdateParams_OnlyPresentFields pins the fix for
// concurrent-edit clobbering: a PATCH must only set the fields it carries, so
// omitted columns fall through to the SQL COALESCE and keep their stored value.
func TestBuildConnectionUpdateParams_OnlyPresentFields(t *testing.T) {
	id := pgtype.UUID{Bytes: [16]byte{0xAB}, Valid: true}
	kr := testKeyring(t)

	t.Run("name only leaves the rest nil", func(t *testing.T) {
		name := "renamed"
		got, err := buildConnectionUpdateParams(id, UpdateCatalogConnectionRequest{Name: &name}, kr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Name == nil || *got.Name != "renamed" {
			t.Fatalf("Name = %v, want \"renamed\"", got.Name)
		}
		if got.Url != nil || got.CredentialsEnc != nil || got.DefaultReadonly != nil {
			t.Fatalf("omitted fields must stay nil: %+v", got)
		}
		if got.ID != id {
			t.Fatalf("ID = %v, want %v", got.ID, id)
		}
	})

	t.Run("credentials only are encrypted and isolated", func(t *testing.T) {
		creds := map[string]interface{}{"password": "s3cr3t-value"}
		got, err := buildConnectionUpdateParams(id, UpdateCatalogConnectionRequest{Credentials: &creds}, kr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.CredentialsEnc == nil {
			t.Fatal("CredentialsEnc is nil, want ciphertext")
		}
		if strings.Contains(*got.CredentialsEnc, "s3cr3t-value") {
			t.Fatalf("credentials stored in cleartext: %q", *got.CredentialsEnc)
		}
		if got.Name != nil || got.Url != nil || got.DefaultReadonly != nil {
			t.Fatalf("omitted fields must stay nil: %+v", got)
		}
		if _, err := kr.Open(*got.CredentialsEnc, catalogs.CredentialsAAD(id)); err != nil {
			t.Fatalf("update must bind the envelope to the connection id: %v", err)
		}
	})

	t.Run("credentials without a keyring are refused", func(t *testing.T) {
		creds := map[string]interface{}{"password": "s3cr3t-value"}
		if _, err := buildConnectionUpdateParams(id, UpdateCatalogConnectionRequest{Credentials: &creds}, nil); err == nil {
			t.Fatal("update accepted credentials with no keyring")
		}
	})

	t.Run("empty body sets nothing", func(t *testing.T) {
		got, err := buildConnectionUpdateParams(id, UpdateCatalogConnectionRequest{}, kr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Name != nil || got.Url != nil || got.CredentialsEnc != nil || got.DefaultReadonly != nil {
			t.Fatalf("empty body must set no field: %+v", got)
		}
	})
}
