package api

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/icebergrest"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
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
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
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
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
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

func runDraft(t *testing.T, h *API, user string, body *TestConnectionRequest) TestConnectionDraftResponseObject {
	t.Helper()
	resp, err := h.TestConnectionDraft(ctxAs(user, "member"), TestConnectionDraftRequestObject{Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func freshLimiter(t *testing.T) {
	before := connectionTests
	connectionTests = &testLimiter{windows: map[string]testWindow{}}
	t.Cleanup(func() { connectionTests = before })
}

// TestTransient_RESTPersistsNothing: a draft test of an Iceberg REST
// catalog runs its checks and writes nothing (draftQuerier fails on any
// write).
func TestTransient_RESTPersistsNothing(t *testing.T) {
	freshLimiter(t)
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
	freshLimiter(t)
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

// TestTransient_DeniedCIDR: a host on the cloud metadata address is refused
// by the dialer before any connection.
func TestTransient_DeniedCIDR(t *testing.T) {
	freshLimiter(t)
	r := runDraft(t, draftHandlers(t, testRegistry(t)), ownerID, draft("http://169.254.169.254/catalog")).(TestConnectionDraft200JSONResponse)
	if r.Success || *r.ErrorCode != codeDeniedNetwork {
		t.Fatalf("result = %+v", r)
	}
}

// TestTransient_RateLimited: the 11th test of a user in a minute is a 429;
// another user is not affected.
func TestTransient_RateLimited(t *testing.T) {
	freshLimiter(t)
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

// TestTransient_IntentsFilter: only the requested checks run; a bind on an
// engine the caller may not use is a 404; an unknown intent a 422.
func TestTransient_IntentsFilter(t *testing.T) {
	freshLimiter(t)
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
	if _, ok := runDraft(t, h2, strangerID, draft(srv.URL+"/catalog", "bind:"+uuidString(pgtype.UUID{Bytes: [16]byte{0xE1}, Valid: true}))).(TestConnectionDraft404JSONResponse); !ok {
		t.Fatal("binding into an engine the caller may not use is a 404")
	}
}

// TestTransient_DirectEngineNotRegistered: a kind without an engine adapter
// yet skips the direct check; nothing is registered.
func TestTransient_DirectEngineNotRegistered(t *testing.T) {
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
	h, q, ft := newAuthzHandlers(t)
	ef := &ephemeralFake{authzFake: q}
	h.queries = ef
	reg, _ := loopbackRegistry(t)
	p, _ := reg.Get(connections.KindPostgreSQL)
	cfg, err := p.Validate(connections.Config{Kind: connections.KindPostgreSQL,
		Values: map[string]string{"host": "db", "database": "sales", "username": "u"}, Secrets: map[string]string{"password": "p"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the client went away
	err = connectionTest{h: h.CatalogHandlers, provider: p, cfg: cfg}.bind(ctx, testBind{
		engine:  db.Engine{ID: pgtype.UUID{Bytes: [16]byte{0xE1}, Valid: true}, Name: "trino"},
		cluster: db.TrinoCluster{ID: pgtype.UUID{Bytes: [16]byte{0xCC}, Valid: true}},
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

// TestSweeper_DropsExpired: only expired ephemeral rows are listed, dropped
// and hard-deleted; permanent rows and live test rows stay.
func TestSweeper_DropsExpired(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var cluster pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO trino_clusters (name, coordinator_url) VALUES ('sweeper-test', 'http://t') RETURNING id`).Scan(&cluster); err != nil {
		t.Fatal(err)
	}
	q := db.New(pool)
	ins := func(name string, ttl time.Duration) {
		if err := q.InsertEphemeralRegistryRow(ctx, db.InsertEphemeralRegistryRowParams{ClusterID: cluster, CatalogName: name, ConnectorName: "postgresql",
			Column4: []byte("{}"), Ttl: pgtype.Interval{Microseconds: ttl.Microseconds(), Valid: true}}); err != nil {
			t.Fatal(err)
		}
	}
	ins("baleia_test_old", -time.Minute)
	ins("baleia_test_live", time.Minute)
	if _, err := pool.Exec(ctx, `INSERT INTO trino_catalog_registry (cluster_id, catalog_name, connector_name, properties) VALUES ($1, 'sales', 'iceberg', '{}')`, cluster); err != nil {
		t.Fatal(err)
	}
	ft := newFakeTrino(t)
	_, binder := trinoBackends(ft.srv.URL)
	n, err := binder.SweepEphemeral(ctx, q)
	if err != nil || n != 1 {
		t.Fatalf("swept %d, %v", n, err)
	}
	var names []string
	rows, _ := pool.Query(ctx, `SELECT catalog_name FROM trino_catalog_registry ORDER BY 1`)
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		names = append(names, s)
	}
	if strings.Join(names, ",") != "baleia_test_live,sales" {
		t.Fatalf("rows left = %v", names)
	}
	if got := ft.statements(); len(got) != 1 || got[0] != "DROP CATALOG IF EXISTS baleia_test_old" {
		t.Fatalf("statements = %v", got)
	}
}
