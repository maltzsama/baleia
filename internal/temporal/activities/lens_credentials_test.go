package activities

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

type connQuerier struct {
	db.Querier
	conn db.CatalogConnection
}

func (q *connQuerier) GetCatalogConnectionByID(context.Context, pgtype.UUID) (db.CatalogConnection, error) {
	return q.conn, nil
}

const healthTestKey = "health-key-0123456789abcdef0123456789"

// TestTableHealthOpensCredentialsThroughKeyring: the worker must hand the
// catalog client the decrypted credentials, never the stored envelope, and must
// not call out at all when they cannot be opened.
func TestTableHealthOpensCredentialsThroughKeyring(t *testing.T) {
	var hits int32
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		buf := make([]byte, 512)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n]) + r.URL.RawQuery
		http.Error(w, "stop here", http.StatusInternalServerError)
	}))
	defer srv.Close()

	kr, err := crypto.NewKeyring(healthTestKey, "")
	if err != nil {
		t.Fatal(err)
	}
	id := pgtype.UUID{Bytes: [16]byte{7}, Valid: true}
	sealed, err := kr.Seal([]byte(`{"client_id":"cid","client_secret":"sekret"}`), catalogs.CredentialsAAD(id))
	if err != nil {
		t.Fatal(err)
	}
	params := LensAnalyzeTableHealthParams{CatalogID: "08000000-0000-0000-0000-000000000000", CatalogConnectionID: "07000000-0000-0000-0000-000000000000"}
	dialer, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true}) // the stub server is on loopback
	if err != nil {
		t.Fatal(err)
	}
	conns, err := providers.New(dialer)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("readable credentials reach the client decrypted", func(t *testing.T) {
		a := &Activities{keyring: kr, connections: conns, queries: &connQuerier{conn: db.CatalogConnection{ID: id, Type: "polaris", Url: srv.URL, CredentialsEnc: &sealed}}}
		_ = a.lensAnalyzeTableHealth(context.Background(), params) // fails at the stub server
		if atomic.LoadInt32(&hits) == 0 {
			t.Fatal("client never called the catalog manager")
		}
		if !strings.Contains(gotBody, "cid") || strings.Contains(gotBody, "v1:") {
			t.Fatalf("catalog manager saw %q; want decrypted client id, not an envelope", gotBody)
		}
	})

	t.Run("unreadable credentials stop before any call", func(t *testing.T) {
		atomic.StoreInt32(&hits, 0)
		legacy := "bGVnYWN5LWNpcGhlcnRleHQ"
		a := &Activities{keyring: kr, connections: conns, queries: &connQuerier{conn: db.CatalogConnection{ID: id, Type: "polaris", Url: srv.URL, CredentialsEnc: &legacy}}}
		err := a.lensAnalyzeTableHealth(context.Background(), params)
		if !errors.Is(err, catalogs.ErrCredentialsUnreadable) {
			t.Fatalf("err = %v, want ErrCredentialsUnreadable", err)
		}
		if atomic.LoadInt32(&hits) != 0 {
			t.Fatal("catalog manager was called with unreadable credentials")
		}
	})
}
