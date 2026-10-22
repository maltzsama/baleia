package api

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

var (
	bSourceConn = pgtype.UUID{Bytes: [16]byte{0xC1}, Valid: true}
	bEngineConn = pgtype.UUID{Bytes: [16]byte{0xC2}, Valid: true}
	bCatalog    = pgtype.UUID{Bytes: [16]byte{0xCA}, Valid: true}
	bEngine     = pgtype.UUID{Bytes: [16]byte{0xE1}, Valid: true}
	bCluster    = pgtype.UUID{Bytes: [16]byte{0xCC}, Valid: true}
	bOther      = pgtype.UUID{Bytes: [16]byte{0xCD}, Valid: true} // another cluster
	bBinding    = pgtype.UUID{Bytes: [16]byte{0xB1}, Valid: true}
)

// bindingQuerier is a catalog on a PostgreSQL connection owned by ownerID, a
// Trino engine on a system-owned connection, and its bindings.
type bindingQuerier struct {
	lifecycleFake
	mu            sync.Mutex
	catalog       db.Catalog
	engineKind    string
	clusterOfEng  pgtype.UUID
	createErr     error
	updatedCats   []db.UpdateCatalogParams
	createdBinds  []db.CreateManagedBindingParams
	updatedBinds  []db.UpdateBindingParams
	deletedBinds  int
	registry      []string
	sourceCreated pgtype.UUID
}

func newBindingQuerier(t *testing.T) *bindingQuerier {
	owner, _ := uuidFromString(ownerID)
	cat := db.Catalog{
		ID: bCatalog, ConnectionID: bSourceConn, Name: "sales", Origin: "source", SourceRef: strp("sales"),
		TrinoCatalogName: strp("sales"), ClusterID: bCluster, Warehouse: strp("sales"), Connector: strp("postgresql"), Enabled: true,
	}
	return &bindingQuerier{catalog: cat, engineKind: "trino", clusterOfEng: bOther, sourceCreated: owner}
}

func (q *bindingQuerier) GetCatalogByID(_ context.Context, id pgtype.UUID) (db.Catalog, error) {
	if id != q.catalog.ID {
		return db.Catalog{}, pgx.ErrNoRows
	}
	return q.catalog, nil
}

func (q *bindingQuerier) GetCatalogConnectionByID(_ context.Context, id pgtype.UUID) (db.CatalogConnection, error) {
	switch id {
	case bSourceConn:
		return db.CatalogConnection{ID: id, Name: "pg", Type: "postgresql", CreatedBy: q.sourceCreated,
			Config: []byte(`{"host":"db.example","port":"5432","database":"sales","username":"u"}`)}, nil
	case bEngineConn:
		return db.CatalogConnection{ID: id, Name: "trino", Type: "trino", Config: []byte(`{"url":"http://trino:8080"}`)}, nil // system-owned
	}
	return db.CatalogConnection{}, pgx.ErrNoRows
}

func (q *bindingQuerier) CatalogGrantedToUser(context.Context, db.CatalogGrantedToUserParams) (bool, error) {
	return false, nil
}

func (q *bindingQuerier) GetEngineByID(_ context.Context, id pgtype.UUID) (db.Engine, error) {
	if id != bEngine {
		return db.Engine{}, pgx.ErrNoRows
	}
	return db.Engine{ID: id, Name: "default", Kind: q.engineKind, Class: "federated", ConnectionID: bEngineConn, Enabled: true}, nil
}

func (q *bindingQuerier) EngineHostsCatalogGrantedToUser(context.Context, db.EngineHostsCatalogGrantedToUserParams) (bool, error) {
	return false, nil
}

func (q *bindingQuerier) GetTrinoClusterByEngine(_ context.Context, id pgtype.UUID) (db.TrinoCluster, error) {
	return db.TrinoCluster{ID: q.clusterOfEng, Name: "c", EngineID: id}, nil
}

func (q *bindingQuerier) CreateManagedBinding(_ context.Context, p db.CreateManagedBindingParams) (db.CatalogBinding, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.createErr != nil {
		return db.CatalogBinding{}, q.createErr
	}
	q.createdBinds = append(q.createdBinds, p)
	return q.track(db.CatalogBinding{ID: bBinding, CatalogID: p.CatalogID, EngineID: p.EngineID, EngineCatalogName: p.EngineCatalogName, Connector: p.Connector,
		ExtraProps: p.ExtraProps, Mode: "managed", Enabled: true, LifecycleState: "provisioning", DesiredVersion: p.DesiredVersion}), nil
}

