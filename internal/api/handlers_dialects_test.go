package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestGetDialect(t *testing.T) {
	h := &Handlers{dialects: dialect.NewRegistry()}
	resp, err := h.GetDialect(testContext(), GetDialectRequestObject{Name: "tsql"})
	if err != nil {
		t.Fatal(err)
	}
	ok, isOK := resp.(GetDialect200JSONResponse)
	if !isOK || ok.Body.Name != "tsql" || ok.Body.FormatterLanguage != "transactsql" || ok.Body.CodemirrorBase != "MSSQL" ||
		ok.Body.Keywords == nil || ok.Headers.ETag == "" || ok.Headers.CacheControl == "" {
		t.Fatalf("response = %#v", resp)
	}
	for _, inm := range []string{ok.Headers.ETag, "W/" + ok.Headers.ETag, `"other", ` + ok.Headers.ETag, "*"} {
		resp, _ := h.GetDialect(testContext(), GetDialectRequestObject{Name: "tsql", Params: GetDialectParams{IfNoneMatch: &inm}})
		if nm, is304 := resp.(GetDialect304Response); !is304 || nm.Headers.ETag != ok.Headers.ETag {
			t.Errorf("If-None-Match %s: %T", inm, resp)
		}
	}
	other := `"nope"`
	if resp, _ := h.GetDialect(testContext(), GetDialectRequestObject{Name: "tsql", Params: GetDialectParams{IfNoneMatch: &other}}); func() bool { _, ok := resp.(GetDialect200JSONResponse); return !ok }() {
		t.Error("a stale ETag must get the document")
	}
	if resp, _ := h.GetDialect(testContext(), GetDialectRequestObject{Name: "cobol"}); func() bool { _, ok := resp.(GetDialect404JSONResponse); return !ok }() {
		t.Error("unknown dialect must be 404")
	}
	if resp, _ := h.GetDialect(context.Background(), GetDialectRequestObject{Name: "tsql"}); func() bool { _, ok := resp.(GetDialect401JSONResponse); return !ok }() {
		t.Error("anonymous must be 401")
	}
}

func TestSplitStatements_UTF16Offsets(t *testing.T) {
	h := &Handlers{dialects: dialect.NewRegistry()}
	sql := "SELECT 'é😀';\n  SELECT \"a;b\" -- x\n"
	resp, err := h.SplitStatements(testContext(), SplitStatementsRequestObject{Name: "trino", Body: &SplitStatementsRequest{Sql: sql}})
	if err != nil {
		t.Fatal(err)
	}
	spans, ok := resp.(SplitStatements200JSONResponse)
	if !ok || len(spans) != 2 {
		t.Fatalf("response = %#v", resp)
	}
	js := utf16.Encode([]rune(sql)) // what the editor indexes
	text := func(s StatementSpan) string { return string(utf16.Decode(js[s.Start:s.End])) }
	if text(spans[0]) != "SELECT 'é😀'" || text(spans[1]) != `SELECT "a;b" -- x` {
		t.Errorf("spans %v cut %q / %q", spans, text(spans[0]), text(spans[1]))
	}

	for name, sql := range map[string]string{"trino": "SELECT 'open", "mysql": "DELIMITER //\nSELECT 1//"} {
		resp, _ := h.SplitStatements(testContext(), SplitStatementsRequestObject{Name: name, Body: &SplitStatementsRequest{Sql: sql}})
		if _, ok := resp.(SplitStatements422JSONResponse); !ok {
			t.Errorf("%s %q: %T", name, sql, resp)
		}
	}
	long := strings.Repeat("é", maxSplitSQL+1) // characters, not bytes, count
	if resp, _ := h.SplitStatements(testContext(), SplitStatementsRequestObject{Name: "trino", Body: &SplitStatementsRequest{Sql: long}}); func() bool { _, ok := resp.(SplitStatements422JSONResponse); return !ok }() {
		t.Error("sql over maxLength must be 422")
	}
	if resp, _ := h.SplitStatements(testContext(), SplitStatementsRequestObject{Name: "trino", Body: &SplitStatementsRequest{Sql: long[:len(long)-2]}}); func() bool { _, ok := resp.(SplitStatements200JSONResponse); return !ok }() {
		t.Error("sql at maxLength must be accepted")
	}
	if resp, _ := h.SplitStatements(testContext(), SplitStatementsRequestObject{Name: "cobol", Body: &SplitStatementsRequest{Sql: "x"}}); func() bool { _, ok := resp.(SplitStatements404JSONResponse); return !ok }() {
		t.Error("unknown dialect must be 404")
	}
}

