package activities

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	qtrino "github.com/galileostudio/baleia/internal/queryengine/trino"
	"github.com/jackc/pgx/v5"
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
	rowsProcessed *int64
	historyError  *string
	ownerMissing  bool
}

func (f *retrieverFakeQuerier) GetUserByID(_ context.Context, id pgtype.UUID) (db.User, error) {
	if f.ownerMissing {
		return db.User{}, pgx.ErrNoRows
	}
	return db.User{ID: id, Email: "owner@example.test"}, nil
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
	f.rowsProcessed = arg.RowsProcessed
	f.historyError = arg.ErrorMessage
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
		got, err := buildRefreshSQL((*qtrino.Engine)(nil).Dialect(), c.catalog, c.namespace, c.table, c.definition)
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
	err := runRetrieverRefresh(fakeQuery, &fakeEngine{assignID: true})
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
	err := runRetrieverRefresh(fakeQuery, &fakeEngine{err: errors.New("trino down")})
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
func runRetrieverRefresh(queries *retrieverFakeQuerier, eng *fakeEngine) error {
	before := defaultActivities
	defer func() { defaultActivities = before }()
	defaultActivities = &Activities{queries: queries, engines: eng, s3: &fakeS3{}, cfg: &config.Config{}}

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

// TestRetrieverRefresh_RunsAsTheOwner pins #141: the statement runs under the
// owner's Trino identity, never the service user, and the history records the
// number of rows the CTAS wrote (#142), not the one-row result.
func TestRetrieverRefresh_RunsAsTheOwner(t *testing.T) {
	r := fakeRetriever()
	owner := pgtype.UUID{Bytes: [16]byte{0xA1, 0xB2}, Valid: true}
	r.CreatedBy = owner
	fakeQuery := &retrieverFakeQuerier{retriever: r}
	fakeTrino := &fakeEngine{ctasCount: "25"}
	if err := runRetrieverRefresh(fakeQuery, fakeTrino); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if want := "u_" + owner.String(); fakeTrino.lastUser != want {
		t.Fatalf("statement ran as %q, want %q", fakeTrino.lastUser, want)
	}
	if fakeQuery.rowsProcessed == nil || *fakeQuery.rowsProcessed != 25 {
		t.Fatalf("rows_processed = %v, want 25", fakeQuery.rowsProcessed)
	}
}

// TestRetrieverRefresh_MissingOwnerFailsWithoutRunning: an owner that no longer
// exists records a failed run and flips the retriever to error; Trino is never
// called.
func TestRetrieverRefresh_MissingOwnerFailsWithoutRunning(t *testing.T) {
	fakeQuery := &retrieverFakeQuerier{retriever: fakeRetriever(), ownerMissing: true}
	fakeTrino := &fakeEngine{assignID: true}
	if err := runRetrieverRefresh(fakeQuery, fakeTrino); err == nil {
		t.Fatal("expected an error for a missing owner")
	}
	if fakeTrino.callCount != 0 {
		t.Fatalf("Trino was called %d times for a retriever without an owner", fakeTrino.callCount)
	}
	if fakeQuery.historyStatus != "failed" || fakeQuery.afterStatus != "error" {
		t.Fatalf("history=%q retriever=%q, want failed/error", fakeQuery.historyStatus, fakeQuery.afterStatus)
	}
	if fakeQuery.historyError == nil || !strings.Contains(*fakeQuery.historyError, "owner") {
		t.Fatalf("history error = %v, want it to name the owner", fakeQuery.historyError)
	}
}

func TestRowsWritten(t *testing.T) {
	for name, tc := range map[string]struct {
		res  [][]string
		want int64
	}{
		"ctas count":      {[][]string{{"25"}}, 25},
		"other shape":     {[][]string{{"a"}, {"b"}}, 2},
		"non numeric one": {[][]string{{"x"}}, 1},
		"no rows":         {nil, 0},
	} {
		if got := rowsWritten(tc.res); got != tc.want {
			t.Errorf("%s: %d, want %d", name, got, tc.want)
		}
	}
}
