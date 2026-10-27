package catalogs

import (
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

func testKeyring(t *testing.T) *crypto.Keyring {
	t.Helper()
	kr, err := crypto.NewKeyring("test-key-0123456789abcdef0123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

// TestDecodeCredentials_Unreadable: any envelope that cannot be opened yields a
// typed error and zero credentials; the stored value is never used as plaintext.
func TestDecodeCredentials_Unreadable(t *testing.T) {
	t.Parallel()
	kr := testKeyring(t)
	id := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	other := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	sealed, err := kr.Seal([]byte(`{"client_secret":"s3cr3t"}`), CredentialsAAD(id))
	if err != nil {
		t.Fatal(err)
	}
	legacy := "bGVnYWN5LWNpcGhlcnRleHQ"

	cases := map[string]struct {
		conn db.CatalogConnection
		kr   *crypto.Keyring
	}{
		"legacy format":           {db.CatalogConnection{ID: id, CredentialsEnc: &legacy}, kr},
		"envelope of another row": {db.CatalogConnection{ID: other, CredentialsEnc: &sealed}, kr},
		"no keyring configured":   {db.CatalogConnection{ID: id, CredentialsEnc: &sealed}, nil},
	}
	for name, tc := range cases {
		got, err := DecodeCredentials(tc.conn, tc.kr)
		if !errors.Is(err, ErrCredentialsUnreadable) {
			t.Errorf("%s: err = %v, want ErrCredentialsUnreadable", name, err)
		}
		if got != (Credentials{}) {
			t.Errorf("%s: returned credentials %+v alongside an error", name, got)
		}
	}

	good, err := DecodeCredentials(db.CatalogConnection{ID: id, CredentialsEnc: &sealed}, kr)
	if err != nil || good.ClientSecret != "s3cr3t" {
		t.Fatalf("DecodeCredentials = %+v, %v", good, err)
	}
	none, err := DecodeCredentials(db.CatalogConnection{ID: id}, nil)
	if err != nil || none != (Credentials{}) {
		t.Fatalf("no credentials stored: %+v, %v", none, err)
	}
}
