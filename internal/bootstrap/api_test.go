package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/datapolicy"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
)

type fakeInitialPublisher struct {
	calls     int
	published bool
	err       error
}

func (f *fakeInitialPublisher) PublishInitial(context.Context) (datapolicy.Revision, bool, error) {
	f.calls++
	return datapolicy.Revision{ID: uuid.New(), Number: 1}, f.published, f.err
}

func TestPublishInitialDataPolicies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		p    *fakeInitialPublisher
	}{
		{name: "published", p: &fakeInitialPublisher{published: true}},
		{name: "already published", p: &fakeInitialPublisher{}},
		{name: "a failure does not stop the API", p: &fakeInitialPublisher{err: errors.New("storage down")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			PublishInitialDataPolicies(t.Context(), discard, tt.p)
			if tt.p.calls != 1 {
				t.Fatalf("PublishInitial called %d times, want 1", tt.p.calls)
			}
		})
	}
	// Data policies off: nothing to call.
	PublishInitialDataPolicies(t.Context(), discard, nil)
}

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