type failingEngines struct{}

func (failingEngines) Get(context.Context, pgtype.UUID) (queryengine.Engine, error) {
	return nil, errors.New("no adapter")
}
func (failingEngines) DefaultTrino(context.Context) (queryengine.Engine, error) {
	return nil, errors.New("no adapter")
}

func TestDescribeEngine(t *testing.T) {
	var infoCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/info" {
			infoCalls.Add(1)
			_, _ = w.Write([]byte(`{"nodeVersion":{"version":"483"},"coordinator":true,"starting":false}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	engines, _ := trinoBackends(srv.URL)
	h := &Handlers{dialects: dialect.NewRegistry(), queries: noEngineRows{}, engines: engines, versionCache: NewTTLStore[engineProbe](engineVersionTTL)}
	row := db.Engine{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}, Name: "trino", Kind: "trino", Class: "federated",
		IdentityMode: "impersonation", Enabled: true, UpdatedAt: pgtype.Timestamptz{Time: time.Unix(10, 0), Valid: true}}

	for range 2 {
		e := h.describeEngine(context.Background(), row)
		if e.Version == nil || *e.Version != "483" {
			t.Fatalf("version = %v", e.Version)
		}
		if e.Dialect == nil || e.Dialect.Name != "trino" || e.Capabilities == nil || e.Capabilities.Dialect != "trino" ||
			!e.Capabilities.SupportsCatalogDdl || e.Capabilities.ReadOnly != "enforced" || e.Capabilities.CancelScope == nil {
			t.Fatalf("engine = %+v", e)
		}
	}
	if n := infoCalls.Load(); n != 1 {
		t.Errorf("version fetched %d times, want 1 (cached)", n)
	}

	// No adapter for the kind (or a disabled engine): the dialect is still known.
	h = &Handlers{dialects: dialect.NewRegistry(), queries: noEngineRows{}, engines: failingEngines{}}
	row.Kind = "postgresql"
	e := h.describeEngine(context.Background(), row)
	if e.Capabilities != nil || e.Version != nil || e.Dialect == nil || e.Dialect.Name != "postgresql" {
		t.Errorf("engine without adapter = %+v", e)
	}
}

// slowEngines answers like failingEngines after a delay.
type slowEngines struct{ delay time.Duration }

func (s slowEngines) Get(context.Context, pgtype.UUID) (queryengine.Engine, error) {
	time.Sleep(s.delay)
	return nil, errors.New("no adapter")
}
func (s slowEngines) DefaultTrino(context.Context) (queryengine.Engine, error) {
	return nil, errors.New("no adapter")
}

// TestDescribeEngines_ParallelInOrder: slow engines overlap and the list
// keeps its order.
func TestDescribeEngines_ParallelInOrder(t *testing.T) {
	h := &Handlers{dialects: dialect.NewRegistry(), queries: noEngineRows{}, engines: slowEngines{delay: 100 * time.Millisecond}}
	rows := make([]db.Engine, 8)
	for i := range rows {
		rows[i] = db.Engine{ID: pgtype.UUID{Bytes: [16]byte{byte(i)}, Valid: true}, Name: string(rune('a' + i)), Kind: "postgresql"}
	}
	start := time.Now()
	out := h.describeEngines(context.Background(), rows)
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("8 slow engines took %s; lookups must overlap", d)
	}
	for i, e := range out {
		if e.Name != rows[i].Name {
			t.Fatalf("order lost: %d is %s", i, e.Name)
		}
	}
}

func TestExplainSQL(t *testing.T) {
	h := &Handlers{dialects: dialect.NewRegistry()}
	resp, err := h.ExplainSQL(testContext(), ExplainSQLRequestObject{Name: "sqlite", Body: &ExplainRequest{Sql: "SELECT 1;"}})
	if err != nil {
		t.Fatal(err)
	}
	ok, isOK := resp.(ExplainSQL200JSONResponse)
	if !isOK || !ok.Supported || ok.Sql == nil || *ok.Sql != "EXPLAIN QUERY PLAN SELECT 1" {
		t.Fatalf("response = %#v", resp)
	}

	if resp, _ := h.ExplainSQL(testContext(), ExplainSQLRequestObject{Name: "tsql", Body: &ExplainRequest{Sql: "SELECT 1"}}); func() bool {
		r, ok := resp.(ExplainSQL200JSONResponse)
		return !ok || r.Supported
	}() {
		t.Errorf("tsql has no explain: %#v", resp)
	}
	if _, ok := mustExplain(t, h, ExplainSQLRequestObject{Name: "cobol", Body: &ExplainRequest{Sql: "x"}}).(ExplainSQL404JSONResponse); !ok {
		t.Error("unknown dialect must be 404")
	}
	if _, ok := mustExplain(t, h, ExplainSQLRequestObject{Name: "sqlite", Body: &ExplainRequest{Sql: "  "}}).(ExplainSQL422JSONResponse); !ok {
		t.Error("empty sql must be 422")
	}
	if resp, err := h.ExplainSQL(context.Background(), ExplainSQLRequestObject{Name: "sqlite", Body: &ExplainRequest{Sql: "SELECT 1"}}); err != nil {
		t.Fatal(err)
	} else if _, ok := resp.(ExplainSQL401JSONResponse); !ok {
		t.Error("anonymous must be 401")
	}
}

func mustExplain(t *testing.T, h *Handlers, req ExplainSQLRequestObject) ExplainSQLResponseObject {
	t.Helper()
	resp, err := h.ExplainSQL(testContext(), req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestEngineHealth_FromProbe guards #110: a failed version probe is cached as
// unreachable with its error code instead of leaving health unknown.
func TestEngineHealth_FromProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	engines, _ := trinoBackends(srv.URL)
	h := &Handlers{dialects: dialect.NewRegistry(), queries: noEngineRows{}, engines: engines, versionCache: NewTTLStore[engineProbe](engineVersionTTL)}
	row := db.Engine{ID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true}, Name: "trino", Kind: "trino", Class: "federated",
		IdentityMode: "impersonation", Enabled: true, UpdatedAt: pgtype.Timestamptz{Time: time.Unix(20, 0), Valid: true}}

	e := h.describeEngine(context.Background(), row)
	if e.Health == nil {
		t.Fatal("health is nil")
	}
	if e.Health.Status != "unreachable" {
		t.Fatalf("health.status = %q, want unreachable", e.Health.Status)
	}
	if e.Health.ErrorCode == nil || *e.Health.ErrorCode == "" {
		t.Fatalf("error_code = %v", e.Health.ErrorCode)
	}
	if e.Health.CheckedAt == nil {
		t.Fatal("checked_at is nil")
	}
}

func TestConnectionAllows_ChecksComputedActions(t *testing.T) {
	actions := []CatalogConnectionAllowedAction{CatalogConnectionAllowedActionRegisterEngine}
	conn := CatalogConnection{AllowedActions: &actions}
	if !connectionAllows(conn, "register_engine") {
		t.Fatal("register_engine should be allowed")
	}
	if connectionAllows(conn, "delete") {
		t.Fatal("delete should not be allowed")
	}
	if connectionAllows(CatalogConnection{}, "register_engine") {
		t.Fatal("nil actions should allow nothing")
	}
}

// noEngineRows has no connection or binding for any engine, so describeEngine
// reports neither.
type noEngineRows struct{ db.Querier }

func (noEngineRows) GetCatalogConnectionByID(context.Context, pgtype.UUID) (db.CatalogConnection, error) {
	return db.CatalogConnection{}, pgx.ErrNoRows
}

func (noEngineRows) ListBindingsByEngine(context.Context, pgtype.UUID) ([]db.CatalogBinding, error) {
	return nil, nil
}
