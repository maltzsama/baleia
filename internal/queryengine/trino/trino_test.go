package trino

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	trinoclient "github.com/galileostudio/baleia/internal/trino"
)

func TestErrorMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, typ string
		want      queryengine.ErrorCode
	}{
		{"SYNTAX_ERROR", "USER_ERROR", queryengine.CodeSyntax},
		{"TYPE_MISMATCH", "USER_ERROR", queryengine.CodeSyntax},
		{"INVALID_FUNCTION_ARGUMENT", "USER_ERROR", queryengine.CodeSyntax},
		{"TABLE_NOT_FOUND", "USER_ERROR", queryengine.CodeNotFound},
		{"SCHEMA_NOT_FOUND", "USER_ERROR", queryengine.CodeNotFound},
		{"CATALOG_NOT_FOUND", "USER_ERROR", queryengine.CodeNotFound},
		{"COLUMN_NOT_FOUND", "USER_ERROR", queryengine.CodeNotFound},
		{"FUNCTION_NOT_FOUND", "USER_ERROR", queryengine.CodeNotFound},
		{"PERMISSION_DENIED", "USER_ERROR", queryengine.CodePermission},
		{"EXCEEDED_TIME_LIMIT", "INSUFFICIENT_RESOURCES", queryengine.CodeTimeout},
		{"EXCEEDED_CPU_LIMIT", "INSUFFICIENT_RESOURCES", queryengine.CodeTimeout},
		{"USER_CANCELED", "USER_ERROR", queryengine.CodeCancelled},
		{"ADMINISTRATIVELY_KILLED", "USER_ERROR", queryengine.CodeCancelled},
		{"EXCEEDED_LOCAL_MEMORY_LIMIT", "INSUFFICIENT_RESOURCES", queryengine.CodeResource},
		{"QUERY_QUEUE_FULL", "INSUFFICIENT_RESOURCES", queryengine.CodeResource},
		{"SOMETHING_NEW", "INSUFFICIENT_RESOURCES", queryengine.CodeResource},
		{"SERVER_STARTING_UP", "INTERNAL_ERROR", queryengine.CodeUnavailable},
		{"SERVER_SHUTTING_DOWN", "INTERNAL_ERROR", queryengine.CodeUnavailable},
		{"NOT_SUPPORTED", "USER_ERROR", queryengine.CodeSyntax},
		{"GENERIC_INTERNAL_ERROR", "INTERNAL_ERROR", queryengine.CodeInternal},
	}
	for _, c := range cases {
		err := mapError(&trinoclient.QueryError{ErrorName: c.name, ErrorType: c.typ, Message: "m"})
		var e *queryengine.Error
		if !errors.As(err, &e) || e.Code != c.want || e.Native != c.name || e.Engine != Kind {
			t.Errorf("%s/%s → %v, want %s", c.name, c.typ, err, c.want)
		}
	}
	if got := queryengine.CodeOf(mapError(&trinoclient.TransportError{Status: 503})); got != queryengine.CodeUnavailable {
		t.Errorf("transport error → %s", got)
	}
	if got := queryengine.CodeOf(mapError(&trinoclient.TransportError{Status: 401})); got != queryengine.CodePermission {
		t.Errorf("HTTP 401 → %s", got)
	}
	if got := queryengine.CodeOf(mapError(&trinoclient.TransportError{Status: 404})); got != queryengine.CodeUnavailable {
		t.Errorf("HTTP 404 → %s", got)
	}
	if got := queryengine.CodeOf(mapError(context.Canceled)); got != queryengine.CodeCancelled {
		t.Errorf("cancelled context → %s", got)
	}
	// net timeouts match context.DeadlineExceeded, but a timeout before the
	// engine answered is a transport failure and must stay retryable (#672).
	if got := queryengine.CodeOf(mapError(&trinoclient.TransportError{Err: context.DeadlineExceeded})); got != queryengine.CodeUnavailable {
		t.Errorf("transport timeout before the engine answered → %s", got)
	}
	if got := queryengine.CodeOf(mapError(context.DeadlineExceeded)); got != queryengine.CodeTimeout {
		t.Errorf("statement deadline → %s", got)
	}
}

