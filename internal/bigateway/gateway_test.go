package bigateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/identity/bikeys"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/google/uuid"
)

var (
	testUserID = db.UUID{Bytes: uuid.New(), Valid: true}
	testKeyID  = db.UUID{Bytes: uuid.New(), Valid: true}
	testWSID   = db.UUID{Bytes: uuid.New(), Valid: true}
	testEngine = db.UUID{Bytes: uuid.New(), Valid: true}
)

type fakeKeys struct {
	cred bikeys.Credential
	err  error
}

func (f fakeKeys) Authenticate(_ context.Context, email, secret string) (bikeys.Credential, error) {
	if f.err != nil {
		return bikeys.Credential{}, f.err
	}
	if email != f.cred.User.Email || secret != "secret" {
		return bikeys.Credential{}, bikeys.ErrInvalid
	}
	return f.cred, nil
}

type fakeScope struct {
	mu   sync.Mutex
	rows []db.ListWorkspaceTargetsRow
}

func (f *fakeScope) ListWorkspaceTargets(context.Context, db.UUID) ([]db.ListWorkspaceTargetsRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]db.ListWorkspaceTargetsRow(nil), f.rows...), nil
}

func (f *fakeScope) set(rows []db.ListWorkspaceTargetsRow) {
	f.mu.Lock()
	f.rows = rows
	f.mu.Unlock()
}

type fakeRules struct{ rows []db.WorkloadRule }

func (f fakeRules) ListWorkloadRules(context.Context) ([]db.WorkloadRule, error) { return f.rows, nil }

type fakeWorkspaces struct{ ws db.Workspace }

func (f fakeWorkspaces) GetWorkspaceByID(context.Context, db.UUID) (db.Workspace, error) {
	return f.ws, nil
}

type fakeEngine struct {
	queryengine.Engine
	url string
	hc  *http.Client
}

func (f fakeEngine) ProxyClient() (string, *http.Client) { return f.url, f.hc }

type fakeEngines struct{ eng queryengine.Engine }

func (f fakeEngines) Get(context.Context, db.UUID) (queryengine.Engine, error) { return f.eng, nil }

type fakeCreds struct{ token string }

func (f fakeCreds) CatalogToken(context.Context, string, queryengine.SessionSpec) (string, error) {
	return f.token, nil
}

type coordinator struct {
	mu       sync.Mutex
	requests []recorded
	server   *httptest.Server
}

type recorded struct {
	method string
	path   string
	query  url.Values
	header http.Header
	body   string
}

func (c *coordinator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	c.requests = append(c.requests, recorded{method: r.Method, path: r.URL.Path, query: r.URL.Query(), header: r.Header.Clone(), body: string(body)})
	c.mu.Unlock()
	switch {
	case r.URL.Path == "/v1/info":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nodeVersion":{"version":"484"},"coordinator":true,"starting":false}`))
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Trino-Set-Catalog", "lake")
		w.Header().Set("X-Trino-Set-Authorization-User", "mallory")
		_, _ = w.Write([]byte(`{"id":"20260101_000000_00001_abcde","nextUri":"` + c.server.URL + `/v1/statement/executing/20260101_000000_00001_abcde/slug?slug=1","infoUri":"` + c.server.URL + `/v1/query/20260101_000000_00001_abcde","partialCancelUri":"` + c.server.URL + `/v1/statement/executing/20260101_000000_00001_abcde/slug?slug=1","columns":[{"name":"x","type":"bigint"}],"data":[[1]],"stats":{"state":"FINISHED"}}`))
	}
}

func (c *coordinator) last() recorded {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests[len(c.requests)-1]
}

func (c *coordinator) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

