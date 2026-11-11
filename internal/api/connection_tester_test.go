package api

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/authz"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/catalogs/topology"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/icebergrest"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	wsaccess "github.com/galileostudio/baleia/internal/workspaces/access"
)

// draftQuerier fails the test on any write: a draft test persists nothing.
type draftQuerier struct {
	db.Querier
	t *testing.T
}

func (q draftQuerier) CreateCatalogConnection(context.Context, db.CreateCatalogConnectionParams) (db.CatalogConnection, error) {
	q.t.Fatal("a draft test created a connection")
	return db.CatalogConnection{}, nil
}
func (q draftQuerier) UpsertTrinoCatalogRegistry(context.Context, db.UpsertTrinoCatalogRegistryParams) error {
	q.t.Fatal("a draft test wrote the registry")
	return nil
}
func (q draftQuerier) GetDefaultTrinoCluster(context.Context) (db.TrinoCluster, error) {
	return db.TrinoCluster{}, errors.New("no cluster")
}

// fakeREST is an Iceberg REST server: 401 when refuse is set.
func fakeREST(t *testing.T, refuse bool) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refuse {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"defaults":{},"overrides":{}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func loopbackRegistry(t *testing.T) (*connections.Registry, *connections.Dialer) {
	t.Helper()
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := providers.New(d)
	if err != nil {
		t.Fatal(err)
	}
	return reg, d
}

func draftHandlers(t *testing.T, reg *connections.Registry) *API {
	t.Helper()
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return testAPI(handlerFields{queries: draftQuerier{t: t}, connections: reg, settings: SettingsFromConfig(&config.Config{}), kinds: testKinds(t, d)})
}

func draft(url string, intents ...string) *TestConnectionRequest {
	body := &TestConnectionRequest{Name: "d", Type: "iceberg", Config: strMap("url", url), Credentials: anyMap("token", "t0k3n")}
	if len(intents) > 0 {
		body.Intents = &intents
	}
	return body
}

func runDraftAs(t *testing.T, h *API, user, role string, body *TestConnectionRequest) TestConnectionDraftResponseObject {
	t.Helper()
	resp, err := h.TestConnectionDraft(ctxAs(user, role), TestConnectionDraftRequestObject{Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func runDraft(t *testing.T, h *API, user string, body *TestConnectionRequest) TestConnectionDraftResponseObject {
	t.Helper()
	return runDraftAs(t, h, user, "member", body)
}

// TestTransient_RESTPersistsNothing: a draft test of an Iceberg REST
// catalog runs its checks and writes nothing (draftQuerier fails on any
// write).
func TestTransient_RESTPersistsNothing(t *testing.T) {
	t.Parallel()
	reg, _ := loopbackRegistry(t)
	srv := fakeREST(t, false)
	resp := runDraft(t, draftHandlers(t, reg), ownerID, draft(srv.URL+"/catalog"))
	r, ok := resp.(TestConnectionDraft200JSONResponse)
	if !ok || !r.Success || len(*r.Checks) < 1 || (*r.Checks)[0].Name != "ping" || !(*r.Checks)[0].Ok {
		t.Fatalf("result = %+v", resp)
	}
}

// TestTransient_ErrorCodes: upstream failures map to bounded codes, and the
// raw error never reaches the response.
func TestTransient_ErrorCodes(t *testing.T) {
	t.Parallel()
	reg, _ := loopbackRegistry(t)
	srv := fakeREST(t, true)
	r := runDraft(t, draftHandlers(t, reg), ownerID, draft(srv.URL+"/catalog")).(TestConnectionDraft200JSONResponse)
	if r.Success || r.ErrorCode == nil || *r.ErrorCode != codeAuthFailed || strings.Contains(*r.ErrorMessage, "401") {
		t.Fatalf("refused credentials = %+v", r)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close() // nothing listens there now
	r = runDraft(t, draftHandlers(t, reg), ownerID, draft("http://"+addr+"/catalog")).(TestConnectionDraft200JSONResponse)
	if r.Success || *r.ErrorCode != codeUnreachable {
		t.Fatalf("connection refused = %+v", *r.ErrorCode)
	}

	cases := map[string]error{
		codeAuthFailed:          &icebergrest.StatusError{Op: "config", Status: 401},
		codeForbidden:           &icebergrest.StatusError{Op: "config", Status: 403},
		codeTimeout:             context.DeadlineExceeded,
		codeTLS:                 x509.UnknownAuthorityError{},
		codeDeniedNetwork:       fmt.Errorf("dial: %w", connections.ErrDeniedNetwork),
		codeProviderUnsupported: connections.ErrUnknownKind,
	}
	for want, err := range cases {
		if got := classifyTestError(err); got != want {
			t.Errorf("%v -> %s, want %s", err, got, want)
		}
	}
}

// TestTransient_DeniedCIDR: a host on a denied CIDR is refused by the dialer
// before any connection, with the bounded error code.
func TestTransient_DeniedCIDR(t *testing.T) {
	t.Parallel()
	d, err := connections.NewDialer(connections.DialerOptions{DenyCIDRs: []string{"169.254.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := providers.New(d)
	if err != nil {
		t.Fatal(err)
	}
	r := runDraft(t, draftHandlers(t, reg), ownerID, draft("http://169.254.169.254/catalog")).(TestConnectionDraft200JSONResponse)
	if r.Success || *r.ErrorCode != codeDeniedNetwork {
		t.Fatalf("result = %+v", r)
	}
}

// TestTransient_RateLimited: the 11th test of a user in a minute is a 429;
// another user is not affected.
func TestTransient_RateLimited(t *testing.T) {
	t.Parallel()
	reg, _ := loopbackRegistry(t)
	srv := fakeREST(t, false)
	h := draftHandlers(t, reg)
	for i := range 10 {
		if _, ok := runDraft(t, h, ownerID, draft(srv.URL+"/catalog")).(TestConnectionDraft200JSONResponse); !ok {
			t.Fatalf("test %d refused", i+1)
		}
	}
	if _, ok := runDraft(t, h, ownerID, draft(srv.URL+"/catalog")).(TestConnectionDraft429JSONResponse); !ok {
		t.Fatal("the 11th test in a minute must be refused")
	}
	if _, ok := runDraft(t, h, strangerID, draft(srv.URL+"/catalog")).(TestConnectionDraft200JSONResponse); !ok {
		t.Fatal("another user has their own budget")
	}
}

// TestTestConnectionDraft_AdminsOnly: testing an unsaved connection dials a
// user-supplied host, so with BALEIA_CONNECTIONS_CREATE=admins only an admin
// may do it (#610).
func TestTestConnectionDraft_AdminsOnly(t *testing.T) {
	t.Parallel()
	reg, d := loopbackRegistry(t)
	h := testAPI(handlerFields{
		queries: draftQuerier{t: t}, connections: reg,
		settings: SettingsFromConfig(&config.Config{ConnectionsCreate: "admins"}),
		kinds:    testKinds(t, d),
	})
	srv := fakeREST(t, false)

	resp, ok := runDraftAs(t, h, ownerID, "member", draft(srv.URL+"/catalog")).(TestConnectionDraft403JSONResponse)
	if !ok {
		t.Fatal("a member must not test connections when only admins manage them")
	}
	if resp.Code != ErrorCodeForbidden || resp.Message == "" {
		t.Fatalf("unexpected denial payload: %+v", resp)
	}
	if _, ok := runDraftAs(t, h, ownerID, authz.RoleAdmin, draft(srv.URL+"/catalog")).(TestConnectionDraft200JSONResponse); !ok {
		t.Fatal("an admin may test connections")
	}
}

// TestTransient_IntentsFilter: only the requested checks run; a bind on an
// engine the caller may not use is a 404; an unknown intent a 422.
func TestTransient_IntentsFilter(t *testing.T) {
	t.Parallel()
	reg, _ := loopbackRegistry(t)
	srv := fakeREST(t, false)
	h := draftHandlers(t, reg)
	r := runDraft(t, h, ownerID, draft(srv.URL+"/catalog", "direct_engine")).(TestConnectionDraft200JSONResponse)
	if len(*r.Checks) != 1 || (*r.Checks)[0].Name != "direct_engine" {
		t.Fatalf("checks = %+v", *r.Checks)
	}
	if _, ok := runDraft(t, h, ownerID, draft(srv.URL+"/catalog", "nonsense")).(TestConnectionDraft422JSONResponse); !ok {
		t.Fatal("an unknown intent is a 422")
	}
	h2, _, _ := newAuthzHandlers(t)
	h2.connections = reg
	if _, ok := runDraft(t, h2, strangerID, draft(srv.URL+"/catalog", "bind:"+uuidString(db.UUID{Bytes: [16]byte{0xE1}, Valid: true}))).(TestConnectionDraft404JSONResponse); !ok {
		t.Fatal("binding into an engine the caller may not use is a 404")
	}
}

// TestTransient_DirectEngineNotRegistered: a kind without an engine adapter
// yet skips the direct check; nothing is registered.
func TestTransient_DirectEngineNotRegistered(t *testing.T) {
	t.Parallel()
	reg, d := loopbackRegistry(t)
	p, _ := reg.Get(connections.KindPostgreSQL)
	c, err := connectionTest{h: testAPI(handlerFields{kinds: testKinds(t, d)}).CatalogHandlers, provider: p}.directEngine(context.Background())
	if err != nil || c.Skipped == nil || !*c.Skipped {
		t.Fatalf("check = %+v, %v", c, err)
	}
}

// ephemeralFake records the ephemeral registry rows.
type ephemeralFake struct {
	*authzFake
	mu       sync.Mutex
	inserted []string
	deleted  []string
}

func (q *ephemeralFake) InsertEphemeralRegistryRow(_ context.Context, p db.InsertEphemeralRegistryRowParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.inserted = append(q.inserted, p.CatalogName)
	return nil
}
func (q *ephemeralFake) DeleteEphemeralRegistryRow(_ context.Context, p db.DeleteEphemeralRegistryRowParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.deleted = append(q.deleted, p.CatalogName)
	return nil
}

// TestTransient_SQLCleanupOnClientCancel: when the request is cancelled
// during a bind check, the test catalog is still dropped and its registry
// row hard-deleted (cleanup never uses the request context).
func TestTransient_SQLCleanupOnClientCancel(t *testing.T) {
	t.Parallel()
	h, q, ft := newAuthzHandlers(t)
	ef := &ephemeralFake{authzFake: q}
	h.queries = ef
	h.access = wsaccess.New(h.queries)
	h.topology = topology.New(h.queries)
	reg, _ := loopbackRegistry(t)
	p, _ := reg.Get(connections.KindPostgreSQL)
	cfg, err := p.Validate(connections.Config{Kind: connections.KindPostgreSQL,
		Values: map[string]string{"host": "db", "database": "sales", "username": "u"}, Secrets: map[string]string{"password": "p"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the client went away
	_, err = connectionTest{h: h.CatalogHandlers, provider: p, cfg: cfg}.bind(ctx, testBind{
		engine:  db.Engine{ID: db.UUID{Bytes: [16]byte{0xE1}, Valid: true}, Name: "trino"},
		cluster: db.TrinoCluster{ID: db.UUID{Bytes: [16]byte{0xCC}, Valid: true}},
	})
	if err == nil {
		t.Fatal("a cancelled request fails the check")
	}
	if len(ef.inserted) != 1 || !strings.HasPrefix(ef.inserted[0], bindtrino.EphemeralPrefix) || len(ef.deleted) != 1 || ef.deleted[0] != ef.inserted[0] {
		t.Fatalf("inserted %v deleted %v", ef.inserted, ef.deleted)
	}
	drops := 0
	for _, s := range ft.statements() {
		if strings.HasPrefix(s, "DROP CATALOG IF EXISTS "+bindtrino.EphemeralPrefix) {
			drops++
		}
	}
	if drops != 1 {
		t.Fatalf("statements = %v", ft.statements())
	}
}

// fakeUnity is a Unity Catalog server that lists the given catalogs.
func fakeUnity(t *testing.T, catalogs ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/2.1/unity-catalog/catalogs" {
			http.NotFound(w, r)
			return
		}
		parts := make([]string, 0, len(catalogs))
		for _, c := range catalogs {
			parts = append(parts, fmt.Sprintf(`{"name":%q}`, c))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"catalogs":[`+strings.Join(parts, ",")+`]}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestTransient_BindCatalogManager: a catalog manager's bind check binds a
// source ref (a projection without one is invalid): the warehouse the caller
// will create the catalog with, else the first catalog the connection lists.
// It names it in the check, and fails as not_found, binding nothing, when
// there is neither.
func TestTransient_BindCatalogManager(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		catalogs  []string
		listed    []connections.CatalogRef // what list_catalogs left, nil when it did not run
		warehouse string
		want      string
		wantErr   error
	}{
		{name: "binds the given warehouse", catalogs: []string{"first"}, warehouse: "sumeh_demo", want: "sumeh_demo"},
		{name: "lists the catalogs itself", catalogs: []string{"sumeh_demo", "system"}, want: "sumeh_demo"},
		{name: "reuses the list_catalogs result", listed: []connections.CatalogRef{{Name: "listed"}}, want: "listed"},
		{name: "no catalog", catalogs: nil, wantErr: connections.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, q, ft := newAuthzHandlers(t)
			ef := &ephemeralFake{authzFake: q}
			h.queries = ef
			h.access = wsaccess.New(h.queries)
			h.topology = topology.New(h.queries)
			reg, _ := loopbackRegistry(t)
			p, _ := reg.Get(connections.KindUnity)
			cfg, err := p.Validate(connections.Config{Kind: connections.KindUnity,
				Values:  map[string]string{"url": fakeUnity(t, tt.catalogs...).URL, "iceberg_path": "/api/2.1/unity-catalog/iceberg-rest"},
				Secrets: map[string]string{"token": "t0k3n"}})
			if err != nil {
				t.Fatal(err)
			}
			c, err := connectionTest{h: h.CatalogHandlers, provider: p, cfg: cfg, catalogs: tt.listed, warehouse: tt.warehouse}.bind(context.Background(), testBind{
				engine:  db.Engine{ID: db.UUID{Bytes: [16]byte{0xE1}, Valid: true}, Name: "trino"},
				cluster: db.TrinoCluster{ID: db.UUID{Bytes: [16]byte{0xCC}, Valid: true}},
			})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) || classifyTestError(err) != codeNotFound {
					t.Fatalf("err = %v, want %v classified %s", err, tt.wantErr, codeNotFound)
				}
				if len(ef.inserted) != 0 {
					t.Fatalf("bound %v with no catalog to bind", ef.inserted)
				}
				return
			}
			if err != nil {
				t.Fatalf("bind: %v", err)
			}
			if c.Name != "bind:trino" || c.Detail == nil || *c.Detail != tt.want {
				t.Fatalf("check = %+v", c)
			}
			created := false
			for _, s := range ft.statements() {
				if strings.HasPrefix(s, "CREATE CATALOG") && strings.Contains(s, "'"+tt.want+"'") {
					created = true
				}
			}
			if !created {
				t.Fatalf("no catalog created for warehouse %q: %v", tt.want, ft.statements())
			}
		})
	}
}

// TestSweeper_DropsExpired: only expired ephemeral rows are listed, dropped
// and hard-deleted; permanent rows and live test rows stay.
func TestSweeper_DropsExpired(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	gdb := pool
	clusterModel := model.TrinoCluster{
		ID: model.UUID(repo.NewUUID()), Name: "sweeper-test", CoordinatorURL: "http://t",
	}
	if err := gdb.Create(&clusterModel).Error; err != nil {
		t.Fatal(err)
	}
	cluster := repo.PGUUID(string(clusterModel.ID))
	q := repo.New(gdb)
	ins := func(name string, ttl time.Duration) {
		if err := q.InsertEphemeralRegistryRow(ctx, db.InsertEphemeralRegistryRowParams{ClusterID: cluster, CatalogName: name, ConnectorName: "postgresql",
			Column4: []byte("{}"), Ttl: db.Interval{Microseconds: ttl.Microseconds(), Valid: true}}); err != nil {
			t.Fatal(err)
		}
	}
	ins("baleia_test_old", -time.Minute)
	ins("baleia_test_live", time.Minute)
	if err := gdb.Create(&model.TrinoCatalogRegistry{
		ClusterID: clusterModel.ID, CatalogName: "sales", ConnectorName: "iceberg",
		Properties: model.JSON(`{}`),
	}).Error; err != nil {
		t.Fatal(err)
	}
	ft := newFakeTrino(t)
	_, binder := trinoBackends(ft.srv.URL)
	n, err := binder.SweepEphemeral(ctx, q)
	if err != nil || n != 1 {
		t.Fatalf("swept %d, %v", n, err)
	}
	var names []string
	if err := gdb.Model(&model.TrinoCatalogRegistry{}).Order("catalog_name").Pluck("catalog_name", &names).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "baleia_test_live,sales" {
		t.Fatalf("rows left = %v", names)
	}
	if got := ft.statements(); len(got) != 1 || got[0] != "DROP CATALOG IF EXISTS baleia_test_old" {
		t.Fatalf("statements = %v", got)
	}
}
