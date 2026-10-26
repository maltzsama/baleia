package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	wsaccess "github.com/galileostudio/baleia/internal/workspaces/access"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/client"
)

// explorerFake adds the lookups the explorer and Lens handlers need on top of
// authzFake.
type explorerFake struct {
	*authzFake
	clusterID pgtype.UUID
	snaps     []db.TrinoQuerySnapshot
}

func (q *explorerFake) GetDefaultTrinoCluster(context.Context) (db.TrinoCluster, error) {
	return db.TrinoCluster{ID: q.clusterID}, nil
}

func (q *explorerFake) GetCatalogByTrinoName(_ context.Context, p db.GetCatalogByTrinoNameParams) (db.Catalog, error) {
	if p.TrinoCatalogName == q.cat.TrinoName() {
		return q.cat, nil
	}
	return db.Catalog{}, pgx.ErrNoRows
}

func (q *explorerFake) ListTrinoQuerySnapshots(context.Context, db.ListTrinoQuerySnapshotsParams) ([]db.TrinoQuerySnapshot, error) {
	return q.snaps, nil
}

func (q *explorerFake) ListTrinoQuerySnapshotsByUser(_ context.Context, p db.ListTrinoQuerySnapshotsByUserParams) ([]db.TrinoQuerySnapshot, error) {
	var out []db.TrinoQuerySnapshot
	for _, s := range q.snaps {
		if s.User == p.User {
			out = append(out, s)
		}
	}
	return out, nil
}

func (q *explorerFake) GetTrinoQuerySnapshot(_ context.Context, id string) (db.TrinoQuerySnapshot, error) {
	for _, s := range q.snaps {
		if s.TrinoQueryID == id {
			return s, nil
		}
	}
	return db.TrinoQuerySnapshot{}, pgx.ErrNoRows
}

// asUser injects the identity the shared auth middleware would.
func asUser(id, role string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), auth.ContextKeyUserID, id)
		ctx = context.WithValue(ctx, auth.ContextKeyRole, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func explorerRouter(h *API, userID, role string) http.Handler {
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler { return asUser(userID, role, next) })
		r.Get("/connections/{connection_id}/catalogs/{catalog_name}/namespaces", h.HandleListNamespaces)
		r.Get("/connections/{connection_id}/catalogs/{catalog_name}/namespaces/{namespace}/tables", h.HandleListTables)
		r.Get("/connections/{connection_id}/catalogs/{catalog_name}/namespaces/{namespace}/tables/{table}/columns", h.HandleListColumns)
	})
	return r
}

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

func newExplorerHandlers(t *testing.T) (*API, *explorerFake, *fakeTrino, string) {
	t.Helper()
	h, base, ft := newAuthzHandlers(t)
	q := &explorerFake{authzFake: base, clusterID: base.cat.ClusterID}
	h.queries = q
	h.access = wsaccess.New(h.queries)
	return h, q, ft, uuidString(q.conn.ID)
}

// TestListTables_NamespaceIsQuoted: a hostile namespace reaches Trino as one
// quoted identifier, never as SQL.
func TestListTables_NamespaceIsQuoted(t *testing.T) {
	h, _, ft, conn := newExplorerHandlers(t)
	router := explorerRouter(h, ownerID, "member")
	code, _ := get(t, router, "/connections/"+conn+"/catalogs/wh/namespaces/we%22ird/tables")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	got := ft.statements()
	if len(got) != 1 || got[0] != `SHOW TABLES FROM "wh"."we""ird"` {
		t.Fatalf("Trino received %q, want SHOW TABLES FROM \"wh\".\"we\"\"ird\"", got)
	}
}

func TestExplorer_QuotesEveryStatement(t *testing.T) {
	h, _, ft, conn := newExplorerHandlers(t)
	router := explorerRouter(h, ownerID, "member")
	get(t, router, "/connections/"+conn+"/catalogs/wh/namespaces")
	get(t, router, "/connections/"+conn+"/catalogs/wh/namespaces/ns/tables/ta%3Bble/columns")
	want := []string{`SHOW SCHEMAS FROM "wh"`, `DESCRIBE "wh"."ns"."ta;ble"`}
	got := ft.statements()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("statements = %q, want %q", got, want)
	}
}

func TestExplorer_ForeignUser404_NoTrinoCall(t *testing.T) {
	h, _, ft, conn := newExplorerHandlers(t)
	router := explorerRouter(h, strangerID, "member")
	for _, p := range []string{
		"/connections/" + conn + "/catalogs/wh/namespaces",
		"/connections/" + conn + "/catalogs/wh/namespaces/ns/tables",
		"/connections/" + conn + "/catalogs/wh/namespaces/ns/tables/t/columns",
	} {
		if code, body := get(t, router, p); code != 404 || !strings.Contains(body, `"not_found"`) {
			t.Errorf("%s: %d %s, want 404 not_found", p, code, body)
		}
	}
	if len(ft.statements()) != 0 {
		t.Fatalf("Trino was called for a foreign user: %v", ft.statements())
	}
}