type fixture struct {
	gateway *httptest.Server
	trino   *coordinator
	scope   *fakeScope
	keys    fakeKeys
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	trino := &coordinator{}
	trino.server = httptest.NewServer(trino)
	t.Cleanup(trino.server.Close)

	cred := bikeys.Credential{
		Key:  db.ApiKey{ID: testKeyID, Kind: "bi", WorkspaceID: testWSID, EngineID: testEngine},
		User: db.User{ID: testUserID, Email: "bi@example.test", Status: "active", Role: "member"},
	}
	scope := &fakeScope{rows: []db.ListWorkspaceTargetsRow{{
		EngineID: testEngine, EngineCatalogName: "lake", EngineKind: "trino",
		BindingEnabled: true, EngineEnabled: true, CatalogEnabled: true,
	}, {
		EngineID: testEngine, EngineCatalogName: "raw", EngineKind: "trino",
		BindingEnabled: true, EngineEnabled: true, CatalogEnabled: true,
	}}}
	f := &fixture{trino: trino, scope: scope, keys: fakeKeys{cred: cred}}
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	gw, err := New(Deps{
		Keys:               f.keys,
		Engines:            fakeEngines{eng: fakeEngine{url: trino.server.URL, hc: trino.server.Client()}},
		Scope:              scope,
		Rules:              fakeRules{},
		Workspaces:         fakeWorkspaces{ws: db.Workspace{ID: testWSID}},
		TrinoIdentity:      func(context.Context, db.UUID) (string, error) { return "u_owner", nil },
		CatalogCredentials: fakeCreds{token: "tok"},
		Dialect:            d,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.gateway = httptest.NewServer(gw)
	t.Cleanup(f.gateway.Close)
	return f
}

func (f *fixture) post(t *testing.T, sql string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.gateway.URL+"/v1/statement", strings.NewReader(sql))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("bi@example.test", "secret")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func TestStatementScopeHeadersAndURIs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	resp, body := f.post(t, "SELECT 1", map[string]string{
		"X-Trino-Catalog":             "lake",
		"X-Trino-Schema":              "sales",
		"X-Trino-Client-Tags":         "client-tag",
		"X-Trino-User":                "mallory",
		"X-Trino-Role":                "admin",
		"X-Trino-Original-User":       "mallory",
		"X-Trino-Extra-Credential":    "baleia.catalogs=system",
		"X-Trino-Transaction-Id":      "tx-1",
		"X-Trino-Query-Data-Encoding": "json+zstd",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	got := f.trino.last()
	if got.method != http.MethodPost || got.path != "/v1/statement" || got.body != "SELECT 1" {
		t.Fatalf("upstream request = %+v", got)
	}
	if got.header.Get("X-Trino-User") != "u_owner" {
		t.Errorf("X-Trino-User = %q", got.header.Get("X-Trino-User"))
	}
	if got.header.Get("X-Trino-Catalog") != "lake" || got.header.Get("X-Trino-Schema") != "sales" {
		t.Errorf("catalog/schema = %q/%q", got.header.Get("X-Trino-Catalog"), got.header.Get("X-Trino-Schema"))
	}
	extra := got.header.Get("X-Trino-Extra-Credential")
	if !strings.Contains(extra, "baleia.catalogs=lake%2Craw") || !strings.Contains(extra, "baleia.mode=readonly") || !strings.Contains(extra, "token=tok") {
		t.Errorf("extra credentials = %q", extra)
	}
	if strings.Contains(extra, "system") {
		t.Errorf("client extra credential leaked: %q", extra)
	}
	tags := got.header.Get("X-Trino-Client-Tags")
	for _, want := range []string{"client-tag", "baleia", "bi", "baleia.class=interactive"} {
		if !strings.Contains(tags, want) {
			t.Errorf("client tags = %q, missing %q", tags, want)
		}
	}
	for _, dropped := range []string{"X-Trino-Role", "X-Trino-Original-User", "X-Trino-Transaction-Id", "X-Trino-Query-Data-Encoding"} {
		if got.header.Get(dropped) != "" {
			t.Errorf("%s reached Trino: %q", dropped, got.header.Get(dropped))
		}
	}
	if got.header.Get("Authorization") != "" {
		t.Error("Authorization reached Trino")
	}
	if resp.Header.Get("X-Trino-Set-Authorization-User") != "" {
		t.Error("the response must drop X-Trino-Set-Authorization-User")
	}

	var page map[string]any
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("response: %v (%s)", err, body)
	}
	for _, field := range []string{"nextUri", "infoUri", "partialCancelUri"} {
		uri, _ := page[field].(string)
		if !strings.HasPrefix(uri, f.gateway.URL) {
			t.Errorf("%s = %q, want the gateway", field, uri)
		}
		if !strings.Contains(uri, "baleia_key="+testKeyID.String()) || !strings.Contains(uri, "baleia_exp=") {
			t.Errorf("%s = %q, missing key/expiry", field, uri)
		}
	}
}

func TestFollowAndDelete(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, body := f.post(t, "SELECT 1", nil)
	var page map[string]any
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	next := page["nextUri"].(string)
	req, _ := http.NewRequest(http.MethodGet, next, nil)
	req.SetBasicAuth("bi@example.test", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("follow status = %d", resp.StatusCode)
	}
	got := f.trino.last()
	if got.method != http.MethodGet || got.path == "/v1/statement" || got.query.Get("slug") != "1" {
		t.Fatalf("follow upstream = %+v", got)
	}
	if got.query.Get("baleia_key") != "" || got.query.Get("baleia_exp") != "" {
		t.Errorf("gateway params reached Trino: %v", got.query)
	}

	del, _ := http.NewRequest(http.MethodDelete, next, nil)
	del.SetBasicAuth("bi@example.test", "secret")
	dresp, err := http.DefaultClient.Do(del)
	if err != nil {
		t.Fatal(err)
	}
	_ = dresp.Body.Close()
	if dresp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", dresp.StatusCode)
	}
}

func TestURIKeyAndExpiry(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, body := f.post(t, "SELECT 1", nil)
	var page map[string]any
	_ = json.Unmarshal(body, &page)
	next := page["nextUri"].(string)
	u, _ := url.Parse(next)
	q := u.Query()
	q.Set("baleia_key", uuid.NewString())
	u.RawQuery = q.Encode()
	req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
	req.SetBasicAuth("bi@example.test", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("another key = %d, want 404", resp.StatusCode)
	}

	u, _ = url.Parse(next)
	q = u.Query()
	q.Set("baleia_exp", "1")
	u.RawQuery = q.Encode()
	req, _ = http.NewRequest(http.MethodGet, u.String(), nil)
	req.SetBasicAuth("bi@example.test", "secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expired = %d, want 404", resp.StatusCode)
	}
}

func TestAuthAndReadOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	req, _ := http.NewRequest(http.MethodPost, f.gateway.URL+"/v1/statement", strings.NewReader("SELECT 1"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("no basic auth = %d (%q)", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	if f.trino.count() != 0 {
		t.Fatal("an unauthenticated request reached Trino")
	}

	for _, sql := range []string{"INSERT INTO t VALUES (1)", "CREATE TABLE t (x int)", "DROP TABLE t", "EXPLAIN ANALYZE SELECT 1", "SET SESSION x=1", "/* c */ DELETE FROM t"} {
		resp, _ := f.post(t, sql, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%q status = %d, want 403", sql, resp.StatusCode)
		}
	}
	if f.trino.count() != 0 {
		t.Fatal("a write reached Trino")
	}
}

func TestInfoAndUnknownPaths(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	req, _ := http.NewRequest(http.MethodGet, f.gateway.URL+"/v1/info", nil)
	req.SetBasicAuth("bi@example.test", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"version":"484"`) {
		t.Fatalf("info = %d %s", resp.StatusCode, body)
	}
	if f.trino.last().path != "/v1/info" {
		t.Fatalf("info upstream = %+v", f.trino.last())
	}

	for _, path := range []string{"/api/v1/workspaces", "/v1/query/abc", "/v1/statement"} {
		resp, err := http.Get(f.gateway.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestScopeIsRechecked(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.scope.set(nil)
	resp, _ := f.post(t, "SELECT 1", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("revoked scope = %d, want 403", resp.StatusCode)
	}
	if f.trino.count() != 0 {
		t.Fatal("a revoked scope reached Trino")
	}
}

func TestWorkloadRuleTag(t *testing.T) {
	t.Parallel()
	trino := &coordinator{}
	trino.server = httptest.NewServer(trino)
	t.Cleanup(trino.server.Close)
	cred := bikeys.Credential{
		Key:  db.ApiKey{ID: testKeyID, Kind: "bi", WorkspaceID: testWSID, EngineID: testEngine},
		User: db.User{ID: testUserID, Email: "bi@example.test", Status: "active", Role: "member"},
	}
	gw, err := New(Deps{
		Keys:          fakeKeys{cred: cred},
		Engines:       fakeEngines{eng: fakeEngine{url: trino.server.URL, hc: trino.server.Client()}},
		Scope:         &fakeScope{rows: []db.ListWorkspaceTargetsRow{{EngineID: testEngine, EngineCatalogName: "lake", EngineKind: "trino", BindingEnabled: true, EngineEnabled: true, CatalogEnabled: true}}},
		Rules:         fakeRules{rows: []db.WorkloadRule{{ID: testKeyID, Enabled: true, Match: []byte(`{"origin":"bi"}`), Class: "heavy", Weight: 1}}},
		Workspaces:    fakeWorkspaces{},
		TrinoIdentity: func(context.Context, db.UUID) (string, error) { return "u_owner", nil },
		Dialect:       dialect.NewRegistry().MustGet(dialect.Trino),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gw)
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/statement", strings.NewReader("SELECT 1"))
	req.SetBasicAuth("bi@example.test", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(trino.last().header.Get("X-Trino-Client-Tags"), "baleia.class=heavy") {
		t.Fatalf("tags = %q", trino.last().header.Get("X-Trino-Client-Tags"))
	}
}