// fakeCoordinator answers the statement protocol and records request headers.
type fakeCoordinator struct {
	mu       sync.Mutex
	requests []http.Header
	// respond returns the headers to set and the JSON body for a statement.
	respond func(sql string) (http.Header, map[string]any)
}

func (f *fakeCoordinator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Header.Clone())
	f.mu.Unlock()
	body := make([]byte, r.ContentLength)
	_, _ = r.Body.Read(body)
	h, resp := f.respond(string(body))
	for k, vs := range h {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (f *fakeCoordinator) last() http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

type collect struct {
	schemas []*arrow.Schema
	rows    int64
}

func (c *collect) Begin(s *arrow.Schema) error { c.schemas = append(c.schemas, s); return nil }
func (c *collect) Write(r arrow.RecordBatch) error {
	c.rows += r.NumRows()
	r.Release()
	return nil
}
func (c *collect) End(queryengine.Stats) error { return nil }

func testEngine(t *testing.T, f *fakeCoordinator) *Engine {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return New("e1", srv.URL, "svc", "adm", d, dialect.NewRegistry().MustGet(dialect.Trino))
}

func TestSessionAppliesResponseHeaders(t *testing.T) {
	t.Parallel()
	f := &fakeCoordinator{respond: func(sql string) (http.Header, map[string]any) {
		h := http.Header{}
		switch sql {
		case "USE sales.public":
			h.Set("X-Trino-Set-Catalog", "sales")
			h.Set("X-Trino-Set-Schema", "public")
		case "SET SESSION query_max_run_time = '5m'":
			h.Add("X-Trino-Set-Session", "query_max_run_time=5m")
		case "PREPARE q FROM SELECT 1":
			h.Add("X-Trino-Added-Prepare", "q=SELECT+1")
		case "RESET SESSION query_max_run_time":
			h.Add("X-Trino-Clear-Session", "query_max_run_time")
		case "SET SESSION AUTHORIZATION mallory":
			h.Set("X-Trino-Set-Authorization-User", "mallory")
		}
		return h, map[string]any{"id": "20260101_000000_00001_abcde"}
	}}
	e := testEngine(t, f)
	ctx := context.Background()
	s, err := e.OpenSession(ctx, queryengine.SessionSpec{
		Identity:        queryengine.Identity{UserID: "u_123"},
		ReadOnly:        true,
		AllowedCatalogs: []string{"lake", "raw"},
		Tags:            queryengine.Tags{ExecutionID: "exec-1", RequestID: "req-1", Source: "editor"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{"USE sales.public", "SET SESSION query_max_run_time = '5m'", "PREPARE q FROM SELECT 1", "SELECT 1"} {
		if err := s.Execute(ctx, sql, queryengine.Callbacks{}, &collect{}); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	h := f.last()
	want := map[string]string{
		"X-Trino-User":               "u_123",
		"X-Trino-Catalog":            "sales",
		"X-Trino-Schema":             "public",
		"X-Trino-Session":            "query_max_run_time=5m",
		"X-Trino-Prepared-Statement": "q=SELECT+1",
		"X-Trino-Extra-Credential":   "baleia.catalogs=lake%2Craw,baleia.mode=readonly",
		"X-Trino-Source":             "baleia",
		"X-Trino-Client-Tags":        "baleia,editor",
		"X-Trino-Trace-Token":        "req-1",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if !strings.Contains(h.Get("X-Trino-Client-Info"), `"execution_id":"exec-1"`) {
		t.Errorf("client info = %q", h.Get("X-Trino-Client-Info"))
	}

	// Clearing a property and a request to switch the user.
	for _, sql := range []string{"RESET SESSION query_max_run_time", "SET SESSION AUTHORIZATION mallory", "SELECT 2"} {
		if err := s.Execute(ctx, sql, queryengine.Callbacks{}, &collect{}); err != nil {
			t.Fatal(err)
		}
	}
	h = f.last()
	if h.Get("X-Trino-Session") != "" {
		t.Errorf("cleared property still sent: %q", h.Get("X-Trino-Session"))
	}
	if h.Get("X-Trino-User") != "u_123" {
		t.Errorf("identity taken from the engine response: %q", h.Get("X-Trino-User"))
	}

	// A new session starts clean, as the service identity.
	fresh, _ := e.OpenSession(ctx, queryengine.SessionSpec{})
	if err := fresh.Execute(ctx, "SELECT 3", queryengine.Callbacks{}, &collect{}); err != nil {
		t.Fatal(err)
	}
	h = f.last()
	if h.Get("X-Trino-Catalog") != "" || h.Get("X-Trino-User") != "svc" || h.Get("X-Trino-Extra-Credential") != "" {
		t.Errorf("fresh session headers = %v", h)
	}
}

// TestSessionScopeOnlyForUserIdentity pins the guard contract (#839): a user
// identity carries baleia.catalogs resolved from the source, while the service
// identity sends none and never resolves it.
func TestSessionScopeOnlyForUserIdentity(t *testing.T) {
	t.Parallel()
	f := &fakeCoordinator{respond: func(string) (http.Header, map[string]any) {
		return nil, map[string]any{"id": "20260101_000000_00001_abcde"}
	}}
	e := testEngine(t, f)
	ctx := context.Background()

	userCalls := 0
	user, err := e.OpenSession(ctx, queryengine.SessionSpec{
		Identity: queryengine.Identity{UserID: "u_1"},
		AllowedCatalogsSource: func(context.Context) ([]string, error) {
			userCalls++
			return []string{"lake", "raw"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := user.Execute(ctx, "SELECT 1", queryengine.Callbacks{}, &collect{}); err != nil {
		t.Fatal(err)
	}
	_ = user.Close()
	if userCalls != 1 {
		t.Fatalf("user scope resolved %d times, want 1", userCalls)
	}
	if got := f.last().Get("X-Trino-Extra-Credential"); got != "baleia.catalogs=lake%2Craw" {
		t.Fatalf("user extra credentials = %q, want the workspace scope", got)
	}

	svcCalls := 0
	svc, err := e.OpenSession(ctx, queryengine.SessionSpec{
		AllowedCatalogsSource: func(context.Context) ([]string, error) {
			svcCalls++
			return []string{"lake"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Execute(ctx, "SELECT 2", queryengine.Callbacks{}, &collect{}); err != nil {
		t.Fatal(err)
	}
	_ = svc.Close()
	if svcCalls != 0 {
		t.Fatalf("service identity resolved the scope %d times, want 0", svcCalls)
	}
	if got := f.last().Get("X-Trino-Extra-Credential"); got != "" {
		t.Fatalf("service extra credentials = %q, want none", got)
	}
	if got := f.last().Get("X-Trino-User"); got != "svc" {
		t.Fatalf("service user = %q, want the configured service identity", got)
	}
}

func TestExecuteStreamsArrowAndReportsHandle(t *testing.T) {
	t.Parallel()
	f := &fakeCoordinator{respond: func(sql string) (http.Header, map[string]any) {
		return nil, map[string]any{
			"id":      "20260101_000000_00002_abcde",
			"columns": []map[string]string{{"name": "n", "type": "bigint"}, {"name": "d", "type": "decimal(38,10)"}},
			"data":    [][]any{{json.Number("9007199254740993"), "1.5000000000"}, {json.Number("2"), nil}},
		}
	}}
	e := testEngine(t, f)
	s, _ := e.OpenSession(context.Background(), queryengine.SessionSpec{})
	var handle queryengine.QueryHandle
	sink := &collect{}
	if err := s.Execute(context.Background(), "SELECT", queryengine.Callbacks{OnStart: func(h queryengine.QueryHandle) { handle = h }}, sink); err != nil {
		t.Fatal(err)
	}
	if handle.QueryID != "20260101_000000_00002_abcde" || handle.EngineID != "e1" {
		t.Errorf("handle = %+v", handle)
	}
	if len(sink.schemas) != 1 || sink.rows != 2 || !arrow.TypeEqual(sink.schemas[0].Field(1).Type, &arrow.Decimal128Type{Precision: 38, Scale: 10}) {
		t.Errorf("schemas %v, rows %d", sink.schemas, sink.rows)
	}
}

func TestStatementWithoutColumnsHasOneResultSet(t *testing.T) {
	t.Parallel()
	f := &fakeCoordinator{respond: func(string) (http.Header, map[string]any) {
		return nil, map[string]any{"id": "20260101_000000_00003_abcde"}
	}}
	s, _ := testEngine(t, f).OpenSession(context.Background(), queryengine.SessionSpec{})
	sink := &collect{}
	if err := s.Execute(context.Background(), "SET SESSION x = 1", queryengine.Callbacks{}, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.schemas) != 1 || sink.schemas[0].NumFields() != 0 {
		t.Fatalf("schemas = %v", sink.schemas)
	}
}

func TestQueryErrorsAreTyped(t *testing.T) {
	t.Parallel()
	f := &fakeCoordinator{respond: func(string) (http.Header, map[string]any) {
		return nil, map[string]any{"id": "x", "error": map[string]any{"message": "Table 'x' does not exist", "errorName": "TABLE_NOT_FOUND", "errorType": "USER_ERROR"}}
	}}
	s, _ := testEngine(t, f).OpenSession(context.Background(), queryengine.SessionSpec{})
	err := s.Execute(context.Background(), "SELECT * FROM x", queryengine.Callbacks{}, &collect{})
	var e *queryengine.Error
	if !errors.As(err, &e) || e.Code != queryengine.CodeNotFound || e.Native != "TABLE_NOT_FOUND" {
		t.Fatalf("err = %v", err)
	}
}

func TestQueryGone(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"NOT_SUPPORTED|Target query is not running: 20261014_183149_00019_5wabn": true,
		"NOT_FOUND|Query not found":        true,
		"NOT_SUPPORTED|something else":     false,
		"PERMISSION_DENIED|is not running": false,
	}
	for in, want := range cases {
		name, msg, _ := strings.Cut(in, "|")
		if got := queryGone(&trinoclient.QueryError{ErrorName: name, Message: msg}); got != want {
			t.Errorf("%s: %v, want %v", in, got, want)
		}
	}
}

func TestSubSecondTimeoutRoundsUp(t *testing.T) {
	t.Parallel()
	s := newSession(&Engine{}, queryengine.SessionSpec{Timeout: 300 * time.Millisecond})
	if got := s.properties["query_max_execution_time"]; got != "1s" {
		t.Fatalf("query_max_execution_time = %q, want 1s", got)
	}
	s = newSession(&Engine{}, queryengine.SessionSpec{Timeout: 90 * time.Second})
	if got := s.properties["query_max_execution_time"]; got != "90s" {
		t.Fatalf("query_max_execution_time = %q, want 90s", got)
	}
}

// TestMaintenanceStatementsWithoutRetention: an empty retention renders
// optimize only, and a bad one is still refused.
func TestMaintenanceStatementsWithoutRetention(t *testing.T) {
	t.Parallel()
	e := testEngine(t, &fakeCoordinator{})
	got, err := e.MaintenanceStatements([]string{"c", "n", "t"}, "")
	if err != nil || len(got) != 1 || got[0] != `ALTER TABLE "c"."n"."t" EXECUTE optimize` {
		t.Errorf("empty retention = %q, %v; want optimize only", got, err)
	}
	if _, err := e.MaintenanceStatements([]string{"c", "n", "t"}, "7d') --"); err == nil {
		t.Error("a bad retention was rendered")
	}
}

func TestIcebergStatement(t *testing.T) {
	t.Parallel()
	e := testEngine(t, &fakeCoordinator{})
	table := []string{"lake", "cdc", "orders"}
	clean := false
	tests := []struct {
		name string
		op   queryengine.IcebergOperation
		want string
	}{
		{"optimize with defaults", queryengine.IcebergOperation{Name: "optimize"},
			`ALTER TABLE "lake"."cdc"."orders" EXECUTE optimize`},
		{"optimize cold files of a partition", queryengine.IcebergOperation{
			Name: "optimize", FileSizeThreshold: 268435456, SortedBy: []string{"id"},
			PartitionFilter: "day < DATE '2026-05-01'", SkipFilesNewerThan: time.Hour,
		}, `ALTER TABLE "lake"."cdc"."orders" EXECUTE optimize(file_size_threshold => '268435456B', sorted_by => ARRAY['id'])` +
			` WHERE (day < DATE '2026-05-01') AND "$file_modified_time" < current_timestamp - INTERVAL '3600' SECOND`},
		{"manifests", queryengine.IcebergOperation{Name: "optimize_manifests"},
			`ALTER TABLE "lake"."cdc"."orders" EXECUTE optimize_manifests`},
		{"expiry in days with retain_last", queryengine.IcebergOperation{
			Name: "expire_snapshots", Retention: 72 * time.Hour, RetainLast: 10, CleanExpiredMetadata: &clean,
		}, `ALTER TABLE "lake"."cdc"."orders" EXECUTE expire_snapshots(retention_threshold => '3d', retain_last => 10, clean_expired_metadata => false)`},
		{"orphans rounded up to a minute", queryengine.IcebergOperation{Name: "remove_orphan_files", Retention: 90 * time.Second},
			`ALTER TABLE "lake"."cdc"."orders" EXECUTE remove_orphan_files(retention_threshold => '2m')`},
		{"orphans in hours", queryengine.IcebergOperation{Name: "remove_orphan_files", Retention: 25 * time.Hour},
			`ALTER TABLE "lake"."cdc"."orders" EXECUTE remove_orphan_files(retention_threshold => '25h')`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := e.IcebergStatement(table, tt.op)
			if err != nil || got != tt.want {
				t.Errorf("IcebergStatement() = %q, %v\nwant %q", got, err, tt.want)
			}
		})
	}
	for _, bad := range []queryengine.IcebergOperation{
		{Name: "optimize", PartitionFilter: "day IN (SELECT 1)"},
		{Name: "expire_snapshots"},
		{Name: "vacuum"},
	} {
		if got, err := e.IcebergStatement(table, bad); err == nil {
			t.Errorf("IcebergStatement(%+v) = %q, want an error", bad, got)
		}
	}
}

func TestIcebergMetricsStatements(t *testing.T) {
	t.Parallel()
	e := testEngine(t, &fakeCoordinator{})
	small, manifests, err := e.IcebergMetricsStatements([]string{"lake", "cdc", "orders"}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if small != `SELECT count(*) FROM "lake"."cdc"."orders$files" WHERE content = 0 AND file_size_in_bytes < 1024` {
		t.Errorf("small files = %q", small)
	}
	if manifests != `SELECT count(*) FROM "lake"."cdc"."orders$manifests"` {
		t.Errorf("manifests = %q", manifests)
	}
}

func TestSessionCarriesWorkloadClass(t *testing.T) {
	t.Parallel()
	f := &fakeCoordinator{respond: func(string) (http.Header, map[string]any) {
		return http.Header{}, map[string]any{"id": "20260101_000000_00001_abcde"}
	}}
	e := testEngine(t, f)
	s, err := e.OpenSession(context.Background(), queryengine.SessionSpec{
		Tags: queryengine.Tags{Source: "editor", Class: queryengine.ClassHeavy},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Execute(context.Background(), "SELECT 1", queryengine.Callbacks{}, &collect{}); err != nil {
		t.Fatal(err)
	}
	h := f.last()
	if got := h.Get("X-Trino-Client-Tags"); got != "baleia,editor,baleia.class=heavy" {
		t.Errorf("client tags = %q", got)
	}
	if got := h.Get("X-Trino-Session"); !strings.Contains(got, "query_priority=5") {
		t.Errorf("session = %q, want query_priority=5", got)
	}
}

func TestSessionSkipsQueryPriorityWhenReadOnly(t *testing.T) {
	t.Parallel()
	f := &fakeCoordinator{respond: func(string) (http.Header, map[string]any) {
		return http.Header{}, map[string]any{"id": "20260101_000000_00001_abcde"}
	}}
	e := testEngine(t, f)
	s, err := e.OpenSession(context.Background(), queryengine.SessionSpec{
		ReadOnly: true,
		Tags:     queryengine.Tags{Source: "editor", Class: queryengine.ClassInteractive},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Execute(context.Background(), "SELECT 1", queryengine.Callbacks{}, &collect{}); err != nil {
		t.Fatal(err)
	}
	h := f.last()
	// The catalog guard denies system session properties but not client tags
	// in a read-only session, so the class must still reach the selector.
	if got := h.Get("X-Trino-Client-Tags"); got != "baleia,editor,baleia.class=interactive" {
		t.Errorf("client tags = %q", got)
	}
	if got := h.Get("X-Trino-Session"); strings.Contains(got, "query_priority") {
		t.Errorf("read-only session sent query_priority: %q", got)
	}
}

func TestSessionCarriesRuntimeTimeout(t *testing.T) {
	t.Parallel()
	f := &fakeCoordinator{respond: func(string) (http.Header, map[string]any) {
		return http.Header{}, map[string]any{"id": "20260101_000000_00001_abcde"}
	}}
	e := testEngine(t, f)
	s, err := e.OpenSession(context.Background(), queryengine.SessionSpec{Timeout: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Execute(context.Background(), "SELECT 1", queryengine.Callbacks{}, &collect{}); err != nil {
		t.Fatal(err)
	}
	if got := f.last().Get("X-Trino-Session"); !strings.Contains(got, "query_max_execution_time=86400s") {
		t.Errorf("session = %q, want query_max_execution_time=86400s", got)
	}
}

// TestSessionCarriesCatalogToken (#648): the catalog token travels as the
// "token" extra credential, next to the read-only mode, and never as a header
// of its own.
func TestSessionCarriesCatalogToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		spec queryengine.SessionSpec
		want []string
		not  []string
	}{
		{"token only", queryengine.SessionSpec{CatalogToken: "user-jwt"}, []string{"token=user-jwt"}, []string{"baleia.mode"}},
		{"token and read-only", queryengine.SessionSpec{CatalogToken: "user-jwt", ReadOnly: true}, []string{"token=user-jwt", "baleia.mode=readonly"}, nil},
		{"read-only only", queryengine.SessionSpec{ReadOnly: true}, []string{"baleia.mode=readonly"}, []string{"token="}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeCoordinator{respond: func(string) (http.Header, map[string]any) {
				return http.Header{}, map[string]any{"id": "20260101_000000_00001_abcde"}
			}}
			s, err := testEngine(t, f).OpenSession(context.Background(), tt.spec)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Execute(context.Background(), "SELECT 1", queryengine.Callbacks{}, &collect{}); err != nil {
				t.Fatal(err)
			}
			got := f.last().Get("X-Trino-Extra-Credential")
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("extra credential = %q, want %q", got, w)
				}
			}
			for _, n := range tt.not {
				if strings.Contains(got, n) {
					t.Errorf("extra credential = %q, must not carry %q", got, n)
				}
			}
		})
	}
}

// TestSessionRenewsCatalogToken (#648): a script's later statements carry the
// token its source returns then, not the one from when the session opened.
func TestSessionRenewsCatalogToken(t *testing.T) {
	t.Parallel()
	f := &fakeCoordinator{respond: func(string) (http.Header, map[string]any) {
		return http.Header{}, map[string]any{"id": "20260101_000000_00001_abcde"}
	}}
	n := 0
	s, err := testEngine(t, f).OpenSession(context.Background(), queryengine.SessionSpec{
		CatalogToken: "at-open",
		CatalogTokenSource: func(context.Context) (string, error) {
			n++
			return fmt.Sprintf("renewed-%d", n), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for want := 1; want <= 2; want++ {
		if err := s.Execute(context.Background(), "SELECT 1", queryengine.Callbacks{}, &collect{}); err != nil {
			t.Fatal(err)
		}
		if got := f.last().Get("X-Trino-Extra-Credential"); !strings.Contains(got, fmt.Sprintf("token=renewed-%d", want)) {
			t.Errorf("statement %d sent %q", want, got)
		}
	}
	failing, err := testEngine(t, f).OpenSession(context.Background(), queryengine.SessionSpec{
		CatalogTokenSource: func(context.Context) (string, error) { return "", errors.New("idp down") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := failing.Execute(context.Background(), "SELECT 1", queryengine.Callbacks{}, &collect{}); err == nil {
		t.Error("a statement must fail when its token cannot be renewed")
	}
}
