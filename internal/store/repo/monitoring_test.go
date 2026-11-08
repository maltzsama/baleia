package repo

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/monitoring/queryevents"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// monitoringFixture is the workbench fixture's catalog, binding and engine,
// which the monitoring and maintenance tables reference.
func monitoringFixture(t *testing.T) *workbenchFixture {
	t.Helper()
	return newWorkbenchFixture(t)
}

func (f *workbenchFixture) catalogConnection(t *testing.T) model.UUID {
	t.Helper()
	var catalog model.Catalog
	if err := f.gdb.Where("id = ?", string(f.catalog)).First(&catalog).Error; err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	return catalog.ConnectionID
}

// TestWorkloadRulesCRUD covers create, ordering by position, update and delete
// over the GORM store (#539, #773).
func TestWorkloadRulesCRUD(t *testing.T) {
	t.Parallel()
	f := monitoringFixture(t)
	ctx := context.Background()

	second, err := f.r.CreateWorkloadRule(ctx, db.CreateWorkloadRuleParams{
		Position: 2, Enabled: true, Match: []byte(`{"role":"admin"}`),
		Class: "heavy", Weight: 3, CreatedBy: f.ownerID(),
	})
	if err != nil {
		t.Fatalf("CreateWorkloadRule(second): %v", err)
	}
	first, err := f.r.CreateWorkloadRule(ctx, db.CreateWorkloadRuleParams{
		Position: 1, Enabled: false, Match: []byte(`{}`),
		Class: "interactive", Weight: 1, CreatedBy: f.ownerID(),
	})
	if err != nil {
		t.Fatalf("CreateWorkloadRule(first): %v", err)
	}

	rows, err := f.r.ListWorkloadRules(ctx)
	if err != nil {
		t.Fatalf("ListWorkloadRules: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != first.ID || rows[1].ID != second.ID {
		t.Fatalf("ListWorkloadRules = %+v; want position order", rows)
	}
	if rows[0].Enabled {
		t.Fatal("CreateWorkloadRule stored enabled=true for an explicitly disabled rule")
	}
	if maxPosition, err := f.r.MaxWorkloadRulePosition(ctx); err != nil || maxPosition != 2 {
		t.Fatalf("MaxWorkloadRulePosition = %d, %v; want 2", maxPosition, err)
	}
	got, err := f.r.GetWorkloadRule(ctx, first.ID)
	if err != nil || got.Class != "interactive" || !got.CreatedAt.Valid || !got.UpdatedAt.Valid {
		t.Fatalf("GetWorkloadRule = %+v, %v", got, err)
	}
	if _, err := f.r.GetWorkloadRule(ctx, PGUUID(NewUUID())); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetWorkloadRule(missing) = %v, want ErrNoRows", err)
	}

	updated, err := f.r.UpdateWorkloadRule(ctx, db.UpdateWorkloadRuleParams{
		ID: first.ID, Position: 0, Enabled: false, Match: []byte(`{"origin":"cli"}`),
		Class: "background", MaxClass: strPtr("background"), Weight: 5,
	})
	if err != nil {
		t.Fatalf("UpdateWorkloadRule: %v", err)
	}
	if updated.Position != 0 || updated.Enabled || updated.Class != "background" ||
		updated.MaxClass == nil || *updated.MaxClass != "background" || updated.Weight != 5 {
		t.Fatalf("UpdateWorkloadRule = %+v", updated)
	}
	if _, err := f.r.UpdateWorkloadRule(ctx, db.UpdateWorkloadRuleParams{ID: PGUUID(NewUUID()), Position: 1}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("UpdateWorkloadRule(missing) = %v, want ErrNoRows", err)
	}

	if n, err := f.r.DeleteWorkloadRule(ctx, first.ID); err != nil || n != 1 {
		t.Fatalf("DeleteWorkloadRule = %d, %v", n, err)
	}
	if n, err := f.r.DeleteWorkloadRule(ctx, first.ID); err != nil || n != 0 {
		t.Fatalf("DeleteWorkloadRule(again) = %d, %v", n, err)
	}
	if rows, err := f.r.ListWorkloadRules(ctx); err != nil || len(rows) != 1 || rows[0].ID != second.ID {
		t.Fatalf("ListWorkloadRules(after delete) = %+v, %v", rows, err)
	}
}