// TestExplorer_CatalogMustBelongToConnection: a connection you own cannot be
// used to browse another connection's catalog.
func TestExplorer_CatalogMustBelongToConnection(t *testing.T) {
	h, q, ft, _ := newExplorerHandlers(t)
	// A second connection, also owned by the caller, that has no catalogs.
	owner, _ := uuidFromString(ownerID)
	otherConn := db.CatalogConnection{ID: pgtype.UUID{Bytes: [16]byte{0xC2}, Valid: true}, CreatedBy: owner}
	h.queries = &twoConns{explorerFake: q, second: otherConn}
	h.access = wsaccess.New(h.queries)
	router := explorerRouter(h, ownerID, "member")
	code, _ := get(t, router, "/connections/"+uuidString(otherConn.ID)+"/catalogs/wh/namespaces")
	if code != 404 {
		t.Fatalf("status %d, want 404", code)
	}
	if len(ft.statements()) != 0 {
		t.Fatal("Trino was called")
	}
}

type twoConns struct {
	*explorerFake
	second db.CatalogConnection
}

func (q *twoConns) GetCatalogConnectionByID(ctx context.Context, id pgtype.UUID) (db.CatalogConnection, error) {
	if id.Bytes == q.second.ID.Bytes {
		return q.second, nil
	}
	return q.explorerFake.GetCatalogConnectionByID(ctx, id)
}

// TestExplorer_TrinoErrorsAreNotReturned: the cause is logged; the client gets
// a code, a generic message and the request id, as valid JSON.
func TestExplorer_TrinoErrorsAreNotReturned(t *testing.T) {
	h, _, _, conn := newExplorerHandlers(t)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"q","error":{"message":"line 1:1: Catalog \"secret-name\" /etc/trino " broke","errorName":"X"}}`)
	}))
	defer failing.Close()
	h.engines, h.binder = trinoBackends(failing.URL)
	router := explorerRouter(h, ownerID, "member")
	code, body := get(t, router, "/connections/"+conn+"/catalogs/wh/namespaces")
	if code != 502 {
		t.Fatalf("status %d, want 502", code)
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("response is not valid JSON: %v (%s)", err, body)
	}
	if out["code"] != "engine_error" || strings.Contains(body, "secret-name") || strings.Contains(body, "/etc/trino") {
		t.Fatalf("Trino error text leaked or wrong code: %s", body)
	}
}

func lensRouter(q db.Querier, tc client.Client, userID, role string) http.Handler {
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler { return asUser(userID, role, next) })
		RegisterLensRoutes(r, q, &config.Config{TrinoUserIdentity: "uuid"}, tc, testEngines(), nil)
	})
	return r
}

func snapshot(id, user string) db.TrinoQuerySnapshot {
	return db.TrinoQuerySnapshot{TrinoQueryID: id, User: user, Sql: "SELECT secret_of_" + user, State: "finished"}
}

// TestLensQueries_MemberSeesOnlyOwn: a member sees their own Trino identity's
// queries; the admin sees everyone's.
func TestLensQueries_MemberSeesOnlyOwn(t *testing.T) {
	member := "u_" + ownerID
	q := &explorerFake{snaps: []db.TrinoQuerySnapshot{snapshot("q-mine", member), snapshot("q-theirs", "u_"+strangerID)}}

	count := func(userID, role string) (int, string) {
		code, body := get(t, lensRouter(q, nil, userID, role), "/lens/queries")
		if code != 200 {
			t.Fatalf("status %d", code)
		}
		var out struct{ Data []map[string]any }
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		return len(out.Data), body
	}
	if n, body := count(ownerID, "member"); n != 1 || strings.Contains(body, "secret_of_u_"+strangerID) {
		t.Fatalf("member sees %d queries, body=%s", n, body)
	}
	if n, _ := count(adminID, "admin"); n != 2 {
		t.Fatalf("admin sees %d queries, want 2", n)
	}

	// Another user's single query is a 404, not a 403 or its SQL.
	if code, body := get(t, lensRouter(q, nil, ownerID, "member"), "/lens/queries/q-theirs"); code != 404 || strings.Contains(body, "secret_of") {
		t.Fatalf("foreign query: %d %s, want 404", code, body)
	}
	if code, _ := get(t, lensRouter(q, nil, ownerID, "member"), "/lens/queries/q-mine"); code != 200 {
		t.Fatalf("own query: %d, want 200", code)
	}
	if code, _ := get(t, lensRouter(q, nil, adminID, "admin"), "/lens/queries/q-theirs"); code != 200 {
		t.Fatalf("admin on any query: %d, want 200", code)
	}

	// Analytics are scoped the same way.
	_, body := get(t, lensRouter(q, nil, ownerID, "member"), "/lens/analytics/queries")
	if !strings.Contains(body, `"total_queries":1`) {
		t.Fatalf("member analytics = %s, want total_queries 1", body)
	}
}

