package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/catalogs/conns"
	"github.com/galileostudio/baleia/internal/catalogs/topology"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	wsaccess "github.com/galileostudio/baleia/internal/workspaces/access"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// fakeEngineProvider is a direct-engine provider whose metadata reader lists
// a fixed set of catalogs.
type fakeEngineProvider struct {
	kind     connections.Kind
	roles    []connections.Role
	catalogs []string
	pingErr  error
}

func (f *fakeEngineProvider) Kind() connections.Kind     { return f.kind }
func (f *fakeEngineProvider) Label() string              { return string(f.kind) }
func (f *fakeEngineProvider) Family() connections.Family { return connections.FamilyDatabase }
func (f *fakeEngineProvider) Roles() []connections.Role  { return f.roles }
func (f *fakeEngineProvider) ConfigSchema() []connections.Field {
	return []connections.Field{{Key: "url", Type: connections.FieldText}, {Key: "database", Type: connections.FieldText}}
}
func (f *fakeEngineProvider) Validate(c connections.Config) (connections.Config, error) {
	return c, nil
}
func (f *fakeEngineProvider) EngineKind() string                             { return string(f.kind) }
func (f *fakeEngineProvider) Ping(context.Context, connections.Config) error { return f.pingErr }
func (f *fakeEngineProvider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}
func (f *fakeEngineProvider) OpenMetadata(context.Context, connections.Config) (connections.MetadataReader, error) {
	return fakeReader{names: f.catalogs}, nil
}

type fakeReader struct{ names []string }

func (r fakeReader) ListCatalogs(context.Context) ([]connections.CatalogRef, error) {
	out := []connections.CatalogRef{}
	for _, n := range r.names {
		out = append(out, connections.CatalogRef{Name: n})
	}
	return out, nil
}
func (fakeReader) ListNamespaces(context.Context, string, connections.Namespace) ([]connections.Namespace, error) {
	return nil, nil
}
func (fakeReader) ListTables(context.Context, string, connections.Namespace, string) ([]connections.TableRef, string, error) {
	return nil, "", nil
}
func (fakeReader) LoadTable(context.Context, connections.TableRef) (*connections.TableMeta, error) {
	return nil, nil
}
func (fakeReader) Close() error { return nil }

type engineQuerier struct {
	db.Querier
	conn       db.CatalogConnection
	engine     db.Engine
	granted    bool
	blockers   db.CountEngineBlockersRow
	created    []db.CreateEngineParams
	natives    []db.UpsertNativeCatalogParams
	bindings   []db.UpsertNativeBindingParams
	deletedNat int
	deletedEng int
}

func (q *engineQuerier) GetCatalogConnectionByID(context.Context, pgtype.UUID) (db.CatalogConnection, error) {
	return q.conn, nil
}
func (q *engineQuerier) GetEngineByID(_ context.Context, id pgtype.UUID) (db.Engine, error) {
	if !q.engine.ID.Valid || q.engine.ID != id {
		return db.Engine{}, pgx.ErrNoRows
	}
	return q.engine, nil
}
func (q *engineQuerier) EngineHostsCatalogGrantedToUser(context.Context, db.EngineHostsCatalogGrantedToUserParams) (bool, error) {
	return q.granted, nil
}
func (q *engineQuerier) CreateEngine(_ context.Context, p db.CreateEngineParams) (db.Engine, error) {
	q.created = append(q.created, p)
	return db.Engine{ID: pgtype.UUID{Bytes: [16]byte{0xE1}, Valid: true}, Name: p.Name, Kind: p.Kind, Class: p.Class, ConnectionID: p.ConnectionID, IdentityMode: p.IdentityMode, Enabled: true}, nil
}
func (q *engineQuerier) UpsertNativeCatalog(_ context.Context, p db.UpsertNativeCatalogParams) (db.Catalog, error) {
	q.natives = append(q.natives, p)
	return db.Catalog{ID: pgtype.UUID{Bytes: [16]byte{byte(len(q.natives))}, Valid: true}, ConnectionID: p.ConnectionID, Name: p.Name, Origin: "native"}, nil
}
func (q *engineQuerier) UpsertNativeBinding(_ context.Context, p db.UpsertNativeBindingParams) (db.CatalogBinding, error) {
	q.bindings = append(q.bindings, p)
	return db.CatalogBinding{}, nil
}
func (q *engineQuerier) CountEngineBlockers(context.Context, pgtype.UUID) (db.CountEngineBlockersRow, error) {
	return q.blockers, nil
}
func (q *engineQuerier) DeleteNativeCatalogsOfEngine(context.Context, pgtype.UUID) error {
	q.deletedNat++
	return nil
}
func (q *engineQuerier) DeleteEngine(context.Context, pgtype.UUID) error {
	q.deletedEng++
	return nil
}

func engineHandlers(t *testing.T, q *engineQuerier, providers ...connections.Provider) *API {
	t.Helper()
	reg, err := connections.NewRegistry(providers...)
	if err != nil {
		t.Fatal(err)
	}
	return testAPI(handlerFields{dialects: dialect.NewRegistry(), queries: q, tx: directTx{q: q}, connections: reg, catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute)})
}