func (q *bindingQuerier) GetBinding(_ context.Context, id pgtype.UUID) (db.CatalogBinding, error) {
	if b, ok := q.current(id); ok {
		return b, nil
	}
	return db.CatalogBinding{}, pgx.ErrNoRows
}

func (q *bindingQuerier) UpdateBinding(_ context.Context, p db.UpdateBindingParams) (db.CatalogBinding, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.updatedBinds = append(q.updatedBinds, p)
	b, ok := q.current(p.ID)
	if !ok || !slices.Contains(p.FromStates, b.LifecycleState) {
		return db.CatalogBinding{}, pgx.ErrNoRows
	}
	b.EngineCatalogName, b.Connector, b.ExtraProps, b.Enabled, b.LifecycleState = p.EngineCatalogName, p.Connector, p.ExtraProps, p.Enabled, p.LifecycleState
	return q.track(b), nil
}

func (q *bindingQuerier) DeleteBinding(context.Context, pgtype.UUID) error {
	q.deletedBinds++
	return nil
}

func (q *bindingQuerier) UpdateCatalog(_ context.Context, p db.UpdateCatalogParams) (db.Catalog, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.updatedCats = append(q.updatedCats, p)
	return q.catalog, nil
}

func (q *bindingQuerier) SetCatalogEnabled(context.Context, db.SetCatalogEnabledParams) error {
	return nil
}

func (q *bindingQuerier) UpsertTrinoCatalogRegistry(_ context.Context, p db.UpsertTrinoCatalogRegistryParams) error {
	q.registry = append(q.registry, "upsert "+p.CatalogName)
	q.noteRegistry(p.CatalogName, p.CatalogVersion)
	return nil
}

func (q *bindingQuerier) SoftDeleteTrinoCatalogRegistry(_ context.Context, p db.SoftDeleteTrinoCatalogRegistryParams) error {
	q.registry = append(q.registry, "disable "+p.CatalogName)
	return nil
}

func bindingHandlers(t *testing.T, q *bindingQuerier) (*API, *fakeTrino) {
	t.Helper()
	ft := newFakeTrino(t)
	h := testAPI(handlerFields{queries: q, tx: directTx{q: q}, connections: testRegistry(t), settings: SettingsFromConfig(&config.Config{}), catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute)})
	h.engines, h.binder = trinoBackends(ft.srv.URL)
	return h, ft
}

func code(t *testing.T, visit func(*httptest.ResponseRecorder) error) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := visit(rec); err != nil {
		t.Fatal(err)
	}
	return rec.Code, rec.Body.String()
}

func createBinding(t *testing.T, h *API, ctx context.Context, body CreateBindingRequest) (int, string) { //nolint:revive // thelper wants t first in test helpers
	t.Helper()
	resp, err := h.CreateCatalogBinding(ctx, CreateCatalogBindingRequestObject{Id: openapi_types.UUID(bCatalog.Bytes), Body: &body})
	if err != nil {
		t.Fatal(err)
	}
	return code(t, func(r *httptest.ResponseRecorder) error { return resp.VisitCreateCatalogBindingResponse(r) })
}

// TestBindingAPI_Authz: binding needs write on the catalog's connection and
// on the engine's (G-admin for the system-owned Trino connection).
func TestBindingAPI_Authz(t *testing.T) {
	q := newBindingQuerier(t)
	h, ft := bindingHandlers(t, q)
	body := CreateBindingRequest{EngineId: openapi_types.UUID(bEngine.Bytes), EngineCatalogName: "sales_eu"}

	if c, _ := createBinding(t, h, ctxAs(strangerID, "member"), body); c != 404 {
		t.Errorf("stranger: %d", c)
	}
	if c, _ := createBinding(t, h, ctxAs(ownerID, "member"), body); c != 404 {
		t.Errorf("catalog owner on a system engine: %d (engine is not theirs)", c)
	}
	if len(q.createdBinds) != 0 || len(ft.statements()) != 0 {
		t.Fatal("a denied request changed state")
	}
	c, b := createBinding(t, h, ctxAs(adminID, "admin"), body)
	if c != 201 || !strings.Contains(b, `"sync_status":"synced"`) || !strings.Contains(b, `"engine_catalog_name":"sales_eu"`) {
		t.Fatalf("admin: %d %s", c, b)
	}
	if len(q.createdBinds) != 1 || *q.createdBinds[0].Connector != "postgresql" || len(q.updatedCats) != 0 {
		t.Errorf("binding = %+v, catalog updates = %d (not the primary binding)", q.createdBinds, len(q.updatedCats))
	}
	if stmts := ft.statements(); len(stmts) != 1 || !strings.HasPrefix(stmts[0], "CREATE CATALOG") || len(q.registry) != 1 {
		t.Errorf("trino = %q, registry = %v", stmts, q.registry)
	}
}