type recordingTemporal struct {
	client.Client
	mu    sync.Mutex
	calls [][]any
}

func (f *recordingTemporal) ExecuteWorkflow(_ context.Context, _ client.StartWorkflowOptions, _ any, args ...any) (client.WorkflowRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, args)
	return nil, nil
}

func post(t *testing.T, h http.Handler, path, body string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec.Code, rec.Body.String()
}

func TestTriggerMaintenance(t *testing.T) {
	h, q, _, conn := newExplorerHandlers(t)
	_ = h
	tc := &recordingTemporal{}
	path := "/lens/tables/" + conn + "/wh/analytics/events/maintain"

	// retention is validated: an injection attempt is a 422 and starts nothing.
	if code, _ := post(t, lensRouter(q, tc, ownerID, "member"), path, `{"retention":"7d') --"}`); code != 422 {
		t.Fatalf("hostile retention: %d, want 422", code)
	}
	if len(tc.calls) != 0 {
		t.Fatal("a workflow was started for an invalid retention")
	}

	// a stranger does not learn the connection exists.
	if code, _ := post(t, lensRouter(q, tc, strangerID, "member"), path, `{"retention":"7d"}`); code != 404 {
		t.Fatalf("stranger: %d, want 404", code)
	}

	// a catalog that is not on this connection is 404.
	if code, _ := post(t, lensRouter(q, tc, ownerID, "member"), "/lens/tables/"+conn+"/not_mine/ns/t/maintain", `{}`); code != 404 {
		t.Fatalf("catalog of another connection: %d, want 404", code)
	}
	if len(tc.calls) != 0 {
		t.Fatal("a workflow was started for a rejected request")
	}

	// the owner and an admin may start it; the stored Trino name reaches the workflow.
	for _, who := range [][2]string{{ownerID, "member"}, {adminID, "admin"}} {
		if code, body := post(t, lensRouter(q, tc, who[0], who[1]), path, `{"retention":"14d"}`); code != 202 {
			t.Fatalf("%s: %d %s, want 202", who[1], code, body)
		}
	}
	if len(tc.calls) != 2 || tc.calls[0][1] != "wh" || tc.calls[0][2] != "analytics" || tc.calls[0][4] != "14d" {
		t.Fatalf("workflow args = %v", tc.calls)
	}

	// default retention when the body is empty.
	if code, _ := post(t, lensRouter(q, tc, ownerID, "member"), path, ``); code != 202 || tc.calls[2][4] != "7d" {
		t.Fatalf("default retention: %d %v", code, tc.calls)
	}
}

func TestLensRoutesRequireAnIdentity(t *testing.T) {
	q := &explorerFake{}
	r := chi.NewRouter()
	RegisterLensRoutes(r, q, &config.Config{}, nil, nil, nil) // no auth middleware in front: no identity in ctx
	for _, p := range []string{"/lens/queries", "/lens/analytics/queries"} {
		if code, _ := get(t, r, p); code != 401 {
			t.Errorf("%s without identity: %d, want 401", p, code)
		}
	}
	time.Sleep(0)
}

// TestExplorer_PathParametersArePercentDecoded pins #134: %24 and %3B survive
// as RawPath and must be decoded before they reach Trino; a bad escape is 400.
func TestExplorer_PathParametersArePercentDecoded(t *testing.T) {
	h, _, ft, conn := newExplorerHandlers(t)
	router := explorerRouter(h, ownerID, "member")
	if code, _ := get(t, router, "/connections/"+conn+"/catalogs/wh/namespaces/ns/tables/events%24snapshots/columns"); code != 200 {
		t.Fatalf("status %d", code)
	}
	if got := ft.statements(); len(got) != 1 || got[0] != `DESCRIBE "wh"."ns"."events$snapshots"` {
		t.Fatalf("statements = %q", got)
	}

	// net/http never delivers a malformed escape, but the handler must not trust
	// that: a bad escape in the route parameter is a 400, not a Trino call.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("connection_id", conn)
	rctx.URLParams.Add("catalog_name", "wh")
	rctx.URLParams.Add("namespace", "ns%zz")
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKeyUserID, ownerID))
	rec := httptest.NewRecorder()
	h.HandleListTables(rec, req)
	if rec.Code != 400 {
		t.Fatalf("bad escape: %d, want 400", rec.Code)
	}
}
