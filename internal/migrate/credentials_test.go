package migrate

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	jwtSecret = "jwt-secret-0123456789abcdef"
	encKey    = "enc-key-0123456789abcdef0123456789"
)

type memStore struct{ rows map[string]string }

func (m *memStore) ListCredentials(context.Context) ([]CredentialRow, error) {
	var out []CredentialRow
	for id, enc := range m.rows {
		out = append(out, CredentialRow{ID: id, Enc: enc})
	}
	return out, nil
}

func (m *memStore) UpdateCredentials(_ context.Context, id, enc string) error {
	m.rows[id] = enc
	return nil
}

func legacySeal(t *testing.T, plaintext, secret string) string {
	t.Helper()
	h := sha256.Sum256([]byte(secret))
	block, _ := aes.NewCipher(h[:])
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	return base64.RawStdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(plaintext), nil))
}

func TestCredentialsMigration_Idempotent(t *testing.T) {
	kr, err := crypto.NewKeyring(encKey, "")
	if err != nil {
		t.Fatal(err)
	}
	a, b, bad := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store := &memStore{rows: map[string]string{
		a:   legacySeal(t, `{"client_secret":"alpha"}`, jwtSecret),
		b:   legacySeal(t, `{"client_secret":"beta"}`, jwtSecret),
		bad: legacySeal(t, `{"client_secret":"gamma"}`, "some-other-secret-0123456789"),
	}}
	badBefore := store.rows[bad]

	res, err := MigrateCredentials(context.Background(), store, kr, jwtSecret)
	if err != nil {
		t.Fatalf("MigrateCredentials: %v", err)
	}
	if res.Migrated != 2 || res.Skipped != 0 || len(res.Failed) != 1 || res.Failed[0] != bad {
		t.Fatalf("result = %+v", res)
	}
	if store.rows[bad] != badBefore {
		t.Fatal("a row that failed legacy decryption was modified")
	}
	for id, want := range map[string]string{a: "alpha", b: "beta"} {
		enc := store.rows[id]
		if !strings.HasPrefix(enc, "v1:") {
			t.Fatalf("row %s not v1 after migration: %q", id, enc)
		}
		u, _ := uuid.Parse(id)
		plain, err := kr.Open(enc, catalogs.CredentialsAAD(pgtype.UUID{Bytes: u, Valid: true}))
		if err != nil || !strings.Contains(string(plain), want) {
			t.Fatalf("row %s: Open = %q, %v", id, plain, err)
		}
	}

	snapshot := map[string]string{a: store.rows[a], b: store.rows[b]}
	res, err = MigrateCredentials(context.Background(), store, kr, jwtSecret)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if res.Migrated != 0 || res.Skipped != 2 || len(res.Failed) != 1 {
		t.Fatalf("second run result = %+v", res)
	}
	for id, enc := range snapshot {
		if store.rows[id] != enc {
			t.Fatalf("second run rewrote row %s", id)
		}
	}
}

// TestCredentialsMigration_CompletesKeyRotation: v1 rows sealed under the
// previous key are re-sealed under the current one, so the previous key can
// then be dropped.
func TestCredentialsMigration_CompletesKeyRotation(t *testing.T) {
	const oldKey = "old-key-0123456789abcdef0123456789"
	oldKR, _ := crypto.NewKeyring(oldKey, "")
	rotated, err := crypto.NewKeyring(encKey, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	aad := catalogs.CredentialsAAD(pgtype.UUID{Bytes: id, Valid: true})
	sealed, _ := oldKR.Seal([]byte(`{"client_secret":"alpha"}`), aad)
	store := &memStore{rows: map[string]string{id.String(): sealed}}

	res, err := MigrateCredentials(context.Background(), store, rotated, jwtSecret)
	if err != nil || res.Resealed != 1 || res.Migrated != 0 || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	// With the previous key dropped, the row still opens.
	onlyNew, _ := crypto.NewKeyring(encKey, "")
	if plain, err := onlyNew.Open(store.rows[id.String()], aad); err != nil || !strings.Contains(string(plain), "alpha") {
		t.Fatalf("after rotation: %q, %v", plain, err)
	}
	if res, _ = MigrateCredentials(context.Background(), store, onlyNew, jwtSecret); res.Skipped != 1 || res.Resealed != 0 {
		t.Fatalf("second run = %+v", res)
	}
}
