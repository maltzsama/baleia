package activities

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/monitoring"
	"github.com/galileostudio/baleia/internal/monitoring/maintenance"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/stretchr/testify/mock"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/testsuite"
	"gorm.io/gorm"
)

func i64(v int64) *int64 { return &v }

// seedUser inserts an admin user through the GORM store.
func seedUser(t *testing.T, gdb *gorm.DB, name, email string) db.UUID {
	t.Helper()
	password := "x"
	row := model.User{
		ID: model.UUID(repo.NewUUID()), Name: name, Email: model.Text(email),
		Role: "admin", PasswordHash: &password,
	}
	if err := gdb.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return repo.PGUUID(string(row.ID))
}

// seedConnection inserts a Polaris connection through the GORM store.
func seedConnection(t *testing.T, gdb *gorm.DB, name string, createdBy db.UUID) db.UUID {
	t.Helper()
	row, err := repo.New(gdb).CreateCatalogConnection(context.Background(), db.CreateCatalogConnectionParams{
		ID: repo.PGUUID(repo.NewUUID()), Name: name, Type: "polaris", Url: "http://p.test", CreatedBy: createdBy,
	})
	if err != nil {
		t.Fatal(err)
	}
	return row.ID
}

// seedEngine inserts a Trino engine through the GORM store.
func seedEngine(t *testing.T, gdb *gorm.DB, name string, conn, createdBy db.UUID) db.UUID {
	t.Helper()
	row, err := repo.New(gdb).CreateEngine(context.Background(), db.CreateEngineParams{
		Name: name, Kind: "trino", Class: "federated", ConnectionID: conn,
		IdentityMode: "connection", CreatedBy: createdBy,
	})
	if err != nil {
		t.Fatal(err)
	}
	return row.ID
}

// seedCatalogBinding inserts a catalog and its managed binding, on the seeded
// Trino cluster, and returns both ids.
func seedCatalogBinding(t *testing.T, gdb *gorm.DB, conn, engine db.UUID, name string) (db.UUID, db.UUID) {
	t.Helper()
	var cluster model.TrinoCluster
	if err := gdb.Order("name").First(&cluster).Error; err != nil {
		t.Fatal(err)
	}
	trinoName := model.Text(name)
	warehouse := "wh"
	connector := model.Text("iceberg")
	clusterID := cluster.ID
	catalog := model.Catalog{
		ID: model.UUID(repo.NewUUID()), ConnectionID: model.UUID(repo.UUIDString(conn)),
		Name: model.Text(name), TrinoCatalogName: &trinoName, ClusterID: &clusterID,
		Warehouse: &warehouse, Connector: &connector, Origin: "source",
	}
	if err := gdb.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}
	binding := model.CatalogBinding{
		ID: model.UUID(repo.NewUUID()), CatalogID: catalog.ID,
		EngineID: model.UUID(repo.UUIDString(engine)), EngineCatalogName: model.Text(name),
		Mode: "managed", Enabled: true, LifecycleState: "active",
	}
	if err := gdb.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	return repo.PGUUID(string(catalog.ID)), repo.PGUUID(string(binding.ID))
}

func TestReadSnapshotMetrics(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) int64 { return now.Add(-ago).UnixMilli() }
	meta := &connections.TableMeta{
		FormatVersion:   2,
		TableProperties: map[string]string{"write.delete.mode": "merge-on-read"},
		Properties:      map[string]string{"current_snapshot_id": "2"},
		Snapshots: []connections.Snapshot{
			{ID: 1, TimestampMs: at(10 * 24 * time.Hour)},
			{ID: 2, TimestampMs: at(30 * time.Minute), Summary: map[string]string{
				"total-data-files": "40", "total-files-size": "4000", "total-delete-files": "12",
				"total-equality-deletes": "300", "total-position-deletes": "50",
			}},
			{ID: 3, TimestampMs: at(2 * time.Hour), Summary: map[string]string{"total-data-files": "999"}},
		},
	}
	m := readSnapshotMetrics(meta, now)
	if *m.dataFiles != 40 || *m.totalSize != 4000 || *m.avgFileSize != 100 {
		t.Errorf("the current snapshot (id 2) must be read: files %v size %v avg %v", *m.dataFiles, *m.totalSize, *m.avgFileSize)
	}
	if *m.deleteFiles != 12 || *m.equalityDel != 300 || *m.positionDel != 50 {
		t.Errorf("delete counts = %v %v %v", *m.deleteFiles, *m.equalityDel, *m.positionDel)
	}
	if *m.commitsHour != 1 || *m.commitsDay != 2 || *m.oldestAgeHours != 240 || *m.formatVersion != 2 {
		t.Errorf("commits %v/%v oldest %v format %v", *m.commitsHour, *m.commitsDay, *m.oldestAgeHours, *m.formatVersion)
	}
	if m.properties["write.delete.mode"] != "merge-on-read" {
		t.Errorf("table properties lost: %v", m.properties)
	}

	v1 := readSnapshotMetrics(&connections.TableMeta{FormatVersion: 1, Snapshots: meta.Snapshots[:2]}, now)
	if v1.deleteFiles != nil {
		t.Error("a format v1 table has no delete counts")
	}
	empty := readSnapshotMetrics(&connections.TableMeta{}, now)
	if empty.dataFiles != nil || empty.commitsHour != nil || empty.formatVersion != nil {
		t.Errorf("a table without snapshots has no metrics: %+v", empty)
	}
}