// TestTableHealthUpsert covers the health upsert, the last-maintenance
// preservation, the single-table read and the filtered catalog list (#117, #773).
func TestTableHealthUpsert(t *testing.T) {
	t.Parallel()
	f := monitoringFixture(t)
	ctx := context.Background()
	connection := f.catalogConnection(t)

	health := func(score int32, needsCompaction, needsExpiry bool, due []string) db.UpsertTableHealthSnapshotParams {
		return db.UpsertTableHealthSnapshotParams{
			CatalogID: f.catalogID(), CatalogConnectionID: PGUUID(string(connection)),
			Catalog: "lake", Namespace: "sales", TableName: "orders",
			SnapshotCount: 3, FileCount: int32Ptr(12), TotalSizeBytes: int64Ptr(2048),
			OldestSnapshotAgeHours: int32Ptr(10), HealthScore: score,
			NeedsCompaction: needsCompaction, NeedsSnapshotExpiry: needsExpiry,
			TableProperties: []byte(`{"format":"iceberg"}`), DueOperations: due,
		}
	}
	first := health(40, true, true, []string{"optimize", "expire_snapshots"})
	first.LastMaintenanceAt = PGTime(time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond))
	stored, err := f.r.UpsertTableHealthSnapshot(ctx, first)
	if err != nil {
		t.Fatalf("UpsertTableHealthSnapshot(create): %v", err)
	}
	if stored.HealthScore != 40 || !stored.NeedsCompaction || len(stored.DueOperations) != 2 {
		t.Fatalf("UpsertTableHealthSnapshot = %+v", stored)
	}
	if _, err := f.r.UpsertTableHealthSnapshot(ctx, health(90, false, false, nil)); err != nil {
		t.Fatalf("UpsertTableHealthSnapshot(update): %v", err)
	}
	got, err := f.r.GetTableHealthByTable(ctx, db.GetTableHealthByTableParams{
		CatalogID: f.catalogID(), Namespace: "sales", TableName: "orders",
	})
	if err != nil {
		t.Fatalf("GetTableHealthByTable: %v", err)
	}
	if got.HealthScore != 90 || got.NeedsCompaction || !got.LastMaintenanceAt.Valid ||
		!got.LastMaintenanceAt.Time.Equal(first.LastMaintenanceAt.Time) {
		t.Fatalf("GetTableHealthByTable = %+v; want score 90 and the preserved maintenance time %v", got, first.LastMaintenanceAt.Time)
	}

	// A second table gives the list something to order and filter.
	if _, err := f.r.UpsertTableHealthSnapshot(ctx, db.UpsertTableHealthSnapshotParams{
		CatalogID: f.catalogID(), CatalogConnectionID: PGUUID(string(connection)),
		Catalog: "lake", Namespace: "marketing", TableName: "visits",
		HealthScore: 0, NeedsSnapshotExpiry: true, TableProperties: []byte(`{}`),
	}); err != nil {
		t.Fatalf("UpsertTableHealthSnapshot(second): %v", err)
	}
	list, err := f.r.ListTableHealthByCatalog(ctx, db.ListTableHealthByCatalogParams{
		CatalogID: f.catalogID(), Lim: 10,
	})
	if err != nil || len(list) != 2 || list[0].TableName != "visits" || list[0].HealthScore != 0 {
		t.Fatalf("ListTableHealthByCatalog = %+v, %v; want score order and a stored zero", list, err)
	}
	needs, err := f.r.ListTableHealthByCatalog(ctx, db.ListTableHealthByCatalogParams{
		CatalogID: f.catalogID(), Needs: strPtr("snapshot_expiry"), Lim: 10,
	})
	if err != nil || len(needs) != 1 || needs[0].TableName != "visits" {
		t.Fatalf("ListTableHealthByCatalog(needs) = %+v, %v", needs, err)
	}
	ns, err := f.r.ListTableHealthByCatalog(ctx, db.ListTableHealthByCatalogParams{
		CatalogID: f.catalogID(), Ns: strPtr("sales"), Lim: 10,
	})
	if err != nil || len(ns) != 1 || ns[0].TableName != "orders" {
		t.Fatalf("ListTableHealthByCatalog(ns) = %+v, %v", ns, err)
	}
	if _, err := f.r.GetTableHealthByTable(ctx, db.GetTableHealthByTableParams{
		CatalogID: f.catalogID(), Namespace: "nope", TableName: "none",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetTableHealthByTable(missing) = %v, want ErrNoRows", err)
	}
}

