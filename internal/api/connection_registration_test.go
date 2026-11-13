package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ── direct registration ────────────────────────────────────────────────────

// directFake is a direct-engine provider that declares the direct access mode.
type directFake struct{ fakeEngineProvider }

func (*directFake) Meta() connections.TypeMeta {
	return connections.TypeMeta{AccessModes: []connections.AccessMode{connections.AccessModeDirect}}
}

// registrationFake is an in-memory store whose transaction either commits what
// it staged or discards it, so a test sees what a rollback leaves behind.
type registrationFake struct {
	db.Querier
	mu        sync.Mutex
	staged    []string
	committed []string
	txs       int
	// failOn names the operation that fails ("connection", "engine", "native",
	// "binding"), with failErr.
	failOn  string
	failErr error
}

func (f *registrationFake) InTx(_ context.Context, fn func(db.Querier) error) error {
	f.mu.Lock()
	f.txs++
	f.mu.Unlock()
	err := fn(f)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		f.committed = append(f.committed, f.staged...)
	}
	f.staged = nil
	return err
}

func (f *registrationFake) record(op, what string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn == op {
		return f.failErr
	}
	f.staged = append(f.staged, op+":"+what)
	return nil
}

func (f *registrationFake) CreateCatalogConnection(_ context.Context, p db.CreateCatalogConnectionParams) (db.CatalogConnection, error) {
	if err := f.record("connection", p.Name); err != nil {
		return db.CatalogConnection{}, err
	}
	return db.CatalogConnection{ID: p.ID, Name: p.Name, Type: p.Type, AccessMode: p.AccessMode, Url: p.Url, Config: p.Config, CreatedBy: p.CreatedBy}, nil
}

func (f *registrationFake) CreateEngine(_ context.Context, p db.CreateEngineParams) (db.Engine, error) {
	if err := f.record("engine", p.Name); err != nil {
		return db.Engine{}, err
	}
	return db.Engine{ID: db.UUID{Bytes: uuid.New(), Valid: true}, Name: p.Name, Kind: p.Kind, Class: p.Class, ConnectionID: p.ConnectionID, Enabled: true}, nil
}

func (f *registrationFake) UpsertNativeCatalog(_ context.Context, p db.UpsertNativeCatalogParams) (db.Catalog, error) {
	if err := f.record("native", p.Name); err != nil {
		return db.Catalog{}, err
	}
	return db.Catalog{ID: db.UUID{Bytes: uuid.New(), Valid: true}, ConnectionID: p.ConnectionID, Name: p.Name, Origin: "native", Enabled: true}, nil
}

func (f *registrationFake) UpsertNativeBinding(_ context.Context, p db.UpsertNativeBindingParams) (db.CatalogBinding, error) {
	if err := f.record("binding", p.EngineCatalogName); err != nil {
		return db.CatalogBinding{}, err
	}
	return db.CatalogBinding{}, nil
}

func (f *registrationFake) ListCatalogsByConnection(context.Context, db.UUID) ([]db.Catalog, error) {
	return nil, nil
}

func (f *registrationFake) GetEngineByID(context.Context, db.UUID) (db.Engine, error) {
	return db.Engine{}, db.ErrNoRows
}

func (f *registrationFake) GetCatalogConnectionByID(context.Context, db.UUID) (db.CatalogConnection, error) {
	return db.CatalogConnection{}, db.ErrNoRows
}

func (f *registrationFake) GetCatalogByID(context.Context, db.UUID) (db.Catalog, error) {
	return db.Catalog{}, db.ErrNoRows
}

// directRegistration is a handler over a fake provider whose source is
// reachable and lists two catalogs.
func directRegistration(t *testing.T, ping error) (*API, *registrationFake) {
	t.Helper()
	f := &registrationFake{}
	p := &directFake{fakeEngineProvider{kind: "clickhouse", roles: []connections.Role{connections.RoleMetadata, connections.RoleDirect}, catalogs: []string{"default", "logs"}, pingErr: ping}}
	reg, err := connections.NewRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	h := testAPI(handlerFields{
		queries: f, tx: f, keyring: testKeyring(t), connections: reg,
		settings:     SettingsFromConfig(&config.Config{}),
		catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute),
	})
	return h, f
}

func directBody(name string) *CreateCatalogConnectionRequest {
	return &CreateCatalogConnectionRequest{Name: name, Type: "clickhouse", Config: strMap("url", "clickhouse://ch:9000")}
}