func TestDefaultScore(t *testing.T) {
	t.Parallel()
	files := int32(120)
	old := int32(200)
	score, compaction, expiry := defaultScore(60, snapshotMetrics{dataFiles: &files, oldestAgeHours: &old})
	if score != 20 || !compaction || !expiry {
		t.Errorf("defaultScore = %d %v %v, want 20 true true", score, compaction, expiry)
	}
	if score, _, _ := defaultScore(0, snapshotMetrics{}); score != 100 {
		t.Errorf("a quiet table scores %d, want 100", score)
	}
	if got := policyScore([]string{"optimize", "expire_snapshots"}); got != 50 {
		t.Errorf("policyScore = %d, want 50", got)
	}
}

func TestIsCommitConflict(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"iceberg commit error", queryengine.NewError(queryengine.CodeInternal, "trino", "ICEBERG_COMMIT_ERROR", "Failed to commit"), true},
		{"engine conflict code", queryengine.NewError(queryengine.CodeConflict, "trino", "", "conflict"), true},
		{"wrapped message", errors.New("maintenance: org.apache.iceberg.exceptions.CommitFailedException: base changed"), true},
		{"syntax error", queryengine.NewError(queryengine.CodeSyntax, "trino", "SYNTAX_ERROR", "bad"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isCommitConflict(tt.err); got != tt.want {
				t.Errorf("isCommitConflict() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIcebergOperation(t *testing.T) {
	t.Parallel()
	filter := "day < DATE '2026-01-01'"
	opt := icebergOperation(maintenance.OpOptimize, maintenance.Settings{Params: maintenance.Params{
		TargetFileSizeBytes: i64(1024), PartitionFilter: &filter, SkipFilesNewerThanSeconds: i64(3600),
	}})
	if opt.FileSizeThreshold != 1024 || opt.PartitionFilter != filter || opt.SkipFilesNewerThan != time.Hour {
		t.Errorf("optimize = %+v", opt)
	}
	exp := icebergOperation(maintenance.OpExpireSnapshots, maintenance.Settings{Params: maintenance.Params{
		RetentionSeconds: i64(86400), RetainLast: i64(5),
	}})
	if exp.Retention != 24*time.Hour || exp.RetainLast != 5 {
		t.Errorf("expire = %+v", exp)
	}
	orph := icebergOperation(maintenance.OpRemoveOrphanFiles, maintenance.Settings{Params: maintenance.Params{OlderThanSeconds: i64(7200)}})
	if orph.Retention != 2*time.Hour {
		t.Errorf("orphans = %+v", orph)
	}
}

func TestOrderCandidates(t *testing.T) {
	t.Parallel()
	at := func(h int) *time.Time {
		v := time.Date(2026, 6, 4, h, 0, 0, 0, time.UTC)
		return &v
	}
	cand := func(table, fired string, last *time.Time) roundCandidate {
		return roundCandidate{
			key:  monitoring.TableKey{Namespace: "cdc", Table: table},
			step: maintenance.Step{Reason: maintenance.Reason{Fired: fired, LastSuccessAt: last}},
		}
	}
	// "busy" is due every round and sorts first by name: it must not keep
	// the overdue, never-run and long-waiting tables from their turn.
	c := []roundCandidate{
		cand("a_busy", "snapshots", at(9)),
		cand("waiting", "snapshots", at(1)),
		cand("never", "small_file_ratio", nil),
		cand("overdue", maintenance.FiredMaxInterval, at(8)),
	}
	orderCandidates(c)
	var got []string
	for _, x := range c {
		got = append(got, x.key.Table)
	}
	if want := []string{"overdue", "never", "waiting", "a_busy"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// TestReserveRuns_GlobalLimit: rounds of several catalogs at once never
// start more runs than the limit, and a table already running is skipped.
func TestReserveRuns_GlobalLimit(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	gdb := pool
	user := seedUser(t, gdb, "m", "m@maint.test")
	conn := seedConnection(t, gdb, "src", user)
	engConn := seedConnection(t, gdb, "eng", user)
	engine := seedEngine(t, gdb, "e", engConn, user)
	const catalogs, tables, limit = 4, 3, 5
	a := &Activities{store: repo.New(gdb), cfg: &config.Config{MaintenanceMaxConcurrentRuns: limit}}
	type round struct {
		binding    db.CatalogBinding
		candidates []roundCandidate
	}
	rounds := make([]round, catalogs)
	for i := range rounds {
		catalogID, bindingID := seedCatalogBinding(t, gdb, conn, engine, fmt.Sprintf("lake%d", i))
		rounds[i].binding.ID = bindingID
		for j := range tables {
			key := monitoring.TableKey{CatalogID: catalogID, Namespace: "cdc", Table: fmt.Sprintf("t%d", j)}
			step := maintenance.Step{Operation: maintenance.OpExpireSnapshots, Due: true, WouldRun: true}
			rounds[i].candidates = append(rounds[i].candidates, roundCandidate{key: key, step: step})
		}
	}
	// The first catalog's first table is already running.
	first := rounds[0].candidates[0]
	if _, err := RecordMaintenanceRun(ctx, repo.New(gdb), first.key, rounds[0].binding, first.row, first.step, first.settings, "manual", db.UUID{}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, catalogs)
	for _, r := range rounds {
		wg.Go(func() {
			if _, err := a.reserveRuns(ctx, r.binding, r.candidates); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var running int64
	if err := gdb.Model(&model.MaintenanceRun{}).Where("status = ?", "running").Count(&running).Error; err != nil {
		t.Fatal(err)
	}
	if running != limit {
		t.Fatalf("running runs = %d, want the limit %d", running, limit)
	}

	// The same round over the GORM store: row locks on the engines serialize
	// it and a savepoint absorbs the table that is already running (#773).
	if err := gdb.Where("id IS NOT NULL").Delete(&model.MaintenanceRun{}).Error; err != nil {
		t.Fatal(err)
	}
	a.store = repo.New(gdb)
	if _, err := RecordMaintenanceRun(ctx, a.store, first.key, rounds[0].binding, first.row, first.step, first.settings, "manual", db.UUID{}); err != nil {
		t.Fatal(err)
	}
	errs = make(chan error, catalogs)
	for _, r := range rounds {
		wg.Go(func() {
			if _, err := a.reserveRuns(ctx, r.binding, r.candidates); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var runningStore int64
	if err := gdb.Model(&model.MaintenanceRun{}).Where("status = ?", "running").Count(&runningStore).Error; err != nil {
		t.Fatal(err)
	}
	if runningStore != limit {
		t.Fatalf("GORM running runs = %d, want the limit %d", runningStore, limit)
	}
}

// TestReserveRunsStore_DuplicateRecovery is the deterministic GORM reservation
// case: a duplicate candidate must not abort the transaction, and the next
// candidate still commits (#773).
func TestReserveRunsStore_DuplicateRecovery(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	gdb := pool
	user := seedUser(t, gdb, "dup", "dup@maint.test")
	conn := seedConnection(t, gdb, "src", user)
	engConn := seedConnection(t, gdb, "eng", user)
	engine := seedEngine(t, gdb, "dup-e", engConn, user)
	catalog, binding := seedCatalogBinding(t, gdb, conn, engine, "dup_lake")

	store := repo.New(gdb)
	a := &Activities{store: store, cfg: &config.Config{MaintenanceMaxConcurrentRuns: 10}}

	existing := monitoring.TableKey{CatalogID: catalog, Namespace: "cdc", Table: "t0"}
	step := maintenance.Step{Operation: maintenance.OpExpireSnapshots, Due: true, WouldRun: true}
	b := db.CatalogBinding{ID: binding}
	if _, err := RecordMaintenanceRun(ctx, store, existing, b, db.TableMaintenancePolicy{}, step, maintenance.Settings{}, "manual", db.UUID{}); err != nil {
		t.Fatal(err)
	}
	candidates := []roundCandidate{
		{key: existing, step: step},
		{key: monitoring.TableKey{CatalogID: catalog, Namespace: "cdc", Table: "t1"}, step: step},
	}
	out, err := a.reserveRuns(ctx, b, candidates)
	if err != nil {
		t.Fatalf("reserveRuns: %v", err)
	}
	if len(out) != 1 || out[0].WorkflowID == "" {
		t.Fatalf("reserved = %+v; want the new table only", out)
	}
	var running int64
	if err := gdb.Model(&model.MaintenanceRun{}).Where("status = ?", "running").Count(&running).Error; err != nil {
		t.Fatal(err)
	}
	if running != 2 {
		t.Fatalf("running runs = %d; want the existing plus the new one", running)
	}
}

// TestCatalogReadOnly: the worker refuses to maintain a catalog whose
// credentials are asserted read-only or whose connection kind promises no
// writes, and its round plans nothing for it (#996).
// TestEngineMetricsReadOnly: the small-file and manifest counts are read in a
// read-only session of the service identity, as every probe (ADR 0005).
func TestEngineMetricsReadOnly(t *testing.T) {
	t.Parallel()
	eng := &fakeEngine{Engine: testTrino, ctasCount: "9"}
	a := &Activities{engines: eng}
	p := maintenance.Policy{
		Optimize: maintenance.Settings{Enabled: true, Trigger: maintenance.Trigger{SmallFileRatio: new(0.5)},
			Params: maintenance.Params{TargetFileSizeBytes: i64(1024)}},
		OptimizeManifests: maintenance.Settings{Enabled: true, Trigger: maintenance.Trigger{Manifests: i64(10)}},
	}
	small, manifests := a.engineMetrics(context.Background(), eng, []string{"lake", "cdc", "orders"}, p)
	if small == nil || *small != 9 || manifests == nil || *manifests != 9 {
		t.Fatalf("small %v, manifests %v; want 9 and 9", small, manifests)
	}
	if eng.callCount != 2 || !eng.lastSpec.ReadOnly || eng.lastSpec.Identity.UserID != "" {
		t.Fatalf("calls %d, last session read-only %v as %q; want two read-only service reads", eng.callCount, eng.lastSpec.ReadOnly, eng.lastSpec.Identity.UserID)
	}
}

func TestCatalogReadOnly(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	user := seedUser(t, gdb, "ro", "ro@maint.test")
	engConn := seedConnection(t, gdb, "ro-eng", user)
	engine := seedEngine(t, gdb, "ro-e", engConn, user)
	dialer, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := providers.New(dialer)
	if err != nil {
		t.Fatal(err)
	}
	a := &Activities{store: repo.New(gdb), connections: reg, cfg: &config.Config{MaintenanceMaxConcurrentRuns: 10}}
	ctx := context.Background()

	for name, tc := range map[string]struct {
		update   map[string]any
		readOnly bool
	}{
		"writable polaris":      {map[string]any{}, false},
		"credentials read-only": {map[string]any{"credentials_read_only": true}, true},
		"unity promises reads":  {map[string]any{"type": "unity"}, true},
	} {
		conn := seedConnection(t, gdb, "ro-src-"+strings.ReplaceAll(name, " ", "-"), user)
		catalog, _ := seedCatalogBinding(t, gdb, conn, engine, "ro_"+strings.ReplaceAll(strings.ReplaceAll(name, " ", "_"), "-", "_"))
		if len(tc.update) > 0 {
			if err := gdb.Model(&model.CatalogConnection{}).Where("id = ?", repo.UUIDString(conn)).Updates(tc.update).Error; err != nil {
				t.Fatal(err)
			}
		}
		got, err := a.catalogReadOnly(ctx, catalog)
		if err != nil || got != tc.readOnly {
			t.Errorf("%s: catalogReadOnly = %v, %v; want %v", name, got, err, tc.readOnly)
		}
		runs, err := a.planMaintenanceRound(ctx, PlanMaintenanceRoundParams{CatalogID: repo.UUIDString(catalog)})
		if err != nil || len(runs) != 0 {
			t.Errorf("%s: round = %v, %v; want no runs", name, runs, err)
		}
	}
}

// TestRunMaintenanceOperation_ReadOnlyCatalog: a run planned before the
// catalog became read-only ends as catalog_read_only and never reaches the
// engine; the same run on a writable catalog executes (#996).
func TestRunMaintenanceOperation_ReadOnlyCatalog(t *testing.T) {
	// Not parallel: it swaps the package-level defaultActivities, which other
	// parallel tests also write.
	for name, readOnly := range map[string]bool{"writable": false, "read-only": true} {
		t.Run(name, func(t *testing.T) {
			gdb := testdb.New(t)
			ctx := context.Background()
			store := repo.New(gdb)
			user := seedUser(t, gdb, "run-ro", "run-ro@maint.test")
			conn := seedConnection(t, gdb, "run-ro-src", user)
			engine := seedEngine(t, gdb, "run-ro-e", seedConnection(t, gdb, "run-ro-eng", user), user)
			catalog, binding := seedCatalogBinding(t, gdb, conn, engine, "run_ro_lake")
			if readOnly {
				if err := gdb.Model(&model.CatalogConnection{}).Where("id = ?", repo.UUIDString(conn)).
					Update("credentials_read_only", true).Error; err != nil {
					t.Fatal(err)
				}
			}
			step := maintenance.Step{Operation: maintenance.OpOptimize, Due: true, WouldRun: true}
			run, err := RecordMaintenanceRun(ctx, store, monitoring.TableKey{CatalogID: catalog, Namespace: "cdc", Table: "orders"},
				db.CatalogBinding{ID: binding}, db.TableMaintenancePolicy{}, step, maintenance.Settings{}, "manual", user)
			if err != nil {
				t.Fatal(err)
			}

			dialer, err := connections.NewDialer(connections.DialerOptions{})
			if err != nil {
				t.Fatal(err)
			}
			reg, err := providers.New(dialer)
			if err != nil {
				t.Fatal(err)
			}
			eng := &fakeEngine{Engine: testTrino}
			a := &Activities{store: store, engines: eng, connections: reg, logger: slog.New(slog.DiscardHandler), cfg: &config.Config{}}
			old := defaultActivities
			defaultActivities = a
			defer func() { defaultActivities = old }()
			env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
			env.RegisterActivityWithOptions(RunMaintenanceOperation, activity.RegisterOptions{Name: "RunMaintenanceOperation"})
			_, runErr := env.ExecuteActivity(RunMaintenanceOperation, RunMaintenanceOperationParams{RunID: repo.UUIDString(run.ID)})

			got, err := store.GetMaintenanceRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if readOnly {
				if runErr == nil || got.Status != "failed" || got.ErrorCode == nil || *got.ErrorCode != "catalog_read_only" {
					t.Fatalf("read-only: err=%v status=%q code=%v", runErr, got.Status, got.ErrorCode)
				}
				if eng.sessions != 0 {
					t.Fatalf("read-only run opened %d engine sessions, want none", eng.sessions)
				}
				return
			}
			if runErr != nil || got.Status != "finished" || eng.sessions == 0 {
				t.Fatalf("writable: err=%v status=%q sessions=%d", runErr, got.Status, eng.sessions)
			}
		})
	}
}

// seedRunningRun records a running run on table, started age ago.
func seedRunningRun(t *testing.T, gdb *gorm.DB, store *repo.Repo, catalog, binding, user db.UUID, table string, age time.Duration) db.MaintenanceRun {
	t.Helper()
	step := maintenance.Step{Operation: maintenance.OpOptimize, Due: true, WouldRun: true}
	run, err := RecordMaintenanceRun(context.Background(), store, monitoring.TableKey{CatalogID: catalog, Namespace: "cdc", Table: table},
		db.CatalogBinding{ID: binding}, db.TableMaintenancePolicy{}, step, maintenance.Settings{}, "manual", user)
	if err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.MaintenanceRun{}).Where("id = ?", repo.UUIDString(run.ID)).
		Update("started_at", time.Now().Add(-age)).Error; err != nil {
		t.Fatal(err)
	}
	return run
}

// TestCloseMaintenanceRun: the workflow closes a run that is still running
// once its activity returned, and leaves one the activity already closed.
func TestCloseMaintenanceRun(t *testing.T) {
	gdb := testdb.New(t)
	store := repo.New(gdb)
	user := seedUser(t, gdb, "close-run", "close-run@maint.test")
	conn := seedConnection(t, gdb, "close-run-src", user)
	engine := seedEngine(t, gdb, "close-run-e", seedConnection(t, gdb, "close-run-eng", user), user)
	catalog, binding := seedCatalogBinding(t, gdb, conn, engine, "close_run_lake")
	a := &Activities{store: store, logger: slog.New(slog.DiscardHandler)}

	tests := []struct {
		name       string
		table      string
		failure    string
		preStatus  string
		wantStatus string
		wantMsg    string
	}{
		{name: "success closes a running run as finished", table: "t_ok", wantStatus: "finished"},
		{name: "failure closes a running run as failed", table: "t_fail", failure: "activity StartToClose timeout", wantStatus: "failed", wantMsg: "activity StartToClose timeout"},
		{name: "a run the activity closed is left alone", table: "t_done", failure: "late failure", preStatus: "finished", wantStatus: "finished"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := seedRunningRun(t, gdb, store, catalog, binding, user, tt.table, time.Minute)
			if tt.preStatus != "" {
				finishMaintenanceRun(context.Background(), store, repo.UUIDString(run.ID), tt.preStatus, "", "")
			}

			err := a.closeMaintenanceRun(context.Background(), CloseMaintenanceRunParams{RunID: repo.UUIDString(run.ID), Failure: tt.failure})

			if err != nil {
				t.Fatal(err)
			}
			got, err := store.GetMaintenanceRun(context.Background(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", got.Status, tt.wantStatus)
			}
			if tt.wantMsg != "" && (got.ErrorMessage == nil || *got.ErrorMessage != tt.wantMsg) {
				t.Fatalf("error message = %v, want %q", got.ErrorMessage, tt.wantMsg)
			}
		})
	}

	t.Run("a missing run is an error", func(t *testing.T) {
		if err := a.closeMaintenanceRun(context.Background(), CloseMaintenanceRunParams{RunID: repo.NewUUID()}); err == nil {
			t.Fatal("closing a run that does not exist succeeded")
		}
	})
}

// TestReconcileMaintenanceRuns: a run left running whose workflow Temporal
// reports closed is closed with that outcome; a live, unreadable or just
// started one is left alone.
func TestReconcileMaintenanceRuns(t *testing.T) {
	gdb := testdb.New(t)
	store := repo.New(gdb)
	user := seedUser(t, gdb, "reconcile", "reconcile@maint.test")
	conn := seedConnection(t, gdb, "reconcile-src", user)
	engine := seedEngine(t, gdb, "reconcile-e", seedConnection(t, gdb, "reconcile-eng", user), user)
	catalog, binding := seedCatalogBinding(t, gdb, conn, engine, "reconcile_lake")

	describe := func(s enumspb.WorkflowExecutionStatus) *workflowservice.DescribeWorkflowExecutionResponse {
		return &workflowservice.DescribeWorkflowExecutionResponse{
			WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: s},
		}
	}
	tests := []struct {
		name       string
		age        time.Duration
		describe   *workflowservice.DescribeWorkflowExecutionResponse
		describeEr error
		asked      bool
		wantStatus string
		wantCode   string
	}{
		{name: "completed workflow finishes the run", age: time.Hour, describe: describe(enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED), asked: true, wantStatus: "finished"},
		{name: "failed workflow fails the run", age: time.Hour, describe: describe(enumspb.WORKFLOW_EXECUTION_STATUS_FAILED), asked: true, wantStatus: "failed", wantCode: "maintenance_failed"},
		{name: "timed out workflow fails the run", age: time.Hour, describe: describe(enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT), asked: true, wantStatus: "failed", wantCode: "maintenance_failed"},
		{name: "a workflow that no longer exists fails the run", age: time.Hour, describeEr: serviceerror.NewNotFound("gone"), asked: true, wantStatus: "failed", wantCode: "maintenance_failed"},
		{name: "a running workflow leaves the run", age: time.Hour, describe: describe(enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING), asked: true, wantStatus: "running"},
		{name: "an unreadable workflow leaves the run", age: time.Hour, describeEr: errors.New("temporal down"), asked: true, wantStatus: "running"},
		{name: "a run that just started is not asked about", age: time.Second, wantStatus: "running"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := seedRunningRun(t, gdb, store, catalog, binding, user, fmt.Sprintf("t%d", i), tt.age)
			tc := &mocks.Client{}
			if tt.asked {
				tc.On("DescribeWorkflowExecution", mock.Anything, run.WorkflowID, "").Return(tt.describe, tt.describeEr)
			}
			a := &Activities{store: store, logger: slog.New(slog.DiscardHandler), temporalClient: tc}

			a.reconcileMaintenanceRuns(context.Background(), catalog)

			got, err := store.GetMaintenanceRun(context.Background(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", got.Status, tt.wantStatus)
			}
			if tt.wantCode != "" && (got.ErrorCode == nil || *got.ErrorCode != tt.wantCode) {
				t.Fatalf("error code = %v, want %q", got.ErrorCode, tt.wantCode)
			}
			tc.AssertExpectations(t)
			// Close the run so the next case's table is the only one running.
			finishMaintenanceRun(context.Background(), store, repo.UUIDString(run.ID), "finished", "", "")
		})
	}
}

// TestCloseRunningMaintenanceRun_KeepsAClosedOutcome: a run that ended while
// the caller was deciding to close it keeps the outcome recorded for it.
func TestCloseRunningMaintenanceRun_KeepsAClosedOutcome(t *testing.T) {
	gdb := testdb.New(t)
	store := repo.New(gdb)
	user := seedUser(t, gdb, "keep-closed", "keep-closed@maint.test")
	conn := seedConnection(t, gdb, "keep-closed-src", user)
	engine := seedEngine(t, gdb, "keep-closed-e", seedConnection(t, gdb, "keep-closed-eng", user), user)
	catalog, binding := seedCatalogBinding(t, gdb, conn, engine, "keep_closed_lake")
	run := seedRunningRun(t, gdb, store, catalog, binding, user, "orders", time.Hour)
	ctx := context.Background()
	id := repo.UUIDString(run.ID)

	finishMaintenanceRun(ctx, store, id, "gave_up", "commit_conflict", "lost the race")
	err := closeRunningMaintenanceRun(ctx, store, id, "finished", "", "")

	if err != nil {
		t.Fatalf("closing a closed run = %v, want nil", err)
	}
	got, err := store.GetMaintenanceRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "gave_up" || got.ErrorCode == nil || *got.ErrorCode != "commit_conflict" {
		t.Fatalf("run = %q %v, want the recorded gave_up/commit_conflict kept", got.Status, got.ErrorCode)
	}
}

// failingCloseStore refuses to close one run, as a database error would.
type failingCloseStore struct {
	*repo.Repo
	failFor db.UUID
}

func (s failingCloseStore) UpdateMaintenanceRun(ctx context.Context, arg db.UpdateMaintenanceRunParams) (db.MaintenanceRun, error) {
	if arg.ID == s.failFor {
		return db.MaintenanceRun{}, errors.New("database down")
	}
	return s.Repo.UpdateMaintenanceRun(ctx, arg)
}

// TestReconcileMaintenanceRuns_ContinuesPastAFailedWrite: one run that cannot
// be closed does not stop the others from being closed.
func TestReconcileMaintenanceRuns_ContinuesPastAFailedWrite(t *testing.T) {
	gdb := testdb.New(t)
	store := repo.New(gdb)
	user := seedUser(t, gdb, "reconcile-fail", "reconcile-fail@maint.test")
	conn := seedConnection(t, gdb, "reconcile-fail-src", user)
	engine := seedEngine(t, gdb, "reconcile-fail-e", seedConnection(t, gdb, "reconcile-fail-eng", user), user)
	catalog, binding := seedCatalogBinding(t, gdb, conn, engine, "reconcile_fail_lake")
	stuck := seedRunningRun(t, gdb, store, catalog, binding, user, "stuck", time.Hour)
	other := seedRunningRun(t, gdb, store, catalog, binding, user, "other", time.Hour)
	tc := &mocks.Client{}
	done := &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED},
	}
	tc.On("DescribeWorkflowExecution", mock.Anything, mock.Anything, "").Return(done, nil)
	a := &Activities{
		store:          failingCloseStore{Repo: store, failFor: stuck.ID},
		logger:         slog.New(slog.DiscardHandler),
		temporalClient: tc,
	}

	a.reconcileMaintenanceRuns(context.Background(), catalog)

	for name, tt := range map[string]struct {
		id   db.UUID
		want string
	}{"the run that cannot be written stays running": {stuck.ID, "running"}, "the other run is closed": {other.ID, "finished"}} {
		got, err := store.GetMaintenanceRun(context.Background(), tt.id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != tt.want {
			t.Errorf("%s: status = %q, want %q", name, got.Status, tt.want)
		}
	}
}
