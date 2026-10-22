package activities

import (
	"context"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// runMaintenance executes the activity in a Temporal test environment (the
// activity uses the SDK logger and heartbeats, which need an activity context).
func runMaintenance(t *testing.T, a *Activities, p LensRunMaintenanceParams) error {
	t.Helper()
	old := defaultActivities
	defaultActivities = a
	defer func() { defaultActivities = old }()
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(LensRunMaintenance, activity.RegisterOptions{Name: "LensRunMaintenance"})
	_, err := env.ExecuteActivity(LensRunMaintenance, p)
	return err
}

type snapshotSink struct{ db.Querier }

func (snapshotSink) UpsertTrinoQuerySnapshot(context.Context, db.UpsertTrinoQuerySnapshotParams) (db.TrinoQuerySnapshot, error) {
	return db.TrinoQuerySnapshot{}, nil
}

const maintExecID = "00000000-0000-0000-0000-00000000abcd"

// TestLensRunMaintenance_QuotesIdentifiersAndRetention: every identifier is a
// quoted Trino identifier and the retention a string literal.
func TestLensRunMaintenance_QuotesIdentifiersAndRetention(t *testing.T) {
	rec := &fakeEngine{Engine: testTrino}
	a := &Activities{engines: rec, queries: snapshotSink{}}
	err := runMaintenance(t, a, LensRunMaintenanceParams{
		ExecutionID: maintExecID, Catalog: "cat", Namespace: `we"ird`, Table: "t; DROP TABLE x", Retention: "7d",
	})
	if err != nil {
		t.Fatalf("lensRunMaintenance: %v", err)
	}
	want := []string{
		`ALTER TABLE "cat"."we""ird"."t; DROP TABLE x" EXECUTE optimize`,
		`ALTER TABLE "cat"."we""ird"."t; DROP TABLE x" EXECUTE expire_snapshots(retention_threshold => '7d')`,
		`ALTER TABLE "cat"."we""ird"."t; DROP TABLE x" EXECUTE remove_orphan_files(retention_threshold => '7d')`,
	}
	if len(rec.sqls) != 3 {
		t.Fatalf("statements = %q", rec.sqls)
	}
	for i := range want {
		if rec.sqls[i] != want[i] {
			t.Errorf("statement %d = %s\nwant %s", i, rec.sqls[i], want[i])
		}
	}
}

// TestLensRunMaintenance_RejectsBadInputBeforeTrino: the workflow input is not a
// trust boundary; nothing reaches Trino for a bad retention or identifier.
func TestLensRunMaintenance_RejectsBadInputBeforeTrino(t *testing.T) {
	for name, p := range map[string]LensRunMaintenanceParams{
		"retention injection": {ExecutionID: maintExecID, Catalog: "c", Namespace: "n", Table: "t", Retention: "7d') --"},
		"empty retention":     {ExecutionID: maintExecID, Catalog: "c", Namespace: "n", Table: "t"},
		"empty table":         {ExecutionID: maintExecID, Catalog: "c", Namespace: "n", Retention: "7d"},
		"NUL in namespace":    {ExecutionID: maintExecID, Catalog: "c", Namespace: "n\x00", Table: "t", Retention: "7d"},
	} {
		rec := &fakeEngine{Engine: testTrino}
		a := &Activities{engines: rec, queries: snapshotSink{}}
		err := runMaintenance(t, a, p)
		if err == nil || len(rec.sqls) != 0 {
			t.Errorf("%s: err=%v statements=%q, want an error and no statements", name, err, rec.sqls)
		}
		if err != nil && strings.Contains(err.Error(), "DROP") {
			t.Errorf("%s: error echoes the input: %v", name, err)
		}
	}
}