func registerConnection(t *testing.T, h *API, body *CreateCatalogConnectionRequest) (CreateCatalogConnectionResponseObject, error) {
	t.Helper()
	return h.CreateCatalogConnection(ctxAs(adminID, "admin"), CreateCatalogConnectionRequestObject{Body: body})
}

func TestRegisterDirect_CreatesTheConnectionAndItsEngineInOneTransaction(t *testing.T) {
	t.Parallel()
	h, f := directRegistration(t, nil)
	resp, err := registerConnection(t, h, directBody("Sales DB"))
	if err != nil {
		t.Fatal(err)
	}
	created, ok := resp.(CreateCatalogConnection201JSONResponse)
	if !ok {
		t.Fatalf("response = %d", visitCreate(t, resp).Code)
	}
	if created.Engine == nil || created.Engine.Name != "sales_db" || created.Engine.Class != "direct" {
		t.Fatalf("engine = %+v, want the native engine named after the connection", created.Engine)
	}
	if created.AccessMode == nil || *created.AccessMode != CatalogConnectionCreatedAccessModeDirect {
		t.Fatalf("access_mode = %v", created.AccessMode)
	}
	want := []string{"connection:Sales DB", "engine:sales_db", "native:default", "binding:default", "native:logs", "binding:logs"}
	if strings.Join(f.committed, ",") != strings.Join(want, ",") || f.txs != 1 {
		t.Fatalf("committed %v in %d transactions, want %v in one", f.committed, f.txs, want)
	}
}

// A failure at any write leaves nothing: the connection is part of the same
// transaction as the engine, its catalogs and their bindings.
func TestRegisterDirect_RollsBackEveryStage(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	for _, stage := range []string{"connection", "engine", "native", "binding"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			h, f := directRegistration(t, nil)
			f.failOn, f.failErr = stage, boom
			resp, err := registerConnection(t, h, directBody("pg"))
			if !errors.Is(err, boom) || resp != nil {
				t.Fatalf("response %v, error %v; want the write failure", resp, err)
			}
			if len(f.committed) != 0 {
				t.Fatalf("a failed registration committed %v", f.committed)
			}
		})
	}
}

func TestRegisterDirect_NameConflictsAre409AndLeaveNothing(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ stage, constraint, want string }{
		"connection name": {"connection", "catalog_connections_name_key", "a connection with this name already exists"},
		"engine name":     {"engine", "engines_name_key", "an engine with this name already exists"},
		"engine taken":    {"engine", "engines_connection_id_key", "this connection already backs an engine"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, f := directRegistration(t, nil)
			f.failOn, f.failErr = c.stage, &db.PGError{Code: "23505", ConstraintName: c.constraint}
			resp, err := registerConnection(t, h, directBody("pg"))
			if err != nil {
				t.Fatal(err)
			}
			if rec := visitCreate(t, resp); rec.Code != 409 || !strings.Contains(rec.Body.String(), c.want) {
				t.Fatalf("response = %d %s, want 409 %q", rec.Code, rec.Body, c.want)
			}
			if len(f.committed) != 0 {
				t.Fatalf("a conflicting registration committed %v", f.committed)
			}
		})
	}
}

// Reachability is checked before a transaction opens.
func TestRegisterDirect_UnreachableSourceOpensNoTransaction(t *testing.T) {
	t.Parallel()
	h, f := directRegistration(t, errors.New("connection refused"))
	resp, err := registerConnection(t, h, directBody("pg"))
	if err != nil {
		t.Fatal(err)
	}
	if rec := visitCreate(t, resp); rec.Code != 422 || !strings.Contains(rec.Body.String(), "engine_unreachable") {
		t.Fatalf("response = %d %s, want 422 engine_unreachable", rec.Code, rec.Body)
	}
	if f.txs != 0 {
		t.Fatalf("%d transactions opened for an unreachable source", f.txs)
	}
}