// TestTableMaintenancePolicies covers create/duplicate/get/update revision
// fencing/delete (#500, #773).
func TestTableMaintenancePolicies(t *testing.T) {
	t.Parallel()
	f := monitoringFixture(t)
	ctx := context.Background()

	create := db.CreateTableMaintenancePolicyParams{
		CatalogID: f.catalogID(), Namespace: "sales", TableName: "orders", Mode: "managed",
		WriterCommitIntervalSeconds: 60, Optimize: []byte(`{"enabled":true}`),
		OptimizeManifests: []byte(`{}`), ExpireSnapshots: []byte(`{"retention_seconds":604800}`),
		RemoveOrphanFiles: []byte(`{}`), Actor: f.ownerID(),
	}
	created, err := f.r.CreateTableMaintenancePolicy(ctx, create)
	if err != nil {
		t.Fatalf("CreateTableMaintenancePolicy: %v", err)
	}
	if created.Revision != 1 || created.Mode != "managed" || !created.CreatedBy.Valid || !created.UpdatedBy.Valid {
		t.Fatalf("CreateTableMaintenancePolicy = %+v", created)
	}
	if _, err := f.r.CreateTableMaintenancePolicy(ctx, create); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("CreateTableMaintenancePolicy(duplicate) = %v, want ErrNoRows", err)
	}
	got, err := f.r.GetTableMaintenancePolicy(ctx, db.GetTableMaintenancePolicyParams{
		CatalogID: f.catalogID(), Namespace: "sales", TableName: "orders",
	})
	if err != nil || got.Revision != 1 || string(got.Optimize) != `{"enabled": true}` {
		t.Fatalf("GetTableMaintenancePolicy = %+v, %v", got, err)
	}

	updated, err := f.r.UpdateTableMaintenancePolicy(ctx, db.UpdateTableMaintenancePolicyParams{
		CatalogID: f.catalogID(), Namespace: "sales", TableName: "orders", Mode: "off",
		WriterCommitIntervalSeconds: 30, Optimize: []byte(`{}`), OptimizeManifests: []byte(`{}`),
		ExpireSnapshots: []byte(`{}`), RemoveOrphanFiles: []byte(`{}`), Actor: f.memberID(), Revision: 1,
	})
	if err != nil {
		t.Fatalf("UpdateTableMaintenancePolicy: %v", err)
	}
	if updated.Revision != 2 || updated.Mode != "off" || updated.WriterCommitIntervalSeconds != 30 {
		t.Fatalf("UpdateTableMaintenancePolicy = %+v", updated)
	}
	if _, err := f.r.UpdateTableMaintenancePolicy(ctx, db.UpdateTableMaintenancePolicyParams{
		CatalogID: f.catalogID(), Namespace: "sales", TableName: "orders", Mode: "off", Revision: 1,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("UpdateTableMaintenancePolicy(stale revision) = %v, want ErrNoRows", err)
	}
	deleted, err := f.r.DeleteTableMaintenancePolicy(ctx, db.DeleteTableMaintenancePolicyParams{
		CatalogID: f.catalogID(), Namespace: "sales", TableName: "orders",
	})
	if err != nil || deleted.Revision != 2 {
		t.Fatalf("DeleteTableMaintenancePolicy = %+v, %v", deleted, err)
	}
	if _, err := f.r.GetTableMaintenancePolicy(ctx, db.GetTableMaintenancePolicyParams{
		CatalogID: f.catalogID(), Namespace: "sales", TableName: "orders",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetTableMaintenancePolicy(after delete) = %v, want ErrNoRows", err)
	}
}

// TestMaintenanceRuns covers the manual and planned run writes, the running
// reads, the list filters and the catalog planning aggregates (#500, #773).
func TestMaintenanceRuns(t *testing.T) {
	t.Parallel()
	f := monitoringFixture(t)
	ctx := context.Background()

	run, err := f.r.CreateMaintenanceRun(ctx, db.CreateMaintenanceRunParams{
		CatalogID: f.catalogID(), BindingID: f.bindingID(), Namespace: "sales", TableName: "orders",
		Operations: []string{"optimize"}, Retention: strPtr("7d"), StartedBy: f.ownerID(),
		WorkflowID: "maintenance-test",
	})
	if err != nil {
		t.Fatalf("CreateMaintenanceRun: %v", err)
	}
	if run.Status != "running" || run.Trigger != "manual" || run.Attempts != 1 || run.FinishedAt.Valid {
		t.Fatalf("CreateMaintenanceRun = %+v", run)
	}
	running, err := f.r.GetRunningMaintenanceRun(ctx, db.GetRunningMaintenanceRunParams{
		CatalogID: f.catalogID(), Namespace: "sales", TableName: "orders",
	})
	if err != nil || running.ID != run.ID {
		t.Fatalf("GetRunningMaintenanceRun = %+v, %v", running, err)
	}
	finished, err := f.r.UpdateMaintenanceRun(ctx, db.UpdateMaintenanceRunParams{
		ID: run.ID, Status: "finished",
	})
	if err != nil || finished.Status != "finished" || !finished.FinishedAt.Valid {
		t.Fatalf("UpdateMaintenanceRun = %+v, %v", finished, err)
	}
	if _, err := f.r.GetRunningMaintenanceRun(ctx, db.GetRunningMaintenanceRunParams{
		CatalogID: f.catalogID(), Namespace: "sales", TableName: "orders",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetRunningMaintenanceRun(finished) = %v, want ErrNoRows", err)
	}

	auto, err := f.r.CreatePlannedMaintenanceRun(ctx, db.CreatePlannedMaintenanceRunParams{
		CatalogID: f.catalogID(), BindingID: f.bindingID(), Namespace: "sales", TableName: "orders",
		Operations: []string{"optimize_manifests"}, Trigger: "automatic", WorkflowID: "auto",
		Reason: []byte(`{"fired":"small_files"}`), Parameters: []byte(`{"enabled":true}`),
	})
	if err != nil {
		t.Fatalf("CreatePlannedMaintenanceRun: %v", err)
	}
	dry, err := f.r.CreatePlannedMaintenanceRun(ctx, db.CreatePlannedMaintenanceRunParams{
		CatalogID: f.catalogID(), Namespace: "sales", TableName: "orders",
		Operations: []string{}, Trigger: "dry_run", WorkflowID: "",
		Reason: []byte(`{}`), Parameters: []byte(`{}`),
	})
	if err != nil || dry.Status != "dry_run" || !dry.FinishedAt.Valid {
		t.Fatalf("CreatePlannedMaintenanceRun(dry) = %+v, %v", dry, err)
	}
	if _, err := f.r.CreatePlannedMaintenanceRun(ctx, db.CreatePlannedMaintenanceRunParams{
		CatalogID: f.catalogID(), Namespace: "sales", TableName: "orders",
		Operations: []string{"optimize"}, Trigger: "automatic", WorkflowID: "dup",
		Reason: []byte(`{}`), Parameters: []byte(`{}`),
	}); err == nil {
		t.Fatal("a second running run on the table must be refused")
	}

	// Distinct start times make the page assertion deterministic.
	base := time.Now().UTC().Add(-time.Hour)
	for i, id := range []pgtype.UUID{run.ID, auto.ID, dry.ID} {
		if err := f.gdb.Model(&model.MaintenanceRun{}).
			Where("id = ?", string(model.UUID(UUIDString(id)))).
			Update("started_at", base.Add(time.Duration(i)*time.Minute)).Error; err != nil {
			t.Fatalf("set started_at: %v", err)
		}
	}
	runs, err := f.r.ListTableMaintenanceRuns(ctx, db.ListTableMaintenanceRunsParams{
		CatalogID: f.catalogID(), Namespace: "sales", TableName: "orders", Lim: 10,
	})
	if err != nil || len(runs) != 3 {
		t.Fatalf("ListTableMaintenanceRuns = %d, %v; want 3", len(runs), err)
	}
	byOperation, err := f.r.ListMaintenanceRuns(ctx, db.ListMaintenanceRunsParams{
		CatalogID: f.catalogID(), Operation: strPtr("optimize_manifests"), Lim: 10,
	})
	if err != nil || len(byOperation) != 1 {
		t.Fatalf("ListMaintenanceRuns(operation) = %+v, %v", byOperation, err)
	}
	if filtered, err := f.r.ListMaintenanceRuns(ctx, db.ListMaintenanceRunsParams{
		CatalogID: f.catalogID(), Status: strPtr("finished"), Lim: 10,
	}); err != nil || len(filtered) != 1 {
		t.Fatalf("ListMaintenanceRuns(status) = %+v, %v", filtered, err)
	}
	if limited, err := f.r.ListMaintenanceRuns(ctx, db.ListMaintenanceRunsParams{
		CatalogID: f.catalogID(), Lim: 1, Off: 1,
	}); err != nil || len(limited) != 1 || limited[0].ID != runs[1].ID {
		t.Fatalf("ListMaintenanceRuns(page) = %+v, %v; want runs[1]", limited, err)
	}

	last, err := f.r.LastSuccessfulMaintenanceByOperation(ctx, db.LastSuccessfulMaintenanceByOperationParams{
		CatalogID: f.catalogID(), Namespace: "sales", TableName: "orders",
	})
	if err != nil || len(last) != 1 || last[0].Operation != "optimize" {
		t.Fatalf("LastSuccessfulMaintenanceByOperation = %+v, %v", last, err)
	}
	successes, err := f.r.ListCatalogLastSuccessfulMaintenance(ctx, f.catalogID())
	if err != nil || len(successes) != 1 || successes[0].Operation != "optimize" {
		t.Fatalf("ListCatalogLastSuccessfulMaintenance = %+v, %v", successes, err)
	}
	if runningRows, err := f.r.ListCatalogRunningMaintenance(ctx, f.catalogID()); err != nil || len(runningRows) != 1 {
		t.Fatalf("ListCatalogRunningMaintenance = %+v, %v", runningRows, err)
	}
	latest, err := f.r.ListCatalogLatestMaintenanceRuns(ctx, f.catalogID())
	if err != nil || len(latest) != 1 || latest[0].Status != "dry_run" {
		t.Fatalf("ListCatalogLatestMaintenanceRuns = %+v, %v", latest, err)
	}
	if health, err := f.r.ListCatalogTableHealth(ctx, f.catalogID()); err != nil || len(health) != 0 {
		t.Fatalf("ListCatalogTableHealth = %+v, %v", health, err)
	}
	policies, err := f.r.ListManagedMaintenancePolicies(ctx, f.catalogID())
	if err != nil || len(policies) != 0 {
		t.Fatalf("ListManagedMaintenancePolicies = %+v, %v", policies, err)
	}
}

// TestMonitoringSnapshots covers cluster and Trino query snapshot writes,
// reads, merge rules, filters and pagination (#117, #773).
func TestMonitoringSnapshots(t *testing.T) {
	t.Parallel()
	f := monitoringFixture(t)
	ctx := context.Background()

	older, err := f.r.InsertClusterSnapshot(ctx, db.InsertClusterSnapshotParams{
		EngineID: f.engineID(), ActiveWorkers: 1, RunningQueries: 2,
		TotalMemoryBytes: int64Ptr(1024), RawPayload: []byte(`{"x":1}`),
	})
	if err != nil {
		t.Fatalf("InsertClusterSnapshot: %v", err)
	}
	newer, err := f.r.InsertClusterSnapshot(ctx, db.InsertClusterSnapshotParams{
		EngineID: f.engineID(), ActiveWorkers: 2, RunningQueries: 3,
	})
	if err != nil {
		t.Fatalf("InsertClusterSnapshot(second): %v", err)
	}
	latest, err := f.r.GetLatestClusterSnapshotByEngine(ctx, f.engineID())
	if err != nil || latest.ID != newer.ID {
		t.Fatalf("GetLatestClusterSnapshotByEngine = %+v, %v", latest, err)
	}
	series, err := f.r.ListClusterSnapshotsByEngine(ctx, db.ListClusterSnapshotsByEngineParams{
		EngineID: f.engineID(), FromTime: PGTime(time.Now().Add(-time.Hour)), Lim: 10,
	})
	if err != nil || len(series) != 2 || series[0].ID != older.ID {
		t.Fatalf("ListClusterSnapshotsByEngine = %+v, %v; want oldest first", series, err)
	}
	if _, err := f.r.GetLatestClusterSnapshotByEngine(ctx, PGUUID(NewUUID())); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetLatestClusterSnapshotByEngine(missing) = %v, want ErrNoRows", err)
	}

	created, err := f.r.UpsertTrinoQuerySnapshot(ctx, db.UpsertTrinoQuerySnapshotParams{
		TrinoQueryID: "q-1", EngineID: f.engineID(), Origin: "baleia", User: "alice",
		State: "running", Sql: "SELECT * FROM Orders", CreatedAt: PGTime(time.Now().UTC()),
		ElapsedMs: int32Ptr(100),
	})
	if err != nil {
		t.Fatalf("UpsertTrinoQuerySnapshot(create): %v", err)
	}
	// A newer poll completes the query and fills its figures.
	completed, err := f.r.UpsertTrinoQuerySnapshot(ctx, db.UpsertTrinoQuerySnapshotParams{
		TrinoQueryID: "q-1", EngineID: f.engineID(), Origin: "baleia", User: "alice",
		State: "finished", Sql: "SELECT * FROM Orders", CreatedAt: PGTime(created.CreatedAt.Time),
		CpuMs: int32Ptr(50), OutputRows: int64Ptr(7),
	})
	if err != nil || completed.State != "finished" || completed.CpuMs == nil || completed.OutputRows == nil {
		t.Fatalf("UpsertTrinoQuerySnapshot(complete) = %+v, %v", completed, err)
	}
	// A late poll of the running query does not roll the state back.
	stale, err := f.r.UpsertTrinoQuerySnapshot(ctx, db.UpsertTrinoQuerySnapshotParams{
		TrinoQueryID: "q-1", EngineID: f.engineID(), Origin: "baleia", User: "alice",
		State: "running", Sql: "SELECT * FROM Orders", CreatedAt: PGTime(created.CreatedAt.Time),
		ElapsedMs: int32Ptr(200),
	})
	if err != nil || stale.State != "finished" || stale.ElapsedMs == nil || *stale.ElapsedMs != 100 {
		t.Fatalf("UpsertTrinoQuerySnapshot(stale) = %+v, %v", stale, err)
	}
	got, err := f.r.GetTrinoQuerySnapshotByEngine(ctx, db.GetTrinoQuerySnapshotByEngineParams{
		EngineID: f.engineID(), TrinoQueryID: "q-1",
	})
	if err != nil || got.ID != created.ID || !got.LastSeenAt.Valid {
		t.Fatalf("GetTrinoQuerySnapshotByEngine = %+v, %v", got, err)
	}
	if err := f.r.SetTrinoQueryProfile(ctx, db.SetTrinoQueryProfileParams{
		TrinoQueryID: "q-1", RawTrinoPayload: []byte(`{"peakMemoryBytes":42}`),
	}); err != nil {
		t.Fatalf("SetTrinoQueryProfile: %v", err)
	}
	got, _ = f.r.GetTrinoQuerySnapshotByEngine(ctx, db.GetTrinoQuerySnapshotByEngineParams{
		EngineID: f.engineID(), TrinoQueryID: "q-1",
	})
	if string(got.RawTrinoPayload) != `{"peakMemoryBytes": 42}` {
		t.Fatalf("profile = %s", got.RawTrinoPayload)
	}

	if _, err := f.r.UpsertTrinoQuerySnapshot(ctx, db.UpsertTrinoQuerySnapshotParams{
		TrinoQueryID: "q-2", EngineID: f.engineID(), Origin: "trino_direct", User: "bob",
		State: "failed", Sql: "SELECT 1", CreatedAt: PGTime(time.Now().UTC().Add(-time.Minute)),
	}); err != nil {
		t.Fatalf("UpsertTrinoQuerySnapshot(second): %v", err)
	}
	all, err := f.r.ListTrinoQuerySnapshotsByEngine(ctx, db.ListTrinoQuerySnapshotsByEngineParams{
		EngineID: f.engineID(), Lim: 10,
	})
	if err != nil || len(all) != 2 || all[0].TrinoQueryID != "q-1" {
		t.Fatalf("ListTrinoQuerySnapshotsByEngine = %+v, %v; want newest first", all, err)
	}
	if byUser, err := f.r.ListTrinoQuerySnapshotsByEngine(ctx, db.ListTrinoQuerySnapshotsByEngineParams{
		EngineID: f.engineID(), UserName: strPtr("bob"), Lim: 10,
	}); err != nil || len(byUser) != 1 || byUser[0].TrinoQueryID != "q-2" {
		t.Fatalf("ListTrinoQuerySnapshotsByEngine(user) = %+v, %v", byUser, err)
	}
	if search, err := f.r.ListTrinoQuerySnapshotsByEngine(ctx, db.ListTrinoQuerySnapshotsByEngineParams{
		EngineID: f.engineID(), Q: strPtr("orders"), Lim: 10,
	}); err != nil || len(search) != 1 || search[0].TrinoQueryID != "q-1" {
		t.Fatalf("ListTrinoQuerySnapshotsByEngine(q) = %+v, %v", search, err)
	}
	if state, err := f.r.ListTrinoQuerySnapshotsByEngine(ctx, db.ListTrinoQuerySnapshotsByEngineParams{
		EngineID: f.engineID(), State: strPtr("failed"), Lim: 10,
	}); err != nil || len(state) != 1 || state[0].TrinoQueryID != "q-2" {
		t.Fatalf("ListTrinoQuerySnapshotsByEngine(state) = %+v, %v", state, err)
	}
	if paged, err := f.r.ListTrinoQuerySnapshotsByEngine(ctx, db.ListTrinoQuerySnapshotsByEngineParams{
		EngineID: f.engineID(), Lim: 1, Off: 1,
	}); err != nil || len(paged) != 1 || paged[0].TrinoQueryID != "q-2" {
		t.Fatalf("ListTrinoQuerySnapshotsByEngine(page) = %+v, %v", paged, err)
	}
	// Cancelled is terminal too: a late running poll keeps the state and its
	// final output count.
	if _, err := f.r.UpsertTrinoQuerySnapshot(ctx, db.UpsertTrinoQuerySnapshotParams{
		TrinoQueryID: "q-3", EngineID: f.engineID(), Origin: "baleia", User: "alice",
		State: "cancelled", Sql: "SELECT 3", CreatedAt: PGTime(time.Now().UTC()),
		OutputRows: int64Ptr(3),
	}); err != nil {
		t.Fatalf("UpsertTrinoQuerySnapshot(cancelled): %v", err)
	}
	cancelled, err := f.r.UpsertTrinoQuerySnapshot(ctx, db.UpsertTrinoQuerySnapshotParams{
		TrinoQueryID: "q-3", EngineID: f.engineID(), Origin: "baleia", User: "alice",
		State: "running", Sql: "SELECT 3", CreatedAt: PGTime(time.Now().UTC()),
		OutputRows: int64Ptr(99),
	})
	if err != nil || cancelled.State != "cancelled" || cancelled.OutputRows == nil || *cancelled.OutputRows != 3 {
		t.Fatalf("cancelled stale merge = %+v, %v", cancelled, err)
	}

	if _, err := f.r.GetTrinoQuerySnapshotByEngine(ctx, db.GetTrinoQuerySnapshotByEngineParams{
		EngineID: f.engineID(), TrinoQueryID: "missing",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetTrinoQuerySnapshotByEngine(missing) = %v, want ErrNoRows", err)
	}
}

// TestResourceGroupSelectors covers the resource-group and selector writes and
// the environment-scoped replacement (#599, #773).
func TestResourceGroupSelectors(t *testing.T) {
	t.Parallel()
	f := monitoringFixture(t)
	ctx := context.Background()

	env := "prod"
	root, err := f.r.InsertTrinoResourceGroup(ctx, db.InsertTrinoResourceGroupParams{
		Name: "baleia", SoftMemoryLimit: "80%", MaxQueued: 10, HardConcurrencyLimit: 5,
		Environment: &env,
	})
	if err != nil {
		t.Fatalf("InsertTrinoResourceGroup(root): %v", err)
	}
	child, err := f.r.InsertTrinoResourceGroup(ctx, db.InsertTrinoResourceGroupParams{
		Name: "interactive", SoftMemoryLimit: "80%", MaxQueued: 5, HardConcurrencyLimit: 2,
		Parent: &root, Environment: &env,
	})
	if err != nil {
		t.Fatalf("InsertTrinoResourceGroup(child): %v", err)
	}
	for i, group := range []int64{child, root} {
		if err := f.r.InsertTrinoSelector(ctx, db.InsertTrinoSelectorParams{
			ResourceGroupID: group, Priority: int64(10 - i),
			ClientTags: strPtr("baleia.class=interactive"),
		}); err != nil {
			t.Fatalf("InsertTrinoSelector: %v", err)
		}
	}
	var selectors []model.Selector
	if err := f.gdb.Where("resource_group_id IN ?", []int64{root, child}).
		Order("priority DESC").Find(&selectors).Error; err != nil {
		t.Fatalf("load selectors: %v", err)
	}
	if len(selectors) != 2 || selectors[0].GroupID != child {
		t.Fatalf("selectors = %+v; want the higher priority on the child", selectors)
	}
	otherEnv := "staging"
	other, err := f.r.InsertTrinoResourceGroup(ctx, db.InsertTrinoResourceGroupParams{
		Name: "baleia", SoftMemoryLimit: "80%", MaxQueued: 3, HardConcurrencyLimit: 1,
		Environment: &otherEnv,
	})
	if err != nil {
		t.Fatalf("InsertTrinoResourceGroup(other environment): %v", err)
	}
	if err := f.r.InsertTrinoSelector(ctx, db.InsertTrinoSelectorParams{
		ResourceGroupID: other, Priority: 1, ClientTags: strPtr("baleia.class=interactive"),
	}); err != nil {
		t.Fatalf("InsertTrinoSelector(other environment): %v", err)
	}
	if err := f.r.DeleteTrinoResourceGroups(ctx, &env); err != nil {
		t.Fatalf("DeleteTrinoResourceGroups: %v", err)
	}
	var remaining int64
	if err := f.gdb.Model(&model.ResourceGroup{}).Where("environment = ?", env).Count(&remaining).Error; err != nil {
		t.Fatalf("count groups: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("groups after delete = %d", remaining)
	}
	var survived int64
	if err := f.gdb.Model(&model.ResourceGroup{}).Where("resource_group_id = ?", other).Count(&survived).Error; err != nil {
		t.Fatalf("count other groups: %v", err)
	}
	var otherSelectors int64
	if err := f.gdb.Model(&model.Selector{}).Where("resource_group_id = ?", other).Count(&otherSelectors).Error; err != nil {
		t.Fatalf("count other selectors: %v", err)
	}
	if survived != 1 || otherSelectors != 1 {
		t.Fatalf("other environment after prod delete = %d groups, %d selectors; want both", survived, otherSelectors)
	}
}

// TestReconcileRuns covers reconcile-run storage, reads, the latest-per-engine
// reduction and the dry-run drift signal (#37, #291, #773).
func TestReconcileRuns(t *testing.T) {
	t.Parallel()
	f := monitoringFixture(t)
	ctx := context.Background()
	started := time.Now().UTC().Add(-time.Minute)

	old, err := f.r.InsertReconcileRun(ctx, db.InsertReconcileRunParams{
		EngineID: f.engineID(), StartedAt: PGTime(started), FinishedAt: PGTime(started.Add(time.Second)),
		DryRun: true, Actions: []byte(`[]`), Adopted: []string{},
	})
	if err != nil {
		t.Fatalf("InsertReconcileRun: %v", err)
	}
	drift, err := f.r.InsertReconcileRun(ctx, db.InsertReconcileRunParams{
		EngineID: f.engineID(), StartedAt: PGTime(started.Add(time.Minute)), FinishedAt: PGTime(started.Add(time.Minute + time.Second)),
		DryRun: true, Actions: []byte(`[{"kind":"create"},{"kind":"drop"}]`), Adopted: []string{},
	})
	if err != nil {
		t.Fatalf("InsertReconcileRun(second): %v", err)
	}
	runs, err := f.r.ListReconcileRuns(ctx, db.ListReconcileRunsParams{EngineID: f.engineID(), Limit: 10})
	if err != nil || len(runs) != 2 || runs[0].ID != drift.ID {
		t.Fatalf("ListReconcileRuns = %+v, %v; want newest first", runs, err)
	}
	got, err := f.r.GetReconcileRun(ctx, db.GetReconcileRunParams{EngineID: f.engineID(), ID: old.ID})
	if err != nil || got.ID != old.ID || got.DryRun != true {
		t.Fatalf("GetReconcileRun = %+v, %v", got, err)
	}
	if _, err := f.r.GetReconcileRun(ctx, db.GetReconcileRunParams{EngineID: f.engineID(), ID: PGUUID(NewUUID())}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetReconcileRun(missing) = %v, want ErrNoRows", err)
	}

	latest, err := f.r.LatestReconcileRuns(ctx)
	if err != nil || len(latest) != 1 || latest[0].EngineID != f.engineID() ||
		!latest[0].StartedAt.Time.Equal(runs[0].StartedAt.Time) {
		t.Fatalf("LatestReconcileRuns = %+v, %v; want the newest run", latest, err)
	}
	driftRows, err := f.r.ListAttentionReconcileDrift(ctx)
	if err != nil || len(driftRows) != 1 || driftRows[0].ActionCount != 2 {
		t.Fatalf("ListAttentionReconcileDrift = %+v, %v", driftRows, err)
	}
}

func int32Ptr(v int32) *int32 { return &v }

// TestUpsertTrinoQuerySnapshotConcurrentFirstWrite synchronizes a poller and an
// event writer inserting the same query before it exists. Both must succeed and
// the stored row must be the terminal one (#406, #773).
func TestUpsertTrinoQuerySnapshotConcurrentFirstWrite(t *testing.T) {
	t.Parallel()
	f := monitoringFixture(t)
	ctx := context.Background()

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, err := f.r.UpsertTrinoQuerySnapshot(ctx, db.UpsertTrinoQuerySnapshotParams{
			TrinoQueryID: "q-race", EngineID: f.engineID(), Origin: "baleia", User: "alice",
			State: "finished", Sql: "SELECT 1", CreatedAt: PGTime(time.Now().UTC()),
			OutputRows: int64Ptr(5),
		})
		errs <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		_, err := f.r.UpsertTrinoQuerySnapshot(ctx, db.UpsertTrinoQuerySnapshotParams{
			TrinoQueryID: "q-race", EngineID: f.engineID(), Origin: "trino_direct", User: "alice",
			State: "running", Sql: "SELECT 1", CreatedAt: PGTime(time.Now().UTC()),
			OutputRows: int64Ptr(99),
		})
		errs <- err
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent first write: %v", err)
		}
	}
	snap, err := f.r.GetTrinoQuerySnapshotByEngine(ctx, db.GetTrinoQuerySnapshotByEngineParams{
		EngineID: f.engineID(), TrinoQueryID: "q-race",
	})
	if err != nil || snap.State != "finished" || snap.OutputRows == nil || *snap.OutputRows != 5 {
		t.Fatalf("concurrent first write = %+v, %v; want the terminal row", snap, err)
	}
}

// TestUpsertTableHealthSnapshotConcurrentFirstWrite synchronizes two health
// passes creating the same row; both succeed and the maintenance timestamp is
// preserved (#773).
func TestUpsertTableHealthSnapshotConcurrentFirstWrite(t *testing.T) {
	t.Parallel()
	f := monitoringFixture(t)
	ctx := context.Background()
	connection := f.catalogConnection(t)
	maintainedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)

	base := db.UpsertTableHealthSnapshotParams{
		CatalogID: f.catalogID(), CatalogConnectionID: PGUUID(string(connection)),
		Catalog: "lake", Namespace: "race", TableName: "health",
		HealthScore: 80, TableProperties: []byte(`{}`),
	}
	withTime := base
	withTime.LastMaintenanceAt = PGTime(maintainedAt)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for _, arg := range []db.UpsertTableHealthSnapshotParams{base, withTime} {
		go func(arg db.UpsertTableHealthSnapshotParams) {
			defer wg.Done()
			<-start
			_, err := f.r.UpsertTableHealthSnapshot(ctx, arg)
			errs <- err
		}(arg)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent health write: %v", err)
		}
	}
	got, err := f.r.GetTableHealthByTable(ctx, db.GetTableHealthByTableParams{
		CatalogID: f.catalogID(), Namespace: "race", TableName: "health",
	})
	if err != nil || !got.LastMaintenanceAt.Valid || !got.LastMaintenanceAt.Time.Equal(maintainedAt) {
		t.Fatalf("concurrent health write = %+v, %v; want the maintenance timestamp", got, err)
	}
}

