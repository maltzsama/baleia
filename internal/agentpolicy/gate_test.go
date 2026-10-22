package agentpolicy

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/jackc/pgx/v5/pgtype"
)

func uid(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }

func ptr(id pgtype.UUID) *pgtype.UUID { return &id }

func str(s string) *string { return &s }

type fakeStore struct {
	agent          db.Agent
	bound          bool
	member         bool
	catalogGranted bool
	engine         db.Engine
	conn           db.CatalogConnection
	running        int64
	hourly         int64
	oldest         pgtype.Timestamptz
}

func (s *fakeStore) GetAgentByID(context.Context, pgtype.UUID) (db.Agent, error) {
	return s.agent, nil
}
func (s *fakeStore) AgentWorkspaceBound(context.Context, db.AgentWorkspaceBoundParams) (bool, error) {
	return s.bound, nil
}
func (s *fakeStore) CheckWorkspaceAccess(context.Context, db.CheckWorkspaceAccessParams) (bool, error) {
	return s.member, nil
}
func (s *fakeStore) CatalogGrantedToAgent(context.Context, db.CatalogGrantedToAgentParams) (bool, error) {
	return s.catalogGranted, nil
}
func (s *fakeStore) GetEngineByID(context.Context, pgtype.UUID) (db.Engine, error) {
	return s.engine, nil
}
func (s *fakeStore) GetCatalogConnectionByID(context.Context, pgtype.UUID) (db.CatalogConnection, error) {
	return s.conn, nil
}
func (s *fakeStore) CountRunningExecutionsByAgent(context.Context, pgtype.UUID) (int64, error) {
	return s.running, nil
}
func (s *fakeStore) CountExecutionsByAgentSince(context.Context, db.CountExecutionsByAgentSinceParams) (int64, error) {
	return s.hourly, nil
}
func (s *fakeStore) OldestExecutionByAgentSince(context.Context, db.OldestExecutionByAgentSinceParams) (pgtype.Timestamptz, error) {
	return s.oldest, nil
}

type fakeEngines struct{ readOnly queryengine.ReadOnlySupport }

func (f fakeEngines) ReadOnly(context.Context, pgtype.UUID) (queryengine.ReadOnlySupport, error) {
	return f.readOnly, nil
}

type fixture struct {
	store  *fakeStore
	gate   *Gate
	p      authz.Principal
	ws     pgtype.UUID
	cat    pgtype.UUID
	engine pgtype.UUID
	now    time.Time
}

func newFixture() *fixture {
	f := &fixture{
		store: &fakeStore{
			agent: db.Agent{
				ID: uid(1), OwnerID: uid(2), Kind: "external", Status: "active",
				Policy: []byte(`{}`),
			},
			bound:          true,
			member:         true,
			catalogGranted: true,
			engine:         db.Engine{ID: uid(4), Kind: "trino", ConnectionID: uid(5)},
			conn:           db.CatalogConnection{ID: uid(5)},
		},
		ws:     uid(3),
		cat:    uid(6),
		engine: uid(4),
		now:    time.Now(),
	}
	f.p = authz.Principal{UserID: uid(2), Role: "member", Kind: authz.KindAgent, AgentID: uid(1)}
	f.gate = NewGate(f.store, fakeEngines{readOnly: queryengine.ReadOnlyEnforced}, DefaultCeilings(), dialect.NewRegistry())
	f.gate.SetClock(func() time.Time { return f.now })
	return f
}

func (f *fixture) call(tool string, sql *string) Call {
	c := Call{Tool: tool, WorkspaceID: f.ws}
	if tool == ToolRunQuery {
		c.SQL = sql
		c.CatalogID = ptr(f.cat)
		c.EngineID = ptr(f.engine)
	}
	return c
}

func (f *fixture) check(t *testing.T, c Call) Decision {
	t.Helper()
	dec, err := f.gate.Check(context.Background(), f.p, c)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return dec
}

func TestToolNotAllowed(t *testing.T) {
	f := newFixture()
	f.store.agent.Policy = []byte(`{"tools":["list_catalogs"]}`)
	dec := f.check(t, f.call(ToolRunQuery, str("SELECT 1")))
	if dec.Allowed || dec.Reason != ReasonPolicy {
		t.Fatalf("decision = %+v, want policy refusal", dec)
	}
}

func TestCatalogRestriction(t *testing.T) {
	f := newFixture()
	f.store.agent.Policy = []byte(`{"catalogs":["00000000-0000-0000-0000-0000000000ff"]}`)
	dec := f.check(t, f.call(ToolRunQuery, str("SELECT 1")))
	if dec.Allowed || dec.Reason != ReasonPolicy {
		t.Fatalf("restricted catalog = %+v, want policy refusal", dec)
	}

	f = newFixture()
	f.store.catalogGranted = false
	dec = f.check(t, f.call(ToolRunQuery, str("SELECT 1")))
	if dec.Allowed || dec.Reason != ReasonScope {
		t.Fatalf("ungranted catalog = %+v, want scope refusal", dec)
	}
}

func TestWorkspaceBinding(t *testing.T) {
	f := newFixture()
	f.store.bound = false
	if dec := f.check(t, f.call(ToolListCatalogs, nil)); dec.Allowed || dec.Reason != ReasonScope {
		t.Fatalf("unbound workspace = %+v, want scope refusal", dec)
	}
	f = newFixture()
	f.store.member = false
	if dec := f.check(t, f.call(ToolListCatalogs, nil)); dec.Allowed || dec.Reason != ReasonScope {
		t.Fatalf("owner removed = %+v, want scope refusal", dec)
	}
}

