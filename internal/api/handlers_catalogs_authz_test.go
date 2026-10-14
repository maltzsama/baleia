package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/trino"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// fakeTrino records every statement it receives and answers FINISHED.
type fakeTrino struct {
	mu   sync.Mutex
	sqls []string
	srv  *httptest.Server
}

func newFakeTrino(t *testing.T) *fakeTrino {
	t.Helper()
	f := &fakeTrino{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/statement" {
			b, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.sqls = append(f.sqls, string(b))
			f.mu.Unlock()
			_, _ = io.WriteString(w, `{"id":"q1","stats":{"state":"FINISHED"}}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTrino) statements() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sqls...)
}

type authzFake struct {
	db.Querier
	conn        db.CatalogConnection
	cat         db.Catalog
	updated     []db.UpdateCatalogParams
	upserts     []string
	registryErr []string
	order       []string
	mu          sync.Mutex
	g           grantsState
}

func (q *authzFake) GetCatalogConnectionByID(_ context.Context, id pgtype.UUID) (db.CatalogConnection, error) {
	if id.Bytes != q.conn.ID.Bytes {
		return db.CatalogConnection{}, pgx.ErrNoRows
	}
	return q.conn, nil
}

func (q *authzFake) GetCatalogByID(_ context.Context, id pgtype.UUID) (db.Catalog, error) {
	if id.Bytes != q.cat.ID.Bytes {
		return db.Catalog{}, pgx.ErrNoRows
	}
	return q.cat, nil
}

func (q *authzFake) ListCatalogsByConnection(context.Context, pgtype.UUID) ([]db.Catalog, error) {
	return []db.Catalog{q.cat}, nil
}

func (q *authzFake) UpdateCatalog(_ context.Context, p db.UpdateCatalogParams) (db.Catalog, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.updated = append(q.updated, p)
	c := q.cat
	c.Name, c.Warehouse, c.Connector, c.ExtraProps, c.TrinoCatalogName = p.Name, p.Warehouse, p.Connector, p.ExtraProps, p.TrinoCatalogName
	return c, nil
}

func (q *authzFake) UpsertTrinoCatalogRegistry(_ context.Context, p db.UpsertTrinoCatalogRegistryParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.upserts = append(q.upserts, p.CatalogName)
	q.order = append(q.order, "upsert")
	return nil
}

func (q *authzFake) MarkTrinoCatalogRegistryError(_ context.Context, p db.MarkTrinoCatalogRegistryErrorParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.registryErr = append(q.registryErr, p.CatalogName)
	return nil
}

func (q *authzFake) SoftDeleteTrinoCatalogRegistry(context.Context, db.SoftDeleteTrinoCatalogRegistryParams) error {
	return nil
}

func (q *authzFake) ListEnabledCatalogs(context.Context) ([]db.Catalog, error) {
	return []db.Catalog{q.cat}, nil
}

const (
	ownerID    = "11111111-1111-1111-1111-111111111111"
	strangerID = "22222222-2222-2222-2222-222222222222"
	adminID    = "33333333-3333-3333-3333-333333333333"
)

func ctxAs(id, role string) context.Context {
	ctx := context.WithValue(context.Background(), auth.ContextKeyUserID, id)
	return context.WithValue(ctx, auth.ContextKeyRole, role)
}

// newAuthzHandlers builds a Handlers whose single connection (and one catalog
// under it) belongs to ownerID, with a recording fake Trino.
func newAuthzHandlers(t *testing.T) (*Handlers, *authzFake, *fakeTrino) {
	t.Helper()
	owner, err := uuidFromString(ownerID)
	if err != nil {
		t.Fatal(err)
	}
	kr := testKeyring(t)
	connID := pgtype.UUID{Bytes: [16]byte{0xC1}, Valid: true}
	sealed, err := kr.Seal([]byte(`{"client_id":"id","client_secret":"secret"}`), "catalog_connections:"+uuidString(connID))
	if err != nil {
		t.Fatal(err)
	}
	q := &authzFake{
		conn: db.CatalogConnection{ID: connID, Name: "src", Type: "polaris", Url: "http://polaris:8181", CreatedBy: owner, CredentialsEnc: &sealed},
		cat: db.Catalog{
			ID: pgtype.UUID{Bytes: [16]byte{0xCA}, Valid: true}, ConnectionID: connID, Name: "wh", TrinoCatalogName: strp("wh"),
			ClusterID: pgtype.UUID{Bytes: [16]byte{0xCC}, Valid: true}, Warehouse: strp("wh"), Connector: strp("iceberg"), ExtraProps: []byte("{}"),
		},
	}
	ft := newFakeTrino(t)
	h := &Handlers{
		queries:      q,
		keyring:      kr,
		trino:        trino.New(ft.srv.URL, "baleia_admin"),
		catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute),
	}
	return h, q, ft
}

func uuidString(u pgtype.UUID) string {
	s, _ := u.Value()
	return s.(string)
}

func reqIDs(q *authzFake) (conn [16]byte, cat [16]byte) { return q.conn.ID.Bytes, q.cat.ID.Bytes }

func TestListCatalogs_Authz(t *testing.T) {
	h, q, _ := newAuthzHandlers(t)
	conn, _ := reqIDs(q)
	for name, tc := range map[string]struct {
		ctx  context.Context
		want int
	}{
		"owner": {ctxAs(ownerID, "member"), 200}, "admin": {ctxAs(adminID, "admin"), 200}, "stranger": {ctxAs(strangerID, "member"), 404},
	} {
		resp, err := h.ListCatalogs(tc.ctx, ListCatalogsRequestObject{ConnectionId: conn})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if code := visitCode(t, resp); code != tc.want {
			t.Errorf("%s: status %d, want %d", name, code, tc.want)
		}
	}
}

func TestGetCatalog_Authz(t *testing.T) {
	h, q, _ := newAuthzHandlers(t)
	conn, cat := reqIDs(q)
	get := func(ctx context.Context, c, k [16]byte) int {
		resp, err := h.GetCatalog(ctx, GetCatalogRequestObject{ConnectionId: c, CatalogId: k})
		if err != nil {
			t.Fatal(err)
		}
		return visitCode(t, resp)
	}
	if got := get(ctxAs(ownerID, "member"), conn, cat); got != 200 {
		t.Errorf("owner: %d", got)
	}
	if got := get(ctxAs(adminID, "admin"), conn, cat); got != 200 {
		t.Errorf("admin: %d", got)
	}
	if got := get(ctxAs(strangerID, "member"), conn, cat); got != 404 {
		t.Errorf("stranger: %d", got)
	}
	// The catalog must be reached through its own connection.
	if got := get(ctxAs(ownerID, "member"), [16]byte{0xFF}, cat); got != 404 {
		t.Errorf("catalog under a different connection id: %d, want 404", got)
	}
}

func TestUpdateCatalog_Authz(t *testing.T) {
	h, q, ft := newAuthzHandlers(t)
	conn, cat := reqIDs(q)
	body := &CreateCatalogRequest{Warehouse: "wh2"}

	resp, err := h.UpdateCatalog(ctxAs(strangerID, "member"), UpdateCatalogRequestObject{ConnectionId: conn, CatalogId: cat, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if code := visitCode(t, resp); code != 404 {
		t.Fatalf("stranger: %d, want 404", code)
	}
	if len(q.updated) != 0 || len(ft.statements()) != 0 {
		t.Fatalf("a foreign update changed state: updated=%d trino=%v", len(q.updated), ft.statements())
	}

	resp, err = h.UpdateCatalog(ctxAs(ownerID, "member"), UpdateCatalogRequestObject{ConnectionId: conn, CatalogId: cat, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if code := visitCode(t, resp); code != 200 || len(q.updated) != 1 {
		t.Fatalf("owner: %d updated=%d", code, len(q.updated))
	}
}

// TestUpdateCatalog_RenamePersistsAndRecreatesInOrder pins #132 and #129: the
// new trino_catalog_name is written with the row, and the registry row is
// upserted (re-enabled) after DROP and before CREATE.
func TestUpdateCatalog_RenamePersistsAndRecreatesInOrder(t *testing.T) {
	h, q, ft := newAuthzHandlers(t)
	conn, cat := reqIDs(q)
	resp, err := h.UpdateCatalog(ctxAs(ownerID, "member"), UpdateCatalogRequestObject{ConnectionId: conn, CatalogId: cat, Body: &CreateCatalogRequest{TrinoCatalogName: "wh_renamed"}})
	if err != nil {
		t.Fatal(err)
	}
	if code := visitCode(t, resp); code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(q.updated) != 1 || q.updated[0].TrinoCatalogName == nil || *q.updated[0].TrinoCatalogName != "wh_renamed" {
		t.Fatalf("rename not persisted: %+v", q.updated)
	}
	var seq []string
	for _, s := range ft.statements() {
		switch {
		case strings.HasPrefix(s, "DROP CATALOG"):
			seq = append(seq, "drop")
		case strings.HasPrefix(s, "CREATE CATALOG"):
			seq = append(seq, "create")
		}
	}
	// old name dropped (rename), then drop of the new name, then create.
	if len(seq) < 2 || seq[len(seq)-1] != "create" || seq[len(seq)-2] != "drop" {
		t.Fatalf("statement order = %v, want ... drop, create", seq)
	}
	if len(q.upserts) != 1 || q.upserts[0] != "wh_renamed" {
		t.Fatalf("registry upserts = %v", q.upserts)
	}
}

func TestSyncCatalog_Authz(t *testing.T) {
	h, q, ft := newAuthzHandlers(t)
	_, cat := reqIDs(q)

	resp, err := h.SyncCatalog(ctxAs(strangerID, "member"), SyncCatalogRequestObject{CatalogId: cat})
	if err != nil {
		t.Fatal(err)
	}
	if code := visitCode(t, resp); code != 404 {
		t.Fatalf("stranger: %d, want 404", code)
	}
	if len(ft.statements()) != 0 {
		t.Fatalf("a foreign sync reached Trino: %v", ft.statements())
	}

	resp, err = h.SyncCatalog(ctxAs(adminID, "admin"), SyncCatalogRequestObject{CatalogId: cat})
	if err != nil {
		t.Fatal(err)
	}
	if code := visitCode(t, resp); code != 200 {
		t.Fatalf("admin: %d, want 200", code)
	}
	var seq []string
	for _, s := range ft.statements() {
		if strings.HasPrefix(s, "DROP CATALOG") {
			seq = append(seq, "drop")
		} else if strings.HasPrefix(s, "CREATE CATALOG") {
			seq = append(seq, "create")
		}
	}
	if len(seq) != 2 || seq[0] != "drop" || seq[1] != "create" || len(q.upserts) != 1 {
		t.Fatalf("sync did drop/upsert/create wrongly: %v upserts=%v", seq, q.upserts)
	}
}

// TestSyncCatalog_FailedRecreateIsASyncError: a DDL failure is recorded and
// answered 409, never reported as synced.
func TestSyncCatalog_FailedRecreateIsASyncError(t *testing.T) {
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
	h.trino = trino.New(failing.URL, "baleia_admin")
	_, cat := reqIDs(q)
	resp, err := h.SyncCatalog(ctxAs(ownerID, "member"), SyncCatalogRequestObject{CatalogId: cat})
	if err != nil {
		t.Fatal(err)
	}
	if code := visitCode(t, resp); code != 409 || len(q.registryErr) != 1 {
		t.Fatalf("status %d registryErr=%v, want 409 with the error recorded", code, q.registryErr)
	}
}

func TestDeleteCatalog_ForeignUser404_NoTrinoCall(t *testing.T) {
	h, q, ft := newAuthzHandlers(t)
	conn, cat := reqIDs(q)
	resp, err := h.DeleteCatalog(ctxAs(strangerID, "member"), DeleteCatalogRequestObject{ConnectionId: conn, CatalogId: cat})
	if err != nil {
		t.Fatal(err)
	}
	if code := visitCode(t, resp); code != 404 {
		t.Fatalf("status %d, want 404", code)
	}
	if got := ft.statements(); len(got) != 0 {
		t.Fatalf("Trino received statements for a foreign delete: %v", got)
	}
}

func TestCreateCatalog_ForeignUser404_NothingWritten(t *testing.T) {
	h, q, ft := newAuthzHandlers(t)
	conn, _ := reqIDs(q)
	resp, err := h.CreateCatalog(ctxAs(strangerID, "member"), CreateCatalogRequestObject{ConnectionId: conn, Body: &CreateCatalogRequest{Name: "x", TrinoCatalogName: "x", Warehouse: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if code := visitCode(t, resp); code != 404 {
		t.Fatalf("status %d, want 404", code)
	}
	if len(ft.statements()) != 0 || len(q.upserts) != 0 {
		t.Fatal("a foreign create wrote state")
	}
}

func TestReconcileTrino_AdminOnly(t *testing.T) {
	h, _, _ := newAuthzHandlers(t)
	resp, err := h.ReconcileTrino(ctxAs(ownerID, "member"), ReconcileTrinoRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if code := visitCode(t, resp); code != 403 {
		t.Fatalf("member: %d, want 403", code)
	}
}

func TestConnectionHandlers_Authz(t *testing.T) {
	h, q, _ := newAuthzHandlers(t)
	id := q.conn.ID.Bytes
	stranger, admin, owner := ctxAs(strangerID, "member"), ctxAs(adminID, "admin"), ctxAs(ownerID, "member")

	name := "renamed"
	cases := []struct {
		name string
		call func(ctx context.Context) int
	}{
		{"get", func(ctx context.Context) int {
			r, err := h.GetCatalogConnection(ctx, GetCatalogConnectionRequestObject{Id: id})
			return codeOf(t, r, err)
		}},
		{"update", func(ctx context.Context) int {
			r, err := h.UpdateCatalogConnection(ctx, UpdateCatalogConnectionRequestObject{Id: id, Body: &UpdateCatalogConnectionRequest{Name: &name}})
			return codeOf(t, r, err)
		}},
		{"test", func(ctx context.Context) int {
			r, err := h.TestCatalogConnection(ctx, TestCatalogConnectionRequestObject{Id: id})
			return codeOf(t, r, err)
		}},
	}
	for _, tc := range cases {
		if got := tc.call(stranger); got != 404 {
			t.Errorf("%s as stranger: %d, want 404", tc.name, got)
		}
	}
	// Owner and global admin get past authz (get/update succeed outright).
	for _, who := range []context.Context{owner, admin} {
		if got := cases[0].call(who); got != 200 {
			t.Errorf("get as owner/admin: %d", got)
		}
	}
}

// visitCode renders a strict response object and returns its HTTP status.
func visitCode(t *testing.T, resp interface{}) int {
	t.Helper()
	rec := httptest.NewRecorder()
	switch r := resp.(type) {
	case interface {
		VisitListCatalogsResponse(http.ResponseWriter) error
	}:
		_ = r.VisitListCatalogsResponse(rec)
	case interface {
		VisitGetCatalogResponse(http.ResponseWriter) error
	}:
		_ = r.VisitGetCatalogResponse(rec)
	case interface {
		VisitUpdateCatalogResponse(http.ResponseWriter) error
	}:
		_ = r.VisitUpdateCatalogResponse(rec)
	case interface {
		VisitSyncCatalogResponse(http.ResponseWriter) error
	}:
		_ = r.VisitSyncCatalogResponse(rec)
	case interface {
		VisitDeleteCatalogResponse(http.ResponseWriter) error
	}:
		_ = r.VisitDeleteCatalogResponse(rec)
	case interface {
		VisitCreateCatalogResponse(http.ResponseWriter) error
	}:
		_ = r.VisitCreateCatalogResponse(rec)
	case interface {
		VisitReconcileTrinoResponse(http.ResponseWriter) error
	}:
		_ = r.VisitReconcileTrinoResponse(rec)
	default:
		t.Fatalf("unsupported response type %T", resp)
	}
	return rec.Code
}

func codeOf(t *testing.T, resp interface{}, err error) int {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	switch r := resp.(type) {
	case interface {
		VisitGetCatalogConnectionResponse(http.ResponseWriter) error
	}:
		_ = r.VisitGetCatalogConnectionResponse(rec)
	case interface {
		VisitUpdateCatalogConnectionResponse(http.ResponseWriter) error
	}:
		_ = r.VisitUpdateCatalogConnectionResponse(rec)
	case interface {
		VisitTestCatalogConnectionResponse(http.ResponseWriter) error
	}:
		_ = r.VisitTestCatalogConnectionResponse(rec)
	case interface {
		VisitDeleteCatalogConnectionResponse(http.ResponseWriter) error
	}:
		_ = r.VisitDeleteCatalogConnectionResponse(rec)
	default:
		t.Fatalf("unsupported response type %T", resp)
	}
	return rec.Code
}

// TestSyncCatalog_StuckDDLTimesOut pins #130: a CREATE CATALOG that never
// answers is cut off by the DDL deadline and reported as a sync error instead
// of blocking the request forever.
func TestSyncCatalog_StuckDDLTimesOut(t *testing.T) {
	old := catalogDDLTimeout
	catalogDDLTimeout = 300 * time.Millisecond
	defer func() { catalogDDLTimeout = old }()

	h, q, _ := newAuthzHandlers(t)
	stuck := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.HasPrefix(string(b), "CREATE CATALOG") {
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
			return
		}
		_, _ = io.WriteString(w, `{"id":"q","stats":{"state":"FINISHED"}}`)
	}))
	defer stuck.Close()
	h.trino = trino.New(stuck.URL, "baleia_admin")

	_, cat := reqIDs(q)
	start := time.Now()
	resp, err := h.SyncCatalog(ctxAs(ownerID, "member"), SyncCatalogRequestObject{CatalogId: cat})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("sync took %s; the DDL deadline did not apply", elapsed)
	}
	if code := visitCode(t, resp); code != 409 || len(q.registryErr) != 1 {
		t.Fatalf("status %d registryErr=%v, want 409 with the timeout recorded", code, q.registryErr)
	}
}

func strp(s string) *string { return &s }
