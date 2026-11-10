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
	testExecID = db.UUID{Bytes: uuid.New(), Valid: true}
	testSecret = []byte("0123456789abcdef0123456789abcdef")
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

type fakeWorkspaces struct {
	mu sync.Mutex
	ws db.Workspace
}

func (f *fakeWorkspaces) GetWorkspaceByID(context.Context, db.UUID) (db.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ws, nil
}

func (f *fakeWorkspaces) setBI(enabled bool) {
	f.mu.Lock()
	f.ws.BIEnabled = enabled
	f.mu.Unlock()
}

type fakeEngine struct {
	queryengine.Engine
	url  string
	hc   *http.Client
	kind string
}

func (f fakeEngine) Kind() string { return f.kind }

func (f fakeEngine) ProxyClient() (string, *http.Client) { return f.url, f.hc }

type fakeEngines struct{ eng queryengine.Engine }

func (f fakeEngines) Get(context.Context, db.UUID) (queryengine.Engine, error) { return f.eng, nil }

type fakeCreds struct{ token string }

func (f fakeCreds) CatalogToken(context.Context, string, queryengine.SessionSpec) (string, error) {
	return f.token, nil
}

type fakeSettings struct {
	mu      sync.Mutex
	enabled bool
}

func (f *fakeSettings) GetBIGatewaySetting(context.Context) (db.BIGatewaySetting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return db.BIGatewaySetting{Enabled: f.enabled}, nil
}

func (f *fakeSettings) set(enabled bool) {
	f.mu.Lock()
	f.enabled = enabled
	f.mu.Unlock()
}

type fakeExecutions struct {
	mu         sync.Mutex
	execID     db.UUID
	created    []db.CreateQueryExecutionParams
	updated    []db.UpdateQueryExecutionParams
	failed     []db.FailQueryExecutionParams
	runningKey map[[16]byte]int64
	runningWS  map[[16]byte]int64
}

func (f *fakeExecutions) CreateQueryExecution(_ context.Context, arg db.CreateQueryExecutionParams) (db.QueryExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, arg)
	return db.QueryExecution{ID: f.execID, WorkspaceID: arg.WorkspaceID, UserID: arg.UserID, Status: arg.Status, Origin: arg.Origin, BIKeyID: arg.BIKeyID}, nil
}

func (f *fakeExecutions) UpdateQueryExecution(_ context.Context, arg db.UpdateQueryExecutionParams) (db.QueryExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updated = append(f.updated, arg)
	return db.QueryExecution{ID: arg.ID}, nil
}

func (f *fakeExecutions) FailQueryExecution(_ context.Context, arg db.FailQueryExecutionParams) (db.QueryExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, arg)
	return db.QueryExecution{ID: arg.ID}, nil
}

func (f *fakeExecutions) CountRunningBIExecutionsByKey(_ context.Context, keyID db.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runningKey[keyID.Bytes], nil
}

func (f *fakeExecutions) CountRunningBIExecutionsByWorkspace(_ context.Context, workspaceID db.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runningWS[workspaceID.Bytes], nil
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
	gateway    *httptest.Server
	trino      *coordinator
	scope      *fakeScope
	keys       fakeKeys
	execs      *fakeExecutions
	settings   *fakeSettings
	workspaces *fakeWorkspaces
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureAll(t, "trino", testSecret, 0, 0)
}

func newFixtureKind(t *testing.T, kind string) *fixture {
	t.Helper()
	return newFixtureAll(t, kind, testSecret, 0, 0)
}

func newFixtureSecret(t *testing.T, kind string, secret []byte) *fixture {
	t.Helper()
	return newFixtureAll(t, kind, secret, 0, 0)
}