func TestRegisterDirect_RefusesWhatItDoesNotTake(t *testing.T) {
	t.Parallel()
	engine := openapi_types.UUID{0xE1}
	withEngines := directBody("pg")
	withEngines.EngineIds = &[]openapi_types.UUID{engine}
	withCatalogs := directBody("pg")
	withCatalogs.CatalogRegistration = catalogRequest("a", "a")
	cases := map[string]struct {
		body *CreateCatalogConnectionRequest
		want string
	}{
		"engine ids":           {withEngines, `"access_mode"`},
		"catalog registration": {withCatalogs, `"access_mode"`},
		"unusable name":        {directBody("!!!"), "cannot be turned into an engine name"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, f := directRegistration(t, nil)
			resp, err := registerConnection(t, h, c.body)
			if err != nil {
				t.Fatal(err)
			}
			if rec := visitCreate(t, resp); rec.Code != 422 || !strings.Contains(rec.Body.String(), c.want) {
				t.Fatalf("response = %d %s, want 422 %q", rec.Code, rec.Body, c.want)
			}
			if f.txs != 0 {
				t.Fatal("a refused request opened a transaction")
			}
		})
	}
}

// ── federated registration ─────────────────────────────────────────────────

func federatedBody(engines ...[16]byte) *CreateCatalogConnectionRequest {
	body := validCreateRequests()["polaris"]
	ids := make([]openapi_types.UUID, 0, len(engines))
	for _, e := range engines {
		ids = append(ids, openapi_types.UUID(e))
	}
	body.EngineIds = &ids
	reg := catalogRequest("wh", "wh")
	wh := "wh"
	reg.Warehouse = &wh
	body.CatalogRegistration = reg
	return body
}

var (
	engineOne = [16]byte{0xE1}
	engineTwo = [16]byte{0xE2}
)

func TestRegisterFederated_PersistsTheDesiredStateAndAppliesIt(t *testing.T) {
	t.Parallel()
	h, q, ft := newAuthzHandlers(t)
	resp, err := registerConnection(t, h, federatedBody(engineOne))
	if err != nil {
		t.Fatal(err)
	}
	created, ok := resp.(CreateCatalogConnection201JSONResponse)
	if !ok {
		t.Fatalf("response = %d %s", visitCreate(t, resp).Code, visitCreate(t, resp).Body)
	}
	if created.Engine != nil || len(created.Catalogs) != 1 {
		t.Fatalf("engine %+v, catalogs %d; a federated connection owns no engine", created.Engine, len(created.Catalogs))
	}
	if len(q.createdConns) != 1 || q.createdConns[0].AccessMode == nil || *q.createdConns[0].AccessMode != "federated" {
		t.Fatalf("connection rows = %+v", q.createdConns)
	}
	if len(q.createdCatalogs) != 1 || len(q.bindingUpserts) != 1 || q.bindingUpserts[0].LifecycleState != "provisioning" {
		t.Fatalf("catalogs %d, binding upserts %+v; want the binding written as provisioning", len(q.createdCatalogs), q.bindingUpserts)
	}
	if got := strings.Join(ft.statements(), ";"); !strings.Contains(got, "CREATE CATALOG") {
		t.Fatalf("the engine was not asked to create the catalog: %q", got)
	}
}

// The commit is not an activation: when the engine refuses the catalog the
// request still succeeds, the rows stay, and the response says it failed.
func TestRegisterFederated_ActivationFailureKeepsTheCommittedState(t *testing.T) {
	t.Parallel()
	h, q, _ := newAuthzHandlers(t)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.HasPrefix(string(b), "CREATE CATALOG") {
			_, _ = io.WriteString(w, `{"id":"q","error":{"message":"boom","errorName":"GENERIC_INTERNAL_ERROR"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"q","stats":{"state":"FINISHED"}}`)
	}))
	defer failing.Close()
	h.engines, h.binder = trinoBackends(failing.URL)

	resp, err := registerConnection(t, h, federatedBody(engineOne))
	if err != nil {
		t.Fatal(err)
	}
	created, ok := resp.(CreateCatalogConnection201JSONResponse)
	if !ok {
		t.Fatalf("response = %d", visitCreate(t, resp).Code)
	}
	if len(q.createdConns) != 1 || len(q.createdCatalogs) != 1 {
		t.Fatalf("connections %d, catalogs %d; the committed state must stay", len(q.createdConns), len(q.createdCatalogs))
	}
	if st := created.Catalogs[0].SyncStatus; st == nil || *st != CatalogSyncStatusError {
		t.Fatalf("sync_status = %v, want error", st)
	}
}