func ownedConnection(t *testing.T, typ string, owner string) db.CatalogConnection {
	return db.CatalogConnection{
		ID: pgtype.UUID{Bytes: [16]byte{0xC1}, Valid: true}, Name: "warehouse-db", Type: typ,
		Config: []byte(`{"database":"sales"}`), CreatedBy: ownerUUID(t, owner),
	}
}

func createEngine(t *testing.T, h *API, name string) *httptest.ResponseRecorder {
	t.Helper()
	resp, err := h.CreateEngine(testContext(), CreateEngineRequestObject{Body: &CreateEngineRequest{Name: name, ConnectionId: openapi_types.UUID{0xC1}}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitCreateEngineResponse(rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestCreateDirectEngine_DiscoversNativeCatalogs(t *testing.T) {
	t.Parallel()
	q := &engineQuerier{conn: ownedConnection(t, "clickhouse", testUserID)}
	p := &fakeEngineProvider{kind: "clickhouse", roles: []connections.Role{connections.RoleMetadata, connections.RoleDirect}, catalogs: []string{"default", "logs"}}
	rec := createEngine(t, engineHandlers(t, q, p), "ch")
	if rec.Code != 201 {
		t.Fatalf("response = %d %s", rec.Code, rec.Body)
	}
	if len(q.created) != 1 || q.created[0].Class != "direct" || q.created[0].Kind != "clickhouse" || q.created[0].IdentityMode != "connection" || !q.created[0].CreatedBy.Valid {
		t.Fatalf("engine = %+v", q.created)
	}
	if len(q.natives) != 2 || q.natives[0].Name != "default" || q.natives[1].Name != "logs" || q.natives[0].ConnectionID != q.conn.ID {
		t.Fatalf("native catalogs = %+v", q.natives)
	}
	if len(q.bindings) != 2 || q.bindings[1].EngineCatalogName != "logs" || !q.bindings[1].EngineID.Valid {
		t.Fatalf("native bindings = %+v", q.bindings)
	}
}

func TestCreateDirectEngine_NativeCatalogPerKind(t *testing.T) {
	t.Parallel()
	cases := map[connections.Kind]string{
		connections.KindPostgreSQL: "sales",        // the configured database
		connections.KindMySQL:      "warehouse-db", // one catalog named after the connection
	}
	for kind, want := range cases {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			q := &engineQuerier{conn: ownedConnection(t, string(kind), testUserID)}
			p := &fakeEngineProvider{kind: kind, roles: []connections.Role{connections.RoleDirect}, catalogs: []string{"ignored"}}
			if rec := createEngine(t, engineHandlers(t, q, p), "e"); rec.Code != 201 {
				t.Fatalf("response = %d %s", rec.Code, rec.Body)
			}
			if len(q.natives) != 1 || q.natives[0].Name != want {
				t.Fatalf("native catalogs = %+v, want %q", q.natives, want)
			}
		})
	}
}

func TestCreateEngine_Rejects(t *testing.T) {
	t.Parallel()
	direct := []connections.Role{connections.RoleDirect}
	cases := map[string]struct {
		conn db.CatalogConnection
		p    *fakeEngineProvider
		name string
		code int
		want string
	}{
		"not an engine kind": {ownedConnection(t, "polaris", testUserID), &fakeEngineProvider{kind: "polaris", roles: []connections.Role{connections.RoleMetadata}}, "e", 422, "engine_unsupported"},
		"no provider":        {ownedConnection(t, "glue", testUserID), &fakeEngineProvider{kind: "x", roles: direct}, "e", 422, "engine_unsupported"},
		"unreachable":        {ownedConnection(t, "clickhouse", testUserID), &fakeEngineProvider{kind: "clickhouse", roles: direct, pingErr: connections.ErrDeniedNetwork}, "e", 422, "connection_denied_network"},
		"bad name":           {ownedConnection(t, "clickhouse", testUserID), &fakeEngineProvider{kind: "clickhouse", roles: direct}, "Bad Name", 422, "validation_error"},
		"foreign connection": {ownedConnection(t, "clickhouse", "00000000-0000-0000-0000-00000000beef"), &fakeEngineProvider{kind: "clickhouse", roles: direct}, "e", 404, "not_found"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			q := &engineQuerier{conn: c.conn}
			rec := createEngine(t, engineHandlers(t, q, c.p), c.name)
			if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) {
				t.Fatalf("response = %d %s, want %d with %q", rec.Code, rec.Body, c.code, c.want)
			}
			if len(q.created) != 0 {
				t.Fatal("an engine was created")
			}
		})
	}
}

func engineFixture(t *testing.T, owner string) *engineQuerier {
	conn := ownedConnection(t, "clickhouse", owner)
	return &engineQuerier{conn: conn, engine: db.Engine{ID: pgtype.UUID{Bytes: [16]byte{0xE1}, Valid: true}, Name: "ch", ConnectionID: conn.ID}}
}