func newFixtureAll(t *testing.T, kind string, secret []byte, maxKey, maxWS int) *fixture {
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
	execs := &fakeExecutions{execID: testExecID, runningKey: map[[16]byte]int64{}, runningWS: map[[16]byte]int64{}}
	settings := &fakeSettings{enabled: true}
	workspaces := &fakeWorkspaces{ws: db.Workspace{ID: testWSID, BIEnabled: true}}
	f := &fixture{trino: trino, scope: scope, keys: fakeKeys{cred: cred}, execs: execs, settings: settings, workspaces: workspaces}
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	gw, err := New(Deps{
		Secret:             secret,
		Keys:               f.keys,
		Engines:            fakeEngines{eng: fakeEngine{url: trino.server.URL, hc: trino.server.Client(), kind: kind}},
		Scope:              scope,
		Rules:              fakeRules{},
		Workspaces:         workspaces,
		Executions:         execs,
		Settings:           settings,
		MaxPerKey:          maxKey,
		MaxPerWorkspace:    maxWS,
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

func (f *fixture) post(t *testing.T, sql string, headers map[string]string) (int, http.Header, []byte) {
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
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Clone(), body
}

func TestStatementScopeHeadersAndURIs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	status, header, body := f.post(t, "SELECT 1", map[string]string{
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
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
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
	if header.Get("X-Trino-Set-Authorization-User") != "" {
		t.Error("the response must drop X-Trino-Set-Authorization-User")
	}

	if len(f.execs.created) != 1 {
		t.Fatalf("created executions = %d, want 1", len(f.execs.created))
	}
	created := f.execs.created[0]
	if created.Origin != "bi" || created.BIKeyID.Bytes != testKeyID.Bytes || created.WorkspaceID.Bytes != testWSID.Bytes || created.UserID.Bytes != testUserID.Bytes {
		t.Fatalf("execution = %+v", created)
	}
	if len(f.execs.updated) == 0 || f.execs.updated[len(f.execs.updated)-1].Status != "finished" {
		t.Fatalf("execution was not finished: %+v", f.execs.updated)
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
		if !strings.Contains(uri, "baleia_sig=") || !strings.Contains(uri, "baleia_qid=") || !strings.Contains(uri, "baleia_exec="+testExecID.String()) {
			t.Errorf("%s = %q, missing signature/query id/execution", field, uri)
		}
	}
}

func TestFollowAndDelete(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	status, _, body := f.post(t, "SELECT 1", nil)
	if status != http.StatusOK {
		t.Fatalf("statement status = %d, body = %s", status, body)
	}
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
	cancelled := false
	for _, update := range f.execs.updated {
		if update.Status == "cancelled" {
			cancelled = true
		}
	}
	if !cancelled {
		t.Fatalf("a DELETE must cancel the execution: %+v", f.execs.updated)
	}
}

// TestOffSwitches: the global and workspace switches revoke at the next
// request.
func TestOffSwitches(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.settings.set(false)
	if status, _, _ := f.post(t, "SELECT 1", nil); status != http.StatusForbidden {
		t.Fatalf("global switch off = %d, want 403", status)
	}

	f = newFixture(t)
	f.workspaces.setBI(false)
	if status, _, _ := f.post(t, "SELECT 1", nil); status != http.StatusForbidden {
		t.Fatalf("workspace switch off = %d, want 403", status)
	}
}

// TestConcurrencyLimit: a key at its running limit gets 429.
func TestConcurrencyLimit(t *testing.T) {
	t.Parallel()
	f := newFixtureAll(t, "trino", testSecret, 1, 0)
	f.execs.runningKey[testKeyID.Bytes] = 1
	status, header, _ := f.post(t, "SELECT 1", nil)
	if status != http.StatusTooManyRequests {
		t.Fatalf("limit = %d, want 429", status)
	}
	if header.Get("Retry-After") == "" {
		t.Fatal("429 must carry Retry-After")
	}
	if len(f.execs.created) != 0 {
		t.Fatal("a rejected statement must not create an execution")
	}
}

func TestURIKeyAndExpiry(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, _, body := f.post(t, "SELECT 1", nil)
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

	for name, change := range map[string]func(url.Values){
		"signature": func(q url.Values) { q.Set("baleia_sig", "forged") },
		"query id":  func(q url.Values) { q.Set("baleia_qid", "other") },
		"execution": func(q url.Values) { q.Set("baleia_exec", uuid.NewString()) },
		"path":      func(q url.Values) { q.Set("slug", "2") },
	} {
		u, _ = url.Parse(next)
		q = u.Query()
		change(q)
		u.RawQuery = q.Encode()
		req, _ = http.NewRequest(http.MethodGet, u.String(), nil)
		req.SetBasicAuth("bi@example.test", "secret")
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("tampered %s = %d, want 404", name, resp.StatusCode)
		}
	}
}

// TestCrossReplicaSignature: a URI signed by one replica validates on another
// that shares the secret, and fails on a replica with a different secret.
func TestCrossReplicaSignature(t *testing.T) {
	t.Parallel()
	a := newFixture(t)
	b := newFixtureSecret(t, "trino", testSecret)
	c := newFixtureSecret(t, "trino", []byte("ffffffffffffffffffffffffffffffff"))
	_, _, body := a.post(t, "SELECT 1", nil)
	var page map[string]any
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	next := page["nextUri"].(string)
	follow := func(f *fixture) int {
		t.Helper()
		u, err := url.Parse(next)
		if err != nil {
			t.Fatal(err)
		}
		base, err := url.Parse(f.gateway.URL)
		if err != nil {
			t.Fatal(err)
		}
		u.Scheme, u.Host = base.Scheme, base.Host
		req, err := http.NewRequest(http.MethodGet, u.String(), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth("bi@example.test", "secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if got := follow(b); got != http.StatusOK {
		t.Fatalf("same secret on another replica = %d, want 200", got)
	}
	if got := follow(c); got != http.StatusNotFound {
		t.Fatalf("different secret = %d, want 404", got)
	}
}

// TestNewRequiresSecret: the stateless URI signature needs the shared secret.
func TestNewRequiresSecret(t *testing.T) {
	t.Parallel()
	_, err := New(Deps{
		Keys: fakeKeys{}, Engines: fakeEngines{}, Scope: &fakeScope{},
		Workspaces:    &fakeWorkspaces{ws: db.Workspace{BIEnabled: true}},
		Executions:    &fakeExecutions{execID: testExecID, runningKey: map[[16]byte]int64{}, runningWS: map[[16]byte]int64{}},
		Settings:      &fakeSettings{enabled: true},
		TrinoIdentity: func(context.Context, db.UUID) (string, error) { return "", nil },
		Dialect:       dialect.NewRegistry().MustGet(dialect.Trino),
	})
	if err == nil {
		t.Fatal("New without a secret must fail")
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
		status, _, _ := f.post(t, sql, nil)
		if status != http.StatusForbidden {
			t.Errorf("%q status = %d, want 403", sql, status)
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

	for _, path := range []string{"/api/v1/workspaces", "/v1/query/abc", "/v1/statement", "/v1/statement/../v1/query"} {
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
	status, _, _ := f.post(t, "SELECT 1", nil)
	if status != http.StatusForbidden {
		t.Fatalf("revoked scope = %d, want 403", status)
	}
	if f.trino.count() != 0 {
		t.Fatal("a revoked scope reached Trino")
	}
	if len(f.execs.failed) == 0 {
		t.Fatal("a revoked scope must fail the recorded execution")
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
	execs := &fakeExecutions{execID: testExecID, runningKey: map[[16]byte]int64{}, runningWS: map[[16]byte]int64{}}
	gw, err := New(Deps{
		Secret:        testSecret,
		Keys:          fakeKeys{cred: cred},
		Engines:       fakeEngines{eng: fakeEngine{url: trino.server.URL, hc: trino.server.Client(), kind: "trino"}},
		Scope:         &fakeScope{rows: []db.ListWorkspaceTargetsRow{{EngineID: testEngine, EngineCatalogName: "lake", EngineKind: "trino", BindingEnabled: true, EngineEnabled: true, CatalogEnabled: true}}},
		Rules:         fakeRules{rows: []db.WorkloadRule{{ID: testKeyID, Enabled: true, Match: []byte(`{"origin":"bi"}`), Class: "heavy", Weight: 1}}},
		Workspaces:    &fakeWorkspaces{ws: db.Workspace{BIEnabled: true}},
		Executions:    execs,
		Settings:      &fakeSettings{enabled: true},
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
	req.Header.Set("X-Trino-Client-Tags", "baleia.class=interactive,baleia,bi,client")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	tags := trino.last().header.Get("X-Trino-Client-Tags")
	if !strings.Contains(tags, "baleia.class=heavy") || strings.Contains(tags, "baleia.class=interactive") {
		t.Fatalf("tags = %q; the client must not override the rule", tags)
	}
	if !strings.Contains(tags, "client") {
		t.Fatalf("tags = %q; the client's non-reserved tag must survive", tags)
	}
}

// TestNonTrinoEngine: the managed wrapper implements TrinoProxy for every
// kind, so the gateway must check the engine kind before using it.
func TestNonTrinoEngine(t *testing.T) {
	t.Parallel()
	f := newFixtureKind(t, "postgresql")
	status, _, _ := f.post(t, "SELECT 1", nil)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("non-trino status = %d, want 422", status)
	}
	if f.trino.count() != 0 {
		t.Fatal("a non-trino engine reached the coordinator")
	}
}