func TestRegisterFederated_ExposesTheCatalogsThroughEveryEngine(t *testing.T) {
	t.Parallel()
	h, q, _ := newAuthzHandlers(t)
	resp, err := registerConnection(t, h, federatedBody(engineOne, engineTwo))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(CreateCatalogConnection201JSONResponse); !ok {
		t.Fatalf("response = %d %s", visitCreate(t, resp).Code, visitCreate(t, resp).Body)
	}
	if len(q.bindingUpserts) != 1 || len(q.secondary) != 1 || q.secondary[0].EngineID.Bytes != engineTwo {
		t.Fatalf("primary %+v, secondary %+v; want one binding per engine", q.bindingUpserts, q.secondary)
	}
}

func TestRegisterFederated_Refusals(t *testing.T) {
	t.Parallel()
	noEngines := federatedBody()
	noEngines.EngineIds = nil
	noRegistration := federatedBody(engineOne)
	noRegistration.CatalogRegistration = nil
	twice := federatedBody(engineOne, engineOne)
	wrongCount := federatedBody(engineOne)
	wrongCount.CatalogRegistration = catalogRequest("a", "a", "b", "b")
	cases := map[string]struct {
		body *CreateCatalogConnectionRequest
		want string
	}{
		"no engines":             {noEngines, `"engine_ids"`},
		"no catalogs":            {noRegistration, `"catalog_registration"`},
		"an engine listed twice": {twice, "listed twice"},
		"wrong output count":     {wrongCount, "exposes 1 catalog outputs"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, q, _ := newAuthzHandlers(t)
			resp, err := registerConnection(t, h, c.body)
			if err != nil {
				t.Fatal(err)
			}
			if rec := visitCreate(t, resp); rec.Code != 422 || !strings.Contains(rec.Body.String(), c.want) {
				t.Fatalf("response = %d %s, want 422 %q", rec.Code, rec.Body, c.want)
			}
			if len(q.createdConns) != 0 || len(q.createdCatalogs) != 0 {
				t.Fatal("a refused registration wrote rows")
			}
		})
	}
}

func TestRegisterOnly_RefusesEnginesAndCatalogs(t *testing.T) {
	t.Parallel()
	h := testAPI(handlerFields{queries: &recordingConnQuerier{}, tx: directTx{q: &recordingConnQuerier{}}, keyring: testKeyring(t), connections: testRegistry(t), settings: SettingsFromConfig(&config.Config{}), catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute)})
	body := &CreateCatalogConnectionRequest{Name: "t", Type: "trino", Config: strMap("url", "http://trino:8080"), EngineIds: &[]openapi_types.UUID{{0xE1}}}
	resp, err := registerConnection(t, h, body)
	if err != nil {
		t.Fatal(err)
	}
	if rec := visitCreate(t, resp); rec.Code != 422 {
		t.Fatalf("response = %d %s, want 422", rec.Code, rec.Body)
	}
}

func TestPlanCatalogOutputs_ProvisionsHiveLast(t *testing.T) {
	t.Parallel()
	body := *catalogRequest("a", "a", "b", "b", "c", "c")
	got, invalid := planCatalogOutputs([]string{"iceberg", "hive", "delta_lake"}, body)
	if invalid != nil {
		t.Fatalf("refused: %+v", invalid)
	}
	order := []string{got[0].connector, got[1].connector, got[2].connector}
	if strings.Join(order, ",") != "iceberg,delta_lake,hive" {
		t.Fatalf("order = %v", order)
	}
}

// ── authzFake: what a registration writes ──────────────────────────────────

func (q *authzFake) CreateCatalogConnection(_ context.Context, p db.CreateCatalogConnectionParams) (db.CatalogConnection, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.createdConns = append(q.createdConns, p)
	return db.CatalogConnection{ID: p.ID, Name: p.Name, Type: p.Type, AccessMode: p.AccessMode, Url: p.Url, Config: p.Config, CredentialsEnc: p.CredentialsEnc, CreatedBy: p.CreatedBy}, nil
}

func (q *authzFake) CreateManagedBinding(_ context.Context, p db.CreateManagedBindingParams) (db.CatalogBinding, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.secondary = append(q.secondary, p)
	return q.track(db.CatalogBinding{
		ID: db.UUID{Bytes: uuid.New(), Valid: true}, CatalogID: p.CatalogID, EngineID: p.EngineID, EngineCatalogName: p.EngineCatalogName,
		Connector: p.Connector, Mode: "managed", Enabled: true, LifecycleState: "provisioning", DesiredVersion: p.DesiredVersion,
	}), nil
}