// TestQueryEventIngesterUsesRepo sends Trino's completed event through the
// HTTP ingester with the GORM store and checks the row it writes, including the
// stale-poll merge (#406, #773).
func TestQueryEventIngesterUsesRepo(t *testing.T) {
	t.Parallel()
	f := monitoringFixture(t)
	ctx := context.Background()
	ingester := queryevents.New(nil, f.r, func(context.Context) (pgtype.UUID, error) {
		return f.engineID(), nil
	}, "", nil)

	completed := []byte(`{
		"metadata": {"queryId": "q-event-1", "query": "SELECT 1", "queryState": "FINISHED"},
		"context": {"user": "alice", "catalog": "lake", "schema": "sales"},
		"statistics": {"wallTime": 1.5, "cpuTime": 0.5, "outputRows": 1},
		"createTime": "2026-11-08T18:00:00Z"
	}`)
	if err := ingester.Ingest(ctx, bytes.NewReader(completed)); err != nil {
		t.Fatalf("Ingest(completed): %v", err)
	}
	snap, err := f.r.GetTrinoQuerySnapshotByEngine(ctx, db.GetTrinoQuerySnapshotByEngineParams{
		EngineID: f.engineID(), TrinoQueryID: "q-event-1",
	})
	if err != nil {
		t.Fatalf("GetTrinoQuerySnapshotByEngine: %v", err)
	}
	if snap.State != "finished" || snap.User != "alice" || snap.Origin != "trino_direct" ||
		snap.ElapsedMs == nil || *snap.ElapsedMs != 1500 || snap.OutputRows == nil || *snap.OutputRows != 1 {
		t.Fatalf("query-event snapshot = %+v", snap)
	}
	if !strings.Contains(string(snap.RawTrinoPayload), `"queuedMs"`) {
		t.Fatalf("payload lacks the profile timeline: %s", snap.RawTrinoPayload)
	}

	// A poll that still reads RUNNING must not roll the finished row back.
	stale := []byte(`{
		"metadata": {"queryId": "q-event-1", "query": "SELECT 1", "queryState": "RUNNING"},
		"context": {"user": "alice"},
		"statistics": {"wallTime": 9, "cpuTime": 1}
	}`)
	if err := ingester.Ingest(ctx, bytes.NewReader(stale)); err != nil {
		t.Fatalf("Ingest(stale): %v", err)
	}
	snap, err = f.r.GetTrinoQuerySnapshotByEngine(ctx, db.GetTrinoQuerySnapshotByEngineParams{
		EngineID: f.engineID(), TrinoQueryID: "q-event-1",
	})
	if err != nil || snap.State != "finished" || snap.ElapsedMs == nil || *snap.ElapsedMs != 1500 ||
		snap.OutputRows == nil || *snap.OutputRows != 1 {
		t.Fatalf("stale query-event merge = %+v, %v", snap, err)
	}
}

