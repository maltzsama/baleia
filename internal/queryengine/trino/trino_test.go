package trino

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/queryengine"
	trinoclient "github.com/galileostudio/baleia/internal/trino"
)

func TestErrorMapping(t *testing.T) {
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
	if got := queryengine.CodeOf(mapError(context.Canceled)); got != queryengine.CodeCancelled {
		t.Errorf("cancelled context → %s", got)
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
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	return New("e1", srv.URL, "svc", "adm", d)
}

func TestSessionAppliesResponseHeaders(t *testing.T) {
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
		Identity: queryengine.Identity{UserID: "u_123"},
		ReadOnly: true,
		Tags:     queryengine.Tags{ExecutionID: "exec-1", RequestID: "req-1", Source: "editor"},
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
		"X-Trino-Extra-Credential":   "baleia.mode=readonly",
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

func TestExecuteStreamsArrowAndReportsHandle(t *testing.T) {
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
