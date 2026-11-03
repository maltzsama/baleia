package catalogidentity

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeStore struct {
	users map[string]db.User
	conns map[string]db.CatalogConnection // engine catalog name -> connection
	reads int
}

func (f *fakeStore) GetUserByID(_ context.Context, id pgtype.UUID) (db.User, error) {
	u, ok := f.users[id.String()]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func (f *fakeStore) GetManagedConnectionOfEngineCatalog(_ context.Context, a db.GetManagedConnectionOfEngineCatalogParams) (db.CatalogConnection, error) {
	f.reads++
	c, ok := f.conns[a.EngineCatalogName]
	if !ok {
		return db.CatalogConnection{}, pgx.ErrNoRows
	}
	return c, nil
}

type fakeUserTokens map[string]string

func (f fakeUserTokens) Access(_ context.Context, id pgtype.UUID) (string, error) {
	return f[id.String()], nil
}

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

// unityConn is a Unity connection with a static token, sealed like the API
// stores it; userIdentity sets the per-user flag.
func unityConn(t *testing.T, kr *crypto.Keyring, userIdentity bool) db.CatalogConnection {
	t.Helper()
	id := newID()
	cfg := map[string]string{"url": "http://unity:8080"}
	if userIdentity {
		cfg[connections.UserIdentityKey] = "true"
	}
	raw, _ := json.Marshal(cfg)
	enc, err := kr.Seal([]byte(`{"token":"service-token"}`), catalogs.CredentialsAAD(id))
	if err != nil {
		t.Fatal(err)
	}
	return db.CatalogConnection{ID: id, Type: "unity", Url: "http://unity:8080", Config: raw, CredentialsEnc: &enc, UpdatedAt: pgtype.Timestamptz{Time: time.Unix(1, 0), Valid: true}}
}

func TestResolver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	kr, err := crypto.NewKeyring("test-secret-0123456789abcdef-0123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := providers.New(d)
	if err != nil {
		t.Fatal(err)
	}

	oidcUser, expiredUser, localUser := newID(), newID(), newID()
	st := &fakeStore{
		users: map[string]db.User{
			oidcUser.String():    {ID: oidcUser, AuthProvider: "oidc"},
			expiredUser.String(): {ID: expiredUser, AuthProvider: "oidc"},
			localUser.String():   {ID: localUser, AuthProvider: "local"},
		},
		conns: map[string]db.CatalogConnection{
			"per_user":   unityConn(t, kr, true),
			"shared_svc": unityConn(t, kr, false),
		},
	}
	r := New(st, kr, reg, fakeUserTokens{oidcUser.String(): "alice-idp-token"}, nil)
	engine := uuid.NewString()
	spec := func(user pgtype.UUID, catalog string) queryengine.SessionSpec {
		s := queryengine.SessionSpec{Catalog: catalog}
		if user.Valid {
			s.Identity.Principal = user.String()
		}
		return s
	}

	tests := []struct {
		name string
		spec queryengine.SessionSpec
		want string
	}{
		{"identity-provider user carries their own token", spec(oidcUser, "per_user"), "alice-idp-token"},
		{"their token goes on any catalog", spec(oidcUser, ""), "alice-idp-token"},
		{"expired identity-provider user never falls back to the connection", spec(expiredUser, "per_user"), ""},
		{"local user acts as the per-user connection", spec(localUser, "per_user"), "service-token"},
		{"work without a user acts as the per-user connection", spec(pgtype.UUID{}, "per_user"), "service-token"},
		{"connection without per-user identity needs no token", spec(localUser, "shared_svc"), ""},
		{"catalog Baleia does not manage needs no token", spec(localUser, "system"), ""},
		{"no catalog, no user", spec(pgtype.UUID{}, ""), ""},
	}
	for _, tt := range tests {
		got, err := r.CatalogToken(ctx, engine, tt.spec)
		if err != nil || got != tt.want {
			t.Errorf("%s: CatalogToken = %q, %v; want %q", tt.name, got, err, tt.want)
		}
	}

	// A static token is read again on every use (an operator may rotate the
	// secret it references); only "not per-user" is remembered.
	before := st.reads
	for range 3 {
		if _, err := r.CatalogToken(ctx, engine, spec(localUser, "per_user")); err != nil {
			t.Fatal(err)
		}
	}
	if st.reads-before != 3 {
		t.Errorf("binding lookups = %d, want one per session", st.reads-before)
	}
	r.mu.Lock()
	n := len(r.cache)
	r.mu.Unlock()
	if n != 1 {
		t.Errorf("cached %d connections, want only the one that is not per-user", n)
	}
}
