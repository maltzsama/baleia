package workflows

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// TestNoInfiniteLoops is an AST lint: recurring workflows are driven by
// Temporal Schedules and must do a single pass. A `for {}` loop without a
// ContinueAsNewError blows the event history until Temporal terminates the
// workflow, so one appearing in this package fails the build.
func TestNoInfiniteLoops(t *testing.T) {
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
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.OnActivity(activities.ListEnabledIcebergCatalogs, mock.Anything, mock.Anything).
		Return([]activities.EnabledIcebergCatalog{
			{CatalogConnectionID: "conn-1", Catalog: "cat1"},
			{CatalogConnectionID: "conn-2", Catalog: "cat2"},
			{CatalogConnectionID: "conn-3", Catalog: "cat3"},
		}, nil)

	var analyzed []activities.LensAnalyzeTableHealthParams
	env.OnActivity(activities.LensAnalyzeTableHealth, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, p activities.LensAnalyzeTableHealthParams) error {
			analyzed = append(analyzed, p)
			if p.Catalog == "cat2" {
				return errors.New("catalog unreachable")
			}
			return nil
		})

	env.ExecuteWorkflow(LensTableHealthWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError(), "a failing catalog must not fail the run")
	require.Len(t, analyzed, 3)
}

func parseWorkflowSources() (*token.FileSet, []*ast.File, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		return nil, nil, err
	}
	var files []*ast.File
	for _, pkg := range pkgs {
		if pkg.Name != "workflows" {
			continue
		}
		for _, f := range pkg.Files {
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
