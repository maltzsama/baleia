package activities

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/monitoring"
	"github.com/galileostudio/baleia/internal/monitoring/maintenance"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
)

func i64(v int64) *int64 { return &v }

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
	var user, conn, engConn, engine pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (name, email, role, password_hash) VALUES ('m', 'm@maint.test', 'admin', 'x') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		dst  *pgtype.UUID
	}{{"src", &conn}, {"eng", &engConn}} {
		if err := pool.QueryRow(ctx, `INSERT INTO catalog_connections (name, type, url, created_by) VALUES ($1, 'polaris', 'http://p.test', $2) RETURNING id`, c.name, user).Scan(c.dst); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO engines (name, kind, class, connection_id, identity_mode, created_by)
		VALUES ('e', 'trino', 'federated', $1, 'connection', $2) RETURNING id`, engConn, user).Scan(&engine); err != nil {
		t.Fatal(err)
	}
	const catalogs, tables, limit = 4, 3, 5
	a := &Activities{queries: db.New(pool), pool: pool, cfg: &config.Config{MaintenanceMaxConcurrentRuns: limit}}
	type round struct {
		binding    db.CatalogBinding
		candidates []roundCandidate
	}
	rounds := make([]round, catalogs)
	for i := range rounds {
		var catalogID pgtype.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO catalogs (connection_id, name, trino_catalog_name, cluster_id, warehouse, connector)
			VALUES ($1, $2, $2, (SELECT id FROM trino_clusters LIMIT 1), 'wh', 'iceberg') RETURNING id`, conn, fmt.Sprintf("lake%d", i)).Scan(&catalogID); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO catalog_bindings (catalog_id, engine_id, engine_catalog_name, mode, enabled, lifecycle_state)
			VALUES ($1, $2, $3, 'managed', true, 'active') RETURNING id`, catalogID, engine, fmt.Sprintf("lake%d", i)).Scan(&rounds[i].binding.ID); err != nil {
			t.Fatal(err)
		}
		for j := range tables {
			key := monitoring.TableKey{CatalogID: catalogID, Namespace: "cdc", Table: fmt.Sprintf("t%d", j)}
			step := maintenance.Step{Operation: maintenance.OpExpireSnapshots, Due: true, WouldRun: true}
			rounds[i].candidates = append(rounds[i].candidates, roundCandidate{key: key, step: step})
		}
	}
	// The first catalog's first table is already running.
	first := rounds[0].candidates[0]
	if _, err := RecordMaintenanceRun(ctx, db.New(pool), first.key, rounds[0].binding, first.row, first.step, first.settings, "manual", pgtype.UUID{}); err != nil {
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
	var running int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM maintenance_runs WHERE status = 'running'`).Scan(&running); err != nil {
		t.Fatal(err)
	}
	if running != limit {
		t.Fatalf("running runs = %d, want the limit %d", running, limit)
	}
}
