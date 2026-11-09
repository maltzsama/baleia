package bootstrap

import (
	"testing"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
)

func TestSealCredentialsNilKeyring(t *testing.T) {
	t.Parallel()
	if sealCredentials(nil) != nil {
		t.Fatal("sealCredentials(nil) returned a function; want nil")
	}
}

func TestSealCredentialsBindsToConnection(t *testing.T) {
	t.Parallel()
	kr, err := crypto.NewKeyring("key-a-0123456789abcdef0123456789abcdef", "")
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	id := db.UUID{Bytes: uuid.New(), Valid: true}
	sealed, err := sealCredentials(kr)([]byte("secret"), id)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	plain, err := kr.Open(sealed, catalogs.CredentialsAAD(id))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(plain) != "secret" {
		t.Fatalf("opened %q; want %q", plain, "secret")
	}
}