func TestBindingAPI_Rejections(t *testing.T) {
	q := newBindingQuerier(t)
	h, _ := bindingHandlers(t, q)
	admin := ctxAs(adminID, "admin")
	base := CreateBindingRequest{EngineId: openapi_types.UUID(bEngine.Bytes), EngineCatalogName: "x"}

	evil := base
	evil.ExtraProps = &map[string]string{"connection-url": "jdbc:postgresql://evil/x"}
	if c, b := createBinding(t, h, admin, evil); c != 422 || !strings.Contains(b, "property_not_overridable") {
		t.Errorf("protected override: %d %s", c, b)
	}
	reserved := base
	reserved.EngineCatalogName = "system"
	if c, b := createBinding(t, h, admin, reserved); c != 422 || !strings.Contains(b, "reserved_name") {
		t.Errorf("reserved name: %d %s", c, b)
	}
	iceberg := base
	iceberg.Connector = strp("iceberg")
	if c, b := createBinding(t, h, admin, iceberg); c != 422 || !strings.Contains(b, "binding_unsupported") {
		t.Errorf("wrong connector: %d %s", c, b)
	}
	for constraint, want := range map[string]string{
		"catalog_bindings_catalog_id_engine_id_key":          "binding_exists",
		"catalog_bindings_engine_id_engine_catalog_name_key": "name_in_use",
	} {
		q.createErr = &pgconn.PgError{Code: "23505", ConstraintName: constraint}
		if c, b := createBinding(t, h, admin, base); c != 409 || !strings.Contains(b, want) {
			t.Errorf("%s: %d %s", constraint, c, b)
		}
	}
	q.createErr = nil
	q.engineKind = "postgresql"
	if c, b := createBinding(t, h, admin, base); c != 422 || !strings.Contains(b, "binding_unsupported") {
		t.Errorf("direct engine: %d %s", c, b)
	}
	q.engineKind = "trino"
	q.catalog.Origin = "native"
	if c, b := createBinding(t, h, admin, base); c != 422 || !strings.Contains(b, "binding_unsupported") {
		t.Errorf("native catalog: %d %s", c, b)
	}
}

