package bigateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/maltzsama/baleia/internal/audit"
	"github.com/maltzsama/baleia/internal/db"
	"github.com/maltzsama/baleia/internal/dialect"
	"github.com/maltzsama/baleia/internal/semanticcatalog"
)

type fakeSemantic struct {
	mu   sync.Mutex
	snap semanticcatalog.Snapshot
	err  error
	reqs []semanticcatalog.Request
}

func (f *fakeSemantic) Read(_ context.Context, req semanticcatalog.Request) (semanticcatalog.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	return f.snap, f.err
}

type fakeAudit struct {
	mu     sync.Mutex
	events []audit.Event
}

func (f *fakeAudit) Record(_ context.Context, e audit.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}

type semanticFixture struct {
	*fixture
	semantic *fakeSemantic
	audit    *fakeAudit
}

// newSemanticFixture is the statement fixture with a semantic catalog reader
// and an audit recorder.
func newSemanticFixture(t *testing.T) *semanticFixture {
	t.Helper()
	base := newFixture(t)
	sf := &semanticFixture{fixture: base, semantic: &fakeSemantic{snap: semanticcatalog.Snapshot{
		Workspace: semanticcatalog.Workspace{ID: testWSID.String(), Name: "Sales"},
		Models: []semanticcatalog.Model{
			{Slug: "a", SourceTables: []string{"lake.sales.orders"}},
			{Slug: "b", SourceTables: []string{}},
			{Slug: "c", SourceTables: []string{}},
		},
		Metrics: []semanticcatalog.Metric{{Slug: "revenue", SourceTables: []string{"lake.sales.orders"}}},
	}}, audit: &fakeAudit{}}
	gw, err := New(Deps{
		Secret: testSecret, Keys: base.keys,
		Engines: fakeEngines{eng: fakeEngine{url: base.trino.server.URL, hc: base.trino.server.Client(), kind: "trino"}},
		Scope:   base.scope, Rules: fakeRules{}, Workspaces: base.workspaces, Executions: base.execs, Settings: base.settings,
		TrinoIdentity: func(context.Context, db.UUID) (string, error) { return "u_owner", nil },
		Dialect:       dialect.NewRegistry().MustGet(dialect.Trino),
		Semantic:      sf.semantic, Audit: sf.audit,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gw)
	t.Cleanup(srv.Close)
	sf.gateway = srv
	return sf
}

func (f *fixture) get(t *testing.T, path string, auth bool) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.gateway.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth {
		req.SetBasicAuth("bi@example.test", "secret")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func TestSemanticUnservedWithoutReader(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if status, _ := f.get(t, "/baleia/v1/semantic", true); status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no reader is wired", status)
	}
}

func TestSemanticSummaryUsesTheKey(t *testing.T) {
	t.Parallel()
	f := newSemanticFixture(t)
	status, body := f.get(t, "/baleia/v1/semantic", true)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var got semanticSummary
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.ContractVersion != semanticcatalog.ContractVersion || got.Models != 3 || got.Metrics != 1 || got.Workspace.Name != "Sales" {
		t.Fatalf("summary = %+v", got)
	}
	req := f.semantic.reqs[0]
	if req.WorkspaceID != testWSID || req.UserID != testUserID || req.EngineID != testEngine {
		t.Fatalf("request = %+v, want the key's workspace, owner and engine", req)
	}
	if len(f.audit.events) != 1 || f.audit.events[0].Action != audit.BISemanticCatalogRead ||
		f.audit.events[0].WorkspaceID != testWSID.String() {
		t.Fatalf("audit = %+v", f.audit.events)
	}
	for _, change := range f.audit.events[0].Changes {
		if s, ok := change.To.(string); ok && strings.Contains(s, "secret") {
			t.Fatal("the audit event carries the key")
		}
	}
}

func TestSemanticModelsPaginate(t *testing.T) {
	t.Parallel()
	f := newSemanticFixture(t)
	status, body := f.get(t, "/baleia/v1/semantic/models?limit=2", true)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var first semanticPage[semanticcatalog.Model]
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.Items[0].Slug != "a" || first.NextAfter != "b" {
		t.Fatalf("first page = %+v", first)
	}
	_, body = f.get(t, "/baleia/v1/semantic/models?limit=2&after=b", true)
	var second semanticPage[semanticcatalog.Model]
	if err := json.Unmarshal(body, &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].Slug != "c" || second.NextAfter != "" {
		t.Fatalf("second page = %+v", second)
	}

	_, body = f.get(t, "/baleia/v1/semantic/metrics", true)
	var metrics semanticPage[semanticcatalog.Metric]
	if err := json.Unmarshal(body, &metrics); err != nil {
		t.Fatal(err)
	}
	if len(metrics.Items) != 1 || metrics.Items[0].Slug != "revenue" || metrics.NextAfter != "" {
		t.Fatalf("metrics = %+v", metrics)
	}
}

func TestSemanticRejections(t *testing.T) {
	t.Parallel()
	f := newSemanticFixture(t)
	if status, _ := f.get(t, "/baleia/v1/semantic/models", false); status != http.StatusUnauthorized {
		t.Errorf("no key = %d, want 401", status)
	}
	if status, _ := f.get(t, "/baleia/v1/semantic/models?limit=0", true); status != http.StatusBadRequest {
		t.Errorf("limit 0 = %d, want 400", status)
	}
	if status, _ := f.get(t, "/baleia/v1/semantic/models?limit=501", true); status != http.StatusBadRequest {
		t.Errorf("limit 501 = %d, want 400", status)
	}
	req, _ := http.NewRequest(http.MethodPost, f.gateway.URL+"/baleia/v1/semantic", nil)
	req.SetBasicAuth("bi@example.test", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", resp.StatusCode)
	}

	f.settings.set(false)
	if status, _ := f.get(t, "/baleia/v1/semantic", true); status != http.StatusForbidden {
		t.Errorf("global switch off = %d, want 403", status)
	}
	f.settings.set(true)
	f.workspaces.setBI(false)
	if status, _ := f.get(t, "/baleia/v1/semantic", true); status != http.StatusForbidden {
		t.Errorf("workspace switch off = %d, want 403", status)
	}
	if len(f.semantic.reqs) != 0 {
		t.Error("a rejected request read the catalog")
	}
}

func TestSemanticReadFailures(t *testing.T) {
	t.Parallel()
	f := newSemanticFixture(t)
	f.semantic.err = semanticcatalog.ErrTooLarge
	if status, _ := f.get(t, "/baleia/v1/semantic", true); status != http.StatusUnprocessableEntity {
		t.Errorf("too large = %d, want 422", status)
	}
	f.semantic.err = errors.New("database down")
	status, body := f.get(t, "/baleia/v1/semantic", true)
	if status != http.StatusBadGateway || strings.Contains(string(body), "database down") {
		t.Errorf("read failure = %d %s, want 502 without the internal error", status, body)
	}
	if len(f.audit.events) != 0 {
		t.Error("a failed read was audited as a read")
	}
}
