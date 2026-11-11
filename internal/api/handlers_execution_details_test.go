package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// addStatement inserts one finished statement with an engine query id.
func addStatement(t *testing.T, f *historyFixture, execution openapi_types.UUID, queryID string) {
	t.Helper()
	rows, duration, resultBytes := int64(10), int64(100), int64(2048)
	var engineQueryID *string
	if queryID != "" {
		engineQueryID = &queryID
	}
	now := time.Now()
	var parent model.QueryExecution
	if err := f.gdb.Where("id = ?", execution.String()).First(&parent).Error; err != nil {
		t.Fatal(err)
	}
	row := model.QueryStatement{
		ID: model.UUID(repo.NewUUID()), ExecutionID: model.UUID(execution.String()),
		ExecutionStartedAt: parent.StartedAt, Position: 1, SQL: "SELECT 1", Status: "finished", EngineQueryID: engineQueryID,
		RowsReturned: &rows, DurationMs: &duration, ResultBytes: &resultBytes,
		StartedAt: now, FinishedAt: &now, ResultSets: model.JSON(`[]`),
	}
	if err := f.gdb.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
}

// setTarget changes the execution's target snapshot (the fixture writes an
// empty one).
func setTarget(t *testing.T, f *historyFixture, execution openapi_types.UUID, engineKind string) {
	t.Helper()
	snapshot := model.JSON(fmt.Sprintf(`{"engine_kind":%q}`, engineKind))
	if err := f.gdb.Model(&model.QueryExecution{}).
		Where("id = ?", execution.String()).
		Update("target_snapshot", snapshot).Error; err != nil {
		t.Fatal(err)
	}
}

func TestExecutionDetails_Trino(t *testing.T) {
	t.Parallel()
	f := newHistoryFixture(t)
	execution := f.addExecution(t, f.owner, "finished", "SELECT 1", time.Now())
	setTarget(t, f, execution, "trino")
	addStatement(t, f, execution, "q1")
	payload := model.JSON(`{"source":"query_completed_event","state":"FINISHED","queuedMs":10,"analysisMs":3,` +
		`"planningMs":20,"executionMs":65,"cpuMs":50,"peakMemoryBytes":4096,"spilledBytes":0,` +
		`"inputBytes":5000,"inputRows":100,"outputBytes":200,"outputRows":10,` +
		`"inputs":[{"catalog":"lake","schema":"sales","table":"orders"}]}`)
	if err := f.gdb.Create(&model.TrinoQuerySnapshot{
		ID: model.UUID(repo.NewUUID()), TrinoQueryID: "q1",
		EngineID: model.UUID(f.engine.String()), Origin: "trino_direct", User: "u",
		State: "finished", SQL: "SELECT 1", CreatedAt: time.Now(), RawTrinoPayload: &payload,
	}).Error; err != nil {
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
	password := "x"
	outsiderUser := model.User{
		ID: model.UUID(repo.NewUUID()), Name: "outsider", Email: "out@x", PasswordHash: &password,
	}
	if err := f.gdb.Create(&outsiderUser).Error; err != nil {
		t.Fatal(err)
	}
	resp, err := f.h.GetExecutionDetails(ctxAs(string(outsiderUser.ID), "member"), GetExecutionDetailsRequestObject{
		Id: f.ws, ExecutionId: execution,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(GetExecutionDetails401JSONResponse); !ok {
		t.Fatalf("response = %T, want 401", resp)
	}
}