// TestMonitoringSignals covers the status counts and attention reads that the
// system status page and the attention list consume (#291, #261, #773).
func TestMonitoringSignals(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	ctx := context.Background()

	if err := f.gdb.Model(&model.CatalogBinding{}).
		Where("id = ?", string(f.binding)).
		Updates(map[string]any{"lifecycle_state": "error", "state_changed_at": time.Now().UTC()}).Error; err != nil {
		t.Fatalf("mark binding in error: %v", err)
	}
	finished := time.Now().UTC()
	if err := f.gdb.Create(&model.MetadataSyncRun{
		ID: model.UUID(NewUUID()), CatalogID: f.catalog, StartedAt: finished.Add(-time.Minute),
		FinishedAt: &finished, Status: "failed", SourcePath: "provider",
	}).Error; err != nil {
		t.Fatalf("create metadata run: %v", err)
	}

	counts, err := f.r.MetadataStatusCounts(ctx, 60)
	if err != nil || counts.Catalogs != 1 || counts.Failed != 1 {
		t.Fatalf("MetadataStatusCounts = %+v, %v", counts, err)
	}
	bindings, err := f.r.ListAttentionBindingErrors(ctx)
	if err != nil || len(bindings) != 1 || bindings[0].LifecycleState != "error" || bindings[0].CatalogName == "" {
		t.Fatalf("ListAttentionBindingErrors = %+v, %v", bindings, err)
	}
	failures, err := f.r.ListAttentionMetadataSyncFailed(ctx)
	if err != nil || len(failures) != 1 || failures[0].CatalogID != f.catalogID() {
		t.Fatalf("ListAttentionMetadataSyncFailed = %+v, %v", failures, err)
	}
	latest, err := f.r.LatestReconcileRuns(ctx)
	if err != nil || len(latest) != 0 {
		t.Fatalf("LatestReconcileRuns(empty) = %+v, %v", latest, err)
	}
	if lag, err := f.r.GetAttentionAuditExportLag(ctx); err != nil || lag.Valid {
		t.Fatalf("GetAttentionAuditExportLag(empty) = %+v, %v", lag, err)
	}
}