func deleteEngine(t *testing.T, h *API) *httptest.ResponseRecorder {
	t.Helper()
	resp, err := h.DeleteEngine(testContext(), DeleteEngineRequestObject{Id: [16]byte{0xE1}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitDeleteEngineResponse(rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestDeleteEngineWithBindings409(t *testing.T) {
	t.Parallel()
	for name, blockers := range map[string]db.CountEngineBlockersRow{
		"managed bindings":   {ManagedBindings: 1},
		"running executions": {RunningExecutions: 2},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			q := engineFixture(t, testUserID)
			q.blockers = blockers
			rec := deleteEngine(t, engineHandlers(t, q))
			if rec.Code != 409 || !strings.Contains(rec.Body.String(), "engine_in_use") {
				t.Fatalf("response = %d %s", rec.Code, rec.Body)
			}
			if q.deletedEng != 0 {
				t.Fatal("engine deleted")
			}
		})
	}
}

func TestDeleteEngine_RemovesNativeCatalogs(t *testing.T) {
	t.Parallel()
	q := engineFixture(t, testUserID)
	if rec := deleteEngine(t, engineHandlers(t, q)); rec.Code != 204 {
		t.Fatalf("response = %d %s", rec.Code, rec.Body)
	}
	if q.deletedNat != 1 || q.deletedEng != 1 {
		t.Fatalf("deleted natives=%d engine=%d", q.deletedNat, q.deletedEng)
	}
}

func TestEngineAccess(t *testing.T) {
	t.Parallel()
	stranger := "00000000-0000-0000-0000-00000000beef"

	t.Run("a stranger gets 404", func(t *testing.T) {
		t.Parallel()
		q := engineFixture(t, stranger)
		resp, err := engineHandlers(t, q).GetEngine(testContext(), GetEngineRequestObject{Id: [16]byte{0xE1}})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := resp.(GetEngine404JSONResponse); !ok {
			t.Fatalf("response = %T", resp)
		}
	})

	t.Run("a grantee reads but may not write", func(t *testing.T) {
		t.Parallel()
		q := engineFixture(t, stranger)
		q.granted = true
		h := engineHandlers(t, q)
		resp, err := h.GetEngine(testContext(), GetEngineRequestObject{Id: [16]byte{0xE1}})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := resp.(GetEngine200JSONResponse); !ok {
			t.Fatalf("get = %T", resp)
		}
		if rec := deleteEngine(t, h); rec.Code != 403 {
			t.Fatalf("delete = %d %s", rec.Code, rec.Body)
		}
		if q.deletedEng != 0 {
			t.Fatal("a grantee deleted the engine")
		}
	})
}

func TestNativeCatalogsReaderError(t *testing.T) {
	t.Parallel()
	p := &fakeEngineProvider{kind: "clickhouse", roles: []connections.Role{connections.RoleDirect}}
	bad := &errReaderProvider{fakeEngineProvider: p}
	_, err := catalogs.NativeNames(context.Background(), bad, connections.Config{}, "")
	if !errors.Is(err, errReader) {
		t.Fatalf("err = %v", err)
	}
}

var errReader = errors.New("reader down")

type errReaderProvider struct{ *fakeEngineProvider }

func (p *errReaderProvider) OpenMetadata(context.Context, connections.Config) (connections.MetadataReader, error) {
	return nil, errReader
}

type deleteConnQuerier struct {
	db.Querier
	conn db.CatalogConnection
	err  error
}

func (q *deleteConnQuerier) GetCatalogConnectionByID(context.Context, pgtype.UUID) (db.CatalogConnection, error) {
	return q.conn, nil
}
func (q *deleteConnQuerier) DeleteCatalogConnection(context.Context, pgtype.UUID) error { return q.err }

// TestDeleteConnectionBackingEngine409: engines reference their connection
// ON DELETE RESTRICT, which Postgres reports as 23001.
func TestDeleteConnectionBackingEngine409(t *testing.T) {
	t.Parallel()
	q := &deleteConnQuerier{
		conn: ownedConnection(t, "postgresql", testUserID),
		err:  &pgconn.PgError{Code: "23001", ConstraintName: "engines_connection_id_fkey"},
	}
	h := engineHandlers(t, nil)
	h.queries = q
	h.access = wsaccess.New(h.queries)
	h.topology = topology.New(h.queries)
	h.conns = conns.New(q)
	resp, err := h.DeleteCatalogConnection(testContext(), DeleteCatalogConnectionRequestObject{Id: [16]byte{0xC1}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitDeleteCatalogConnectionResponse(rec); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "connection_in_use") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body)
	}
}

// The describeEngine reads: these fakes have no catalog bound to the engine.
func (q *engineQuerier) ListBindingsByEngine(context.Context, pgtype.UUID) ([]db.CatalogBinding, error) {
	return nil, nil
}

func (q *deleteConnQuerier) ListBindingsByEngine(context.Context, pgtype.UUID) ([]db.CatalogBinding, error) {
	return nil, nil
}