func TestStatementRules(t *testing.T) {
	cases := []struct {
		sql  string
		want bool
	}{
		{"SELECT 1; SELECT 2", false},
		{"INSERT INTO t VALUES (1)", false},
		{"CREATE CATALOG evil USING iceberg", false},
		{"WITH x AS (SELECT 1 AS n) SELECT * FROM x", true},
		{"EXPLAIN SELECT 1", true},
	}
	for _, tc := range cases {
		f := newFixture()
		dec := f.check(t, f.call(ToolRunQuery, str(tc.sql)))
		if dec.Allowed != tc.want {
			t.Errorf("%q: decision = %+v, want allowed=%v", tc.sql, dec, tc.want)
		}
		if !tc.want && dec.Reason != ReasonStatement {
			t.Errorf("%q: reason = %q, want statement", tc.sql, dec.Reason)
		}
	}
}

func TestConcurrencyAndHourlyQuota(t *testing.T) {
	f := newFixture()
	f.store.running = 2 // default max_concurrent_queries
	if dec := f.check(t, f.call(ToolRunQuery, str("SELECT 1"))); dec.Allowed || dec.Reason != ReasonQuota {
		t.Fatalf("concurrency = %+v, want quota", dec)
	}

	f = newFixture()
	f.store.hourly = 120 // default max_queries_per_hour
	f.store.oldest = pgtype.Timestamptz{Time: f.now.Add(-30 * time.Minute), Valid: true}
	dec := f.check(t, f.call(ToolRunQuery, str("SELECT 1")))
	if dec.Allowed || dec.Reason != ReasonQuota {
		t.Fatalf("hourly = %+v, want quota", dec)
	}
	if dec.RetryAfter < 29*time.Minute || dec.RetryAfter > 31*time.Minute {
		t.Fatalf("retry after = %v, want about 30m", dec.RetryAfter)
	}
}

func TestBurstLimiter(t *testing.T) {
	f := newFixture()
	f.store.agent.Policy = []byte(`{"max_tool_calls_per_minute":2}`)
	for i := 0; i < 2; i++ {
		if dec := f.check(t, f.call(ToolListCatalogs, nil)); !dec.Allowed {
			t.Fatalf("call %d = %+v, want allowed", i+1, dec)
		}
	}
	if dec := f.check(t, f.call(ToolListCatalogs, nil)); dec.Allowed || dec.Reason != ReasonRate {
		t.Fatalf("third call = %+v, want rate refusal", dec)
	}
}

func TestCeilingsClampAndPolicyChangeTakesEffect(t *testing.T) {
	f := newFixture()
	f.store.agent.Policy = []byte(`{"max_rows_returned":10000}`)
	dec := f.check(t, f.call(ToolListCatalogs, nil))
	if !dec.Allowed || dec.Effective.MaxRowsReturned != 5000 {
		t.Fatalf("clamped rows = %d, want 5000", dec.Effective.MaxRowsReturned)
	}

	// Lowering the policy takes effect on the next call: no cache.
	f.store.agent.Policy = []byte(`{"max_rows_returned":10}`)
	dec = f.check(t, f.call(ToolListCatalogs, nil))
	if !dec.Allowed || dec.Effective.MaxRowsReturned != 10 {
		t.Fatalf("changed rows = %d, want 10", dec.Effective.MaxRowsReturned)
	}
}

func TestReadOnlyEnforcement(t *testing.T) {
	f := newFixture()
	f.gate = NewGate(f.store, fakeEngines{readOnly: queryengine.ReadOnlyNone}, DefaultCeilings(), dialect.NewRegistry())
	f.gate.SetClock(func() time.Time { return f.now })
	if dec := f.check(t, f.call(ToolRunQuery, str("SELECT 1"))); dec.Allowed || dec.Reason != ReasonScope {
		t.Fatalf("unenforced engine = %+v, want scope refusal", dec)
	}

	f.store.conn.CredentialsReadOnly = true
	if dec := f.check(t, f.call(ToolRunQuery, str("SELECT 1"))); !dec.Allowed {
		t.Fatalf("asserted read-only credentials = %+v, want allowed", dec)
	}
}

func TestDisabledAgentAndForeignOwner(t *testing.T) {
	f := newFixture()
	f.store.agent.Status = "disabled"
	if dec := f.check(t, f.call(ToolListCatalogs, nil)); dec.Allowed || dec.Reason != ReasonPolicy {
		t.Fatalf("disabled agent = %+v, want policy refusal", dec)
	}

	f = newFixture()
	f.p.UserID = uid(0xee)
	if dec := f.check(t, f.call(ToolListCatalogs, nil)); dec.Allowed || dec.Reason != ReasonScope {
		t.Fatalf("foreign owner = %+v, want scope refusal", dec)
	}
}

func TestValidate(t *testing.T) {
	ceilings := DefaultCeilings()
	if err := (Policy{Tools: []string{"nope"}}).Validate(ceilings); err == nil {
		t.Error("unknown tool accepted")
	}
	if err := (Policy{Tools: []string{ToolRunQuery}, Limits: Limits{MaxRowsReturned: 10000}}).Validate(ceilings); err == nil {
		t.Error("policy above the ceiling accepted")
	}
	if err := (Policy{Tools: []string{ToolRunQuery}, Catalogs: []string{"not-a-uuid"}}).Validate(ceilings); err == nil {
		t.Error("bad catalog UUID accepted")
	}
	if err := (Policy{Tools: []string{ToolRunQuery}, Limits: Limits{MaxRowsReturned: 500}}).Validate(ceilings); err != nil {
		t.Errorf("valid policy rejected: %v", err)
	}
}
