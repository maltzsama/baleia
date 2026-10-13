package activities

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// retrieverFakeQuerier backs refresh/dispatch tests.
type retrieverFakeQuerier struct {
	db.Querier
	retriever     db.Retriever
	due           []db.Retriever
	afterStatus   string
	historyStatus string
	advanceCalled bool
	refreshType   string
}

func (f *retrieverFakeQuerier) GetRetrieverForRefresh(context.Context, pgtype.UUID) (db.Retriever, error) {
	return f.retriever, nil
}

func (f *retrieverFakeQuerier) ListRetrieversDue(context.Context) ([]db.Retriever, error) {
	return f.due, nil
}

func (f *retrieverFakeQuerier) AdvanceRetrieverSchedule(context.Context, db.AdvanceRetrieverScheduleParams) error {
	f.advanceCalled = true
	return nil
}

func (f *retrieverFakeQuerier) CreateRetrieverRefreshHistory(_ context.Context, arg db.CreateRetrieverRefreshHistoryParams) (db.RetrieverRefreshHistory, error) {
	f.refreshType = arg.RefreshType
	return db.RetrieverRefreshHistory{
		ID:          pgtype.UUID{Bytes: [16]byte{9}, Valid: true},
		RetrieverID: pgtype.UUID{Bytes: [16]byte{9}, Valid: true},
		RefreshType: arg.RefreshType,
		Status:      arg.Status,
	}, nil
}

func (f *retrieverFakeQuerier) UpdateRetrieverAfterRefresh(_ context.Context, arg db.UpdateRetrieverAfterRefreshParams) error {
	f.afterStatus = arg.Status
	return nil
}

func (f *retrieverFakeQuerier) UpdateRetrieverRefreshHistory(_ context.Context, arg db.UpdateRetrieverRefreshHistoryParams) error {
	f.historyStatus = arg.Status
	return nil
}

func fakeRetriever() db.Retriever {
	return db.Retriever{
		ID:            pgtype.UUID{Bytes: [16]byte{7}, Valid: true},
		DestCatalog:   "tpch",
		DestNamespace: "sf1",
		DestTable:     "daily_snapshot",
		DefinitionSql: "SELECT 1 AS x",
		CronSchedule:  "0 */6 * * *",
		RefreshType:   "full",
		NextRefreshAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	}
}

func TestRetrieverRefresh_QuotesIdentifiers(t *testing.T) {
	cases := []struct {
		name, catalog, namespace, table, definition, want string
	}{
		{
			name: "simple", catalog: "cat", namespace: "ns", table: "t",
			definition: "SELECT 1 x", want: `CREATE OR REPLACE TABLE "cat"."ns"."t" AS SELECT 1 x`,
		},
		{
			name: "embedded quote", catalog: "cat", namespace: "ns", table: `a"b`,
			definition: "SELECT 1 x", want: `CREATE OR REPLACE TABLE "cat"."ns"."a""b" AS SELECT 1 x`,
		},
		{
			name: "trailing semicolon stripped", catalog: "c", namespace: "n", table: "t",
			definition: "WITH base AS (SELECT 1) SELECT * FROM base;",
			want:       `CREATE OR REPLACE TABLE "c"."n"."t" AS WITH base AS (SELECT 1) SELECT * FROM base`,
		},
	}
	for _, c := range cases {
		got, err := buildRefreshSQL(c.catalog, c.namespace, c.table, c.definition)
		if err != nil {
			t.Fatalf("[%s] unexpected error: %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("[%s]\n got: %s\nwant: %s", c.name, got, c.want)
		}
	}
}

func TestRetrieverDispatch_ReturnsDue(t *testing.T) {
	a := &Activities{queries: &retrieverFakeQuerier{due: []db.Retriever{fakeRetriever()}}}

	due, err := a.retrieverDispatch(context.Background())
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("due = %d, want 1", len(due))
	}
	if due[0].ID == "" {
		t.Fatal("due retriever has no id")
	}
}

func TestRetrieverRefresh_SuccessTransitions(t *testing.T) {
	fakeQuery := &retrieverFakeQuerier{retriever: fakeRetriever()}
	err := runRetrieverRefresh(fakeQuery, &fakeTrinoClient{assignID: true})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if fakeQuery.historyStatus != "finished" {
		t.Fatalf("history status = %q, want finished", fakeQuery.historyStatus)
	}
	if fakeQuery.afterStatus != "active" {
		t.Fatalf("retriever status = %q, want active", fakeQuery.afterStatus)
	}
	if fakeQuery.refreshType != "full" {
		t.Fatalf("history refresh type = %q, want full", fakeQuery.refreshType)
	}
}

func TestRetrieverRefresh_FailureTransitions(t *testing.T) {
	fakeQuery := &retrieverFakeQuerier{retriever: fakeRetriever()}
	err := runRetrieverRefresh(fakeQuery, &fakeTrinoClient{err: errors.New("trino down")})
	if err == nil {
		t.Fatal("expected a refresh error")
	}
	if fakeQuery.historyStatus != "failed" {
		t.Fatalf("history status = %q, want failed", fakeQuery.historyStatus)
	}
	if fakeQuery.afterStatus != "error" {
		t.Fatalf("retriever status = %q, want error", fakeQuery.afterStatus)
	}
}

// runRetrieverRefresh executes the registered activity so activity.GetLogger /
// GetInfo have a real activity context.
func runRetrieverRefresh(queries *retrieverFakeQuerier, trino trinoClient) error {
	before := defaultActivities
	defer func() { defaultActivities = before }()
	defaultActivities = &Activities{queries: queries, trino: trino, s3: &fakeS3{}, cfg: &config.Config{}}

	wts := &testsuite.WorkflowTestSuite{}
	ts := wts.NewTestActivityEnvironment()
	ts.RegisterActivityWithOptions(RetrieverRefresh, activity.RegisterOptions{Name: "RetrieverRefresh"})

	_, err := ts.ExecuteActivity(RetrieverRefresh, RetrieverRefreshParams{
		RetrieverID: fakeRetriever().ID.String(),
		RefreshType: "full",
	})
	return err
}

func TestAdvanceRetrieverSchedule_ComputesNext(t *testing.T) {
	fakeQuery := &retrieverFakeQuerier{retriever: fakeRetriever()}
	a := &Activities{queries: fakeQuery}

	err := a.advanceRetrieverSchedule(context.Background(), AdvanceRetrieverScheduleParams{
		RetrieverID: fakeRetriever().ID.String(),
	})
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if !fakeQuery.advanceCalled {
		t.Fatal("AdvanceRetrieverSchedule was not called")
	}
}
