package api

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// addStatement inserts one finished statement with an engine query id.
func addStatement(t *testing.T, f *historyFixture, execution openapi_types.UUID, queryID string) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO query_statements
		  (execution_id, position, sql, status, engine_query_id, rows_returned, duration_ms, result_bytes, started_at, finished_at)
		VALUES ($1,1,'SELECT 1','finished',$2,10,100,2048,now(),now())`,
		pgUUID(execution), queryID); err != nil {
		t.Fatal(err)
	}
}

// setTarget changes the execution's target snapshot (the fixture writes an
// empty one).
func setTarget(t *testing.T, f *historyFixture, execution openapi_types.UUID, engineKind string) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `
		UPDATE query_executions SET target_snapshot = jsonb_build_object('engine_kind', $2::text) WHERE id = $1`,
		pgUUID(execution), engineKind); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionDetails_Trino(t *testing.T) {
	t.Parallel()
	f := newHistoryFixture(t)
	execution := f.addExecution(t, f.owner, "finished", "SELECT 1", time.Now())
	setTarget(t, f, execution, "trino")
	addStatement(t, f, execution, "q1")
	payload := `{"source":"query_completed_event","state":"FINISHED","queuedMs":10,"analysisMs":3,` +
		`"planningMs":20,"executionMs":65,"cpuMs":50,"peakMemoryBytes":4096,"spilledBytes":0,` +
		`"inputBytes":5000,"inputRows":100,"outputBytes":200,"outputRows":10,` +
		`"inputs":[{"catalog":"lake","schema":"sales","table":"orders"}]}`
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO trino_query_snapshots (trino_query_id, engine_id, origin, "user", state, sql, created_at, raw_trino_payload)
		VALUES ('q1',$1,'trino_direct','u','finished','SELECT 1',now(),$2::jsonb)`, pgUUID(f.engine), payload); err != nil {
		t.Fatal(err)
	}

	resp, err := f.h.GetExecutionDetails(ctxAs(f.owner.String(), "member"), GetExecutionDetailsRequestObject{
		Id: f.ws, ExecutionId: execution,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := resp.(GetExecutionDetails200JSONResponse)
	if !ok {
		t.Fatalf("response = %T", resp)
	}
	if got.EngineKind == nil || *got.EngineKind != "trino" {
		t.Fatalf("engine kind = %v", got.EngineKind)
	}
	if len(got.Statements) != 1 || got.Statements[0].Profile == nil {
		t.Fatalf("statements = %+v", got.Statements)
	}
	p := got.Statements[0].Profile
	if p.QueuedMs == nil || *p.QueuedMs != 10 || p.PlanningMs == nil || *p.PlanningMs != 20 || p.ExecutionMs == nil || *p.ExecutionMs != 65 {
		t.Fatalf("profile timeline = %+v", p)
	}
	if p.Inputs == nil || len(*p.Inputs) != 1 || (*p.Inputs)[0].Table == nil || *(*p.Inputs)[0].Table != "orders" {
		t.Fatalf("profile inputs = %+v", p.Inputs)
	}
	// The engine phases add up to the run's duration within 5% (#532).
	sum := *p.QueuedMs + *p.PlanningMs + *p.ExecutionMs
	total := *got.Timeline.TotalMs
	if diff := sum - total; diff < -5 || diff > 5 {
		t.Fatalf("timeline %d ms vs total %d ms", sum, total)
	}
	if got.Timeline.EngineQueuedMs == nil || *got.Timeline.EngineQueuedMs != 10 {
		t.Fatalf("timeline = %+v", got.Timeline)
	}
}

func TestExecutionDetails_DirectEngine(t *testing.T) {
	t.Parallel()
	f := newHistoryFixture(t)
	execution := f.addExecution(t, f.owner, "finished", "SELECT 1", time.Now())
	setTarget(t, f, execution, "postgresql")
	addStatement(t, f, execution, "")

	resp, err := f.h.GetExecutionDetails(ctxAs(f.owner.String(), "member"), GetExecutionDetailsRequestObject{
		Id: f.ws, ExecutionId: execution,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := resp.(GetExecutionDetails200JSONResponse)
	if got.EngineKind == nil || *got.EngineKind != "postgresql" {
		t.Fatalf("engine kind = %v", got.EngineKind)
	}
	if len(got.Statements) != 1 || got.Statements[0].Profile != nil {
		t.Fatalf("a direct engine has no profile: %+v", got.Statements)
	}
	s := got.Statements[0]
	if s.RowsReturned == nil || *s.RowsReturned != 10 || s.DurationMs == nil || *s.DurationMs != 100 || s.ResultBytes == nil || *s.ResultBytes != 2048 {
		t.Fatalf("statement rows/duration = %+v", s)
	}
	if got.Timeline.EngineQueuedMs != nil || got.Timeline.PlanningMs != nil || got.Timeline.ExecutionMs != nil {
		t.Fatalf("direct engine timeline = %+v", got.Timeline)
	}
}

func TestExecutionDetails_ForeignWorkspace401(t *testing.T) {
	t.Parallel()
	f := newHistoryFixture(t)
	execution := f.addExecution(t, f.owner, "finished", "SELECT 1", time.Now())
	var outsider pgtype.UUID
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO users (name, email, password_hash) VALUES ('outsider','out@x','x') RETURNING id`).Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	resp, err := f.h.GetExecutionDetails(ctxAs(openapi_types.UUID(outsider.Bytes).String(), "member"), GetExecutionDetailsRequestObject{
		Id: f.ws, ExecutionId: execution,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(GetExecutionDetails401JSONResponse); !ok {
		t.Fatalf("response = %T, want 401", resp)
	}
}