func TestBindingAPI_UpdateDeleteSync(t *testing.T) {
	q := newBindingQuerier(t)
	h, ft := bindingHandlers(t, q)
	admin := ctxAs(adminID, "admin")
	q.track(db.CatalogBinding{ID: bBinding, CatalogID: bCatalog, EngineID: bEngine, EngineCatalogName: "old", Connector: strp("postgresql"), ExtraProps: []byte("{}"), Mode: "managed", Enabled: true, LifecycleState: "active"})
	id := openapi_types.UUID(bBinding.Bytes)

	// Rename on a secondary binding: create the new name, then drop the old.
	resp, err := h.UpdateCatalogBinding(admin, UpdateCatalogBindingRequestObject{Id: id, Body: &UpdateBindingRequest{EngineCatalogName: strp("renamed")}})
	if err != nil {
		t.Fatal(err)
	}
	if c, b := code(t, func(r *httptest.ResponseRecorder) error { return resp.VisitUpdateCatalogBindingResponse(r) }); c != 200 || !strings.Contains(b, `"renamed"`) {
		t.Fatalf("rename: %d %s", c, b)
	}
	stmts := ft.statements()
	if len(stmts) != 2 || !strings.HasPrefix(stmts[0], "CREATE CATALOG") || !strings.HasPrefix(stmts[1], "DROP CATALOG") || len(q.updatedCats) != 0 {
		t.Errorf("rename statements = %q, catalog updates = %d", stmts, len(q.updatedCats))
	}

	// Sync re-applies.
	sresp, _ := h.SyncCatalogBinding(admin, SyncCatalogBindingRequestObject{Id: id})
	if c, b := code(t, func(r *httptest.ResponseRecorder) error { return sresp.VisitSyncCatalogBindingResponse(r) }); c != 200 || !strings.Contains(b, `"version"`) {
		t.Errorf("sync: %d %s", c, b)
	}

	// Delete a secondary binding.
	dresp, _ := h.DeleteCatalogBinding(admin, DeleteCatalogBindingRequestObject{Id: id})
	if c, _ := code(t, func(r *httptest.ResponseRecorder) error { return dresp.VisitDeleteCatalogBindingResponse(r) }); c != 204 || q.deletedBinds != 1 {
		t.Errorf("delete: %d deleted=%d", c, q.deletedBinds)
	}

	// The primary binding (on the catalog's own cluster) cannot be deleted and
	// keeps the catalog's Trino columns in step when it changes.
	q.clusterOfEng = bCluster
	q.track(db.CatalogBinding{ID: bBinding, CatalogID: bCatalog, EngineID: bEngine, EngineCatalogName: "renamed", Connector: strp("postgresql"), ExtraProps: []byte("{}"), Mode: "managed", Enabled: true, LifecycleState: "active"})
	dresp, _ = h.DeleteCatalogBinding(admin, DeleteCatalogBindingRequestObject{Id: id})
	if c, b := code(t, func(r *httptest.ResponseRecorder) error { return dresp.VisitDeleteCatalogBindingResponse(r) }); c != 409 || !strings.Contains(b, "binding_is_primary") {
		t.Errorf("delete primary: %d %s", c, b)
	}
	resp, _ = h.UpdateCatalogBinding(admin, UpdateCatalogBindingRequestObject{Id: id, Body: &UpdateBindingRequest{ExtraProps: &map[string]string{"case-insensitive-name-matching": "true"}}})
	if c, _ := code(t, func(r *httptest.ResponseRecorder) error { return resp.VisitUpdateCatalogBindingResponse(r) }); c != 200 || len(q.updatedCats) != 1 ||
		!strings.Contains(string(q.updatedCats[0].ExtraProps), "case-insensitive") {
		t.Errorf("primary update: %d catalog updates %+v", c, q.updatedCats)
	}

	// Native bindings are not Baleia's to change.
	nb, _ := q.current(bBinding)
	nb.Mode = "native"
	q.track(nb)
	resp, _ = h.UpdateCatalogBinding(admin, UpdateCatalogBindingRequestObject{Id: id, Body: &UpdateBindingRequest{Enabled: new(bool)}})
	if c, b := code(t, func(r *httptest.ResponseRecorder) error { return resp.VisitUpdateCatalogBindingResponse(r) }); c != 422 || !strings.Contains(b, "binding_not_managed") {
		t.Errorf("native binding: %d %s", c, b)
	}
	// A stranger learns nothing.
	nb.Mode = "managed"
	q.track(nb)
	resp, _ = h.UpdateCatalogBinding(ctxAs(strangerID, "member"), UpdateCatalogBindingRequestObject{Id: id, Body: &UpdateBindingRequest{}})
	if c, _ := code(t, func(r *httptest.ResponseRecorder) error { return resp.VisitUpdateCatalogBindingResponse(r) }); c != 404 {
		t.Errorf("stranger: %d", c)
	}
}

func TestEngineBindingSupport(t *testing.T) {
	q := newBindingQuerier(t)
	h, _ := bindingHandlers(t, q)
	resp, err := h.GetEngineBindingSupport(ctxAs(adminID, "admin"), GetEngineBindingSupportRequestObject{Id: openapi_types.UUID(bEngine.Bytes)})
	if err != nil {
		t.Fatal(err)
	}
	support, ok := resp.(GetEngineBindingSupport200JSONResponse)
	if !ok {
		t.Fatalf("response = %T", resp)
	}
	pg := support["postgresql"]
	if len(support) != 12 || len(pg.Connectors) != 1 || pg.Connectors[0] != "postgresql" || len(pg.ProtectedKeyPatterns) == 0 || len(pg.ReservedNames) == 0 {
		t.Errorf("support = %+v", support)
	}
	q.engineKind = "postgresql"
	resp, _ = h.GetEngineBindingSupport(ctxAs(adminID, "admin"), GetEngineBindingSupportRequestObject{Id: openapi_types.UUID(bEngine.Bytes)})
	if s := resp.(GetEngineBindingSupport200JSONResponse); len(s) != 0 {
		t.Errorf("a direct engine binds nothing: %v", s)
	}
}
