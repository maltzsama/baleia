package workflows

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sync"
	"testing"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// TestNoInfiniteLoops is an AST lint: recurring workflows are driven by
// Temporal Schedules and must do a single pass. A `for {}` loop without a
// ContinueAsNewError blows the event history until Temporal terminates the
// workflow, so one appearing in this package fails the build.
func TestNoInfiniteLoops(t *testing.T) {
	t.Parallel()
	fset, files, err := parseWorkflowSources()
	require.NoError(t, err)

	for _, f := range files {
		forEachInfiniteLoop(fset, f, func(pos string) {
			t.Errorf("%s: infinite loop without workflow.NewContinueAsNewError", pos)
		})
	}
}

// TestLensQueryPoll_SinglePass documents the schedule pattern: the poller does
// exactly one activity call and returns.
func TestLensQueryPoll_SinglePass(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	var calls int
	env.OnActivity(activities.LensPollTrinoQueries, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, p activities.LensPollTrinoQueriesParams) error {
			calls++
			return nil
		})

	env.ExecuteWorkflow(LensQueryPollWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, 1, calls, "a single-pass schedule workflow runs its activity once")
}

// TestLensTableHealth_FanOut pins the fan-out: every enabled Iceberg catalog is
// analyzed, and one failing catalog does not fail the others.
func TestLensTableHealth_FanOut(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.OnActivity(activities.ListEnabledIcebergCatalogs, mock.Anything, mock.Anything).
		Return([]activities.EnabledIcebergCatalog{
			// cat1 and cat3 share a name on two connections: their rounds
			// still get distinct child workflow IDs.
			{ID: "id-1", CatalogConnectionID: "conn-1", Catalog: "lake"},
			{ID: "id-2", CatalogConnectionID: "conn-2", Catalog: "cat2"},
			{ID: "id-3", CatalogConnectionID: "conn-3", Catalog: "lake"},
		}, nil)

	var analyzed []activities.LensAnalyzeTableHealthParams
	var mu sync.Mutex
	env.OnActivity(activities.LensAnalyzeTableHealth, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, p activities.LensAnalyzeTableHealthParams) error {
			mu.Lock()
			analyzed = append(analyzed, p)
			mu.Unlock()
			if p.Catalog == "cat2" {
				return errors.New("catalog unreachable")
			}
			return nil
		})

	env.RegisterWorkflow(MaintenanceRoundWorkflow)
	var rounds []string
	env.OnActivity(activities.PlanMaintenanceRound, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, p activities.PlanMaintenanceRoundParams) ([]activities.PlannedRun, error) {
			mu.Lock()
			rounds = append(rounds, p.CatalogID)
			mu.Unlock()
			return nil, nil
		})

	env.ExecuteWorkflow(LensTableHealthWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError(), "a failing catalog must not fail the run")
	require.Len(t, analyzed, 3)
	// #500: a round follows every catalog analyzed, never a failed one.
	require.Len(t, rounds, 2)
}

func parseWorkflowSources() (*token.FileSet, []*ast.File, error) {
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		return nil, nil, err
	}
	var files []*ast.File
	for _, name := range names {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			return nil, nil, err
		}
		if f.Name.Name == "workflows" {
			files = append(files, f)
		}
	}
	return fset, files, nil
}

// forEachInfiniteLoop calls fail for every `for {}` whose body has no
// NewContinueAsNewError call.
func forEachInfiniteLoop(fset *token.FileSet, f *ast.File, fail func(pos string)) {
	ast.Inspect(f, func(n ast.Node) bool {
		forStmt, ok := n.(*ast.ForStmt)
		if !ok || forStmt.Cond != nil {
			return true
		}
		if !hasContinueAsNewError(forStmt) {
			fail(fset.Position(forStmt.Pos()).String())
		}
		return true
	})
}

func hasContinueAsNewError(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		call, ok := x.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "NewContinueAsNewError" {
			found = true
			return false
		}
		return true
	})
	return found
}

// TestLensTableHealth_RecordsPassLast: the schedule's success is recorded
// once, after every catalog's analysis, even when one of them failed.
func TestLensTableHealth_RecordsPassLast(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.OnActivity(activities.ListEnabledIcebergCatalogs, mock.Anything, mock.Anything).
		Return([]activities.EnabledIcebergCatalog{{Catalog: "cat1"}, {Catalog: "cat2"}}, nil)
	var mu sync.Mutex
	var events []string
	record := func(e string) { mu.Lock(); defer mu.Unlock(); events = append(events, e) }
	env.OnActivity(activities.LensAnalyzeTableHealth, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, p activities.LensAnalyzeTableHealthParams) error {
			record("analyze")
			if p.Catalog == "cat2" {
				return errors.New("catalog unreachable")
			}
			return nil
		})
	env.RegisterWorkflow(MaintenanceRoundWorkflow)
	env.OnActivity(activities.PlanMaintenanceRound, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, p activities.PlanMaintenanceRoundParams) ([]activities.PlannedRun, error) {
			record("round")
			return nil, nil
		})
	env.OnActivity(activities.SchedulePassSucceeded, mock.Anything, activities.SchedulePassSucceededParams{Schedule: schedules.LensTableHealth}).
		Return(func(ctx context.Context, p activities.SchedulePassSucceededParams) error {
			record("pass")
			return nil
		}).Once()

	env.ExecuteWorkflow(LensTableHealthWorkflow)

	require.NoError(t, env.GetWorkflowError())
	require.Len(t, events, 4)
	require.Equal(t, "pass", events[3], "the pass is recorded after every analysis and round")
	require.ElementsMatch(t, []string{"analyze", "analyze", "round", "pass"}, events)
}

// TestLensTableHealth_NoPassWhenListingFails: a pass that failed is not
// recorded as a success.
func TestLensTableHealth_NoPassWhenListingFails(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.OnActivity(activities.ListEnabledIcebergCatalogs, mock.Anything, mock.Anything).
		Return(nil, errors.New("database down"))
	env.OnActivity(activities.SchedulePassSucceeded, mock.Anything, mock.Anything).Return(nil).Never()

	env.ExecuteWorkflow(LensTableHealthWorkflow)

	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}
