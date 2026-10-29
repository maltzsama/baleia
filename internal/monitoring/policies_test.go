package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/monitoring/maintenance"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// policyStore keeps one table's parameters, runs and health in memory, with
// the same "no row" answers as the SQL.
type policyStore struct {
	fakeStore
	policy  *db.TableMaintenancePolicy
	health  *db.TableHealthSnapshot
	applied *db.GetAppliedCatalogRetentionRow
	last    []db.LastSuccessfulMaintenanceByOperationRow
	running bool
	created []db.CreatePlannedMaintenanceRunParams
}

func (s *policyStore) GetTableMaintenancePolicy(context.Context, db.GetTableMaintenancePolicyParams) (db.TableMaintenancePolicy, error) {
	if s.policy == nil {
		return db.TableMaintenancePolicy{}, pgx.ErrNoRows
	}
	return *s.policy, nil
}

func (s *policyStore) CreateTableMaintenancePolicy(_ context.Context, a db.CreateTableMaintenancePolicyParams) (db.TableMaintenancePolicy, error) {
	if s.policy != nil {
		return db.TableMaintenancePolicy{}, pgx.ErrNoRows
	}
	s.policy = &db.TableMaintenancePolicy{
		CatalogID: a.CatalogID, Namespace: a.Namespace, TableName: a.TableName, Mode: a.Mode,
		WriterCommitIntervalSeconds: a.WriterCommitIntervalSeconds, Optimize: a.Optimize,
		OptimizeManifests: a.OptimizeManifests, ExpireSnapshots: a.ExpireSnapshots,
		RemoveOrphanFiles: a.RemoveOrphanFiles, Revision: 1,
		CreatedAt: pgtype.Timestamptz{Time: time.Unix(0, 0).UTC(), Valid: true},
	}
	return *s.policy, nil
}

func (s *policyStore) UpdateTableMaintenancePolicy(_ context.Context, a db.UpdateTableMaintenancePolicyParams) (db.TableMaintenancePolicy, error) {
	if s.policy == nil || s.policy.Revision != a.Revision {
		return db.TableMaintenancePolicy{}, pgx.ErrNoRows
	}
	p := *s.policy
	p.Mode, p.Optimize, p.Revision = a.Mode, a.Optimize, p.Revision+1
	s.policy = &p
	return p, nil
}

func (s *policyStore) DeleteTableMaintenancePolicy(context.Context, db.DeleteTableMaintenancePolicyParams) (db.TableMaintenancePolicy, error) {
	if s.policy == nil {
		return db.TableMaintenancePolicy{}, pgx.ErrNoRows
	}
	p := *s.policy
	s.policy = nil
	return p, nil
}

func (s *policyStore) LastSuccessfulMaintenanceByOperation(context.Context, db.LastSuccessfulMaintenanceByOperationParams) ([]db.LastSuccessfulMaintenanceByOperationRow, error) {
	return s.last, nil
}

func (s *policyStore) GetRunningMaintenanceRun(context.Context, db.GetRunningMaintenanceRunParams) (db.MaintenanceRun, error) {
	if !s.running {
		return db.MaintenanceRun{}, pgx.ErrNoRows
	}
	return db.MaintenanceRun{Status: "running"}, nil
}

func (s *policyStore) ListTableMaintenanceRuns(context.Context, db.ListTableMaintenanceRunsParams) ([]db.MaintenanceRun, error) {
	return nil, nil
}

func (s *policyStore) CreatePlannedMaintenanceRun(_ context.Context, a db.CreatePlannedMaintenanceRunParams) (db.MaintenanceRun, error) {
	s.created = append(s.created, a)
	return db.MaintenanceRun{Operations: a.Operations, Status: "dry_run", Trigger: a.Trigger}, nil
}

func (s *policyStore) GetAppliedCatalogRetention(context.Context, db.GetAppliedCatalogRetentionParams) (db.GetAppliedCatalogRetentionRow, error) {
	if s.applied == nil {
		return db.GetAppliedCatalogRetentionRow{}, pgx.ErrNoRows
	}
	return *s.applied, nil
}

func (s *policyStore) GetTableHealthByTable(context.Context, db.GetTableHealthByTableParams) (db.TableHealthSnapshot, error) {
	if s.health == nil {
		return db.TableHealthSnapshot{}, pgx.ErrNoRows
	}
	return *s.health, nil
}

var table = TableKey{CatalogID: pgtype.UUID{Bytes: [16]byte{0xCA}, Valid: true}, Namespace: "cdc", Table: "orders"}

func p64(v int64) *int64 { return &v }

// expiryPolicy expires snapshots past 100, at most hourly.
func expiryPolicy(mode string) maintenance.Policy {
	return maintenance.Policy{
		Mode: mode, WriterCommitIntervalSeconds: 60,
		ExpireSnapshots: maintenance.Settings{
			Enabled: true, Trigger: maintenance.Trigger{Snapshots: p64(100)}, MinIntervalSeconds: 3600,
			TimeoutSeconds: 600, OnConflict: maintenance.RetryWithBackoff,
			Params: maintenance.Params{RetentionSeconds: p64(7 * 86400)},
		},
	}
}

func TestService_RetentionFloor(t *testing.T) {
	t.Parallel()
	week := maintenance.UnmanagedRetentionFloor
	row := func(props, by string) *db.GetAppliedCatalogRetentionRow {
		return &db.GetAppliedCatalogRetentionRow{Properties: []byte(props), UpdatedBy: by}
	}
	both := `{"iceberg.expire-snapshots.min-retention": "1h", "iceberg.remove-orphan-files.min-retention": "90m"}`
	tests := []struct {
		name    string
		mode    string
		applied *db.GetAppliedCatalogRetentionRow
		want    time.Duration
	}{
		{"loaded floor, the larger key", "managed", row(both, "trino"), 90 * time.Minute},
		{"fractional duration", "managed", row(`{"iceberg.expire-snapshots.min-retention": "1.5h", "iceberg.remove-orphan-files.min-retention": "1h"}`, "trino"), 90 * time.Minute},
		{"key missing is Trino's default", "managed", row(`{"iceberg.expire-snapshots.min-retention": "1h"}`, "trino"), week},
		{"not confirmed by the plugin", "managed", row(both, "baleia"), week},
		{"not in the registry", "managed", nil, week},
		{"adopted binding", "adopted", row(both, "trino"), week},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := New(&policyStore{applied: tt.applied})
			got, err := svc.RetentionFloor(context.Background(), db.CatalogBinding{Mode: tt.mode, EngineCatalogName: "lake"})
			if err != nil || got != tt.want {
				t.Fatalf("RetentionFloor() = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestService_SetTablePolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &policyStore{}
	svc := New(store)

	if _, err := svc.TablePolicy(ctx, table); !errors.Is(err, ErrPolicyNotFound) {
		t.Fatalf("TablePolicy() error = %v, want ErrPolicyNotFound", err)
	}
	created, before, err := svc.SetTablePolicy(ctx, table, expiryPolicy(maintenance.ModeManaged), nil, pgtype.UUID{})
	if err != nil || created.Revision != 1 || before.Revision != 0 {
		t.Fatalf("create = %+v, before %+v, %v", created, before, err)
	}
	if _, _, err := svc.SetTablePolicy(ctx, table, expiryPolicy(maintenance.ModeManaged), nil, pgtype.UUID{}); !errors.Is(err, ErrPolicyRevision) {
		t.Fatalf("second create error = %v, want ErrPolicyRevision", err)
	}
	stale := int64(7)
	if _, _, err := svc.SetTablePolicy(ctx, table, expiryPolicy(maintenance.ModeOff), &stale, pgtype.UUID{}); !errors.Is(err, ErrPolicyRevision) {
		t.Fatalf("stale update error = %v, want ErrPolicyRevision", err)
	}
	current := int64(1)
	updated, before, err := svc.SetTablePolicy(ctx, table, expiryPolicy(maintenance.ModeOff), &current, pgtype.UUID{})
	if err != nil || updated.Revision != 2 || updated.Mode != maintenance.ModeOff || before.Mode != maintenance.ModeManaged {
		t.Fatalf("update = %+v, before %+v, %v", updated, before, err)
	}

	decoded, err := DecodePolicy(*store.policy)
	if err != nil || !decoded.ExpireSnapshots.Enabled || *decoded.ExpireSnapshots.Params.RetentionSeconds != 7*86400 {
		t.Fatalf("DecodePolicy() = %+v, %v", decoded, err)
	}

	if _, err := svc.DeleteTablePolicy(ctx, table); err != nil {
		t.Fatalf("DeleteTablePolicy() error = %v", err)
	}
	if _, err := svc.DeleteTablePolicy(ctx, table); !errors.Is(err, ErrPolicyNotFound) {
		t.Fatalf("second delete error = %v, want ErrPolicyNotFound", err)
	}
}

func TestService_PlanTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		health   *db.TableHealthSnapshot
		last     []db.LastSuccessfulMaintenanceByOperationRow
		running  bool
		wantRuns string
		blocked  string
	}{
		{name: "fired by the health pass", health: &db.TableHealthSnapshot{SnapshotCount: 140}, wantRuns: maintenance.OpExpireSnapshots},
		{name: "not seen by the health pass yet", blocked: maintenance.BlockedNotFired},
		{name: "ran recently", health: &db.TableHealthSnapshot{SnapshotCount: 140},
			last: []db.LastSuccessfulMaintenanceByOperationRow{{
				Operation: maintenance.OpExpireSnapshots, FinishedAt: pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true},
			}}, blocked: maintenance.BlockedMinInterval},
		{name: "a run is in progress", health: &db.TableHealthSnapshot{SnapshotCount: 140}, running: true, blocked: maintenance.BlockedRunning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := &policyStore{health: tt.health, last: tt.last, running: tt.running}
			svc := New(store)
			row, _, err := svc.SetTablePolicy(ctx, table, expiryPolicy(maintenance.ModeManaged), nil, pgtype.UUID{})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := svc.PlanTable(ctx, table, row, maintenance.DryRun, now)
			if err != nil {
				t.Fatalf("PlanTable() error = %v", err)
			}
			next, ok := plan.Next()
			if next.Operation != tt.wantRuns || ok != (tt.wantRuns != "") {
				t.Fatalf("next = %+v, %v; want %q", next, ok, tt.wantRuns)
			}
			if tt.blocked != "" {
				for _, s := range plan.Steps {
					if s.Operation == maintenance.OpExpireSnapshots && s.Reason.Blocked != tt.blocked {
						t.Fatalf("expiry blocked by %q, want %q", s.Reason.Blocked, tt.blocked)
					}
				}
			}

			run, err := svc.RecordDryRun(ctx, table, row, plan, pgtype.UUID{})
			if err != nil {
				t.Fatalf("RecordDryRun() error = %v", err)
			}
			got := store.created[0]
			if got.Trigger != "dry_run" || run.Status != "dry_run" {
				t.Fatalf("dry run recorded as %+v", got)
			}
			if tt.wantRuns == "" {
				if len(got.Operations) != 0 || string(got.Parameters) != `{}` {
					t.Fatalf("an empty plan recorded %+v", got)
				}
				return
			}
			var params maintenance.Settings
			if err := json.Unmarshal(got.Parameters, &params); err != nil || !params.Enabled {
				t.Fatalf("parameters not frozen: %s, %v", got.Parameters, err)
			}
			var reason maintenance.Reason
			if err := json.Unmarshal(got.Reason, &reason); err != nil || reason.Fired != "snapshots" {
				t.Fatalf("reason = %s, %v", got.Reason, err)
			}
		})
	}
}

// TestService_TableMetrics_FileTriggers: the counts the health pass stores
// reach the planner, so optimize and optimize_manifests fire on them and
// not only on their maximum interval.
func TestService_TableMetrics_FileTriggers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	settings := func(trigger maintenance.Trigger) maintenance.Settings {
		return maintenance.Settings{
			Enabled: true, Trigger: trigger, MinIntervalSeconds: 3600,
			TimeoutSeconds: 600, OnConflict: maintenance.RetryWithBackoff,
		}
	}
	manifests := int32(150)
	tests := []struct {
		name   string
		policy maintenance.Policy
		health db.TableHealthSnapshot
		want   string
		fired  string
	}{
		{
			name:   "delete files fire optimize",
			policy: maintenance.Policy{Mode: maintenance.ModeManaged, WriterCommitIntervalSeconds: 60, Optimize: settings(maintenance.Trigger{DeleteFiles: p64(100)})},
			health: db.TableHealthSnapshot{SnapshotCount: 3, DeleteFileCount: p64(240)},
			want:   maintenance.OpOptimize, fired: "delete_files",
		},
		{
			name:   "manifests fire optimize_manifests",
			policy: maintenance.Policy{Mode: maintenance.ModeManaged, WriterCommitIntervalSeconds: 60, OptimizeManifests: settings(maintenance.Trigger{Manifests: p64(100)})},
			health: db.TableHealthSnapshot{SnapshotCount: 3, ManifestCount: &manifests},
			want:   maintenance.OpOptimizeManifests, fired: "manifests",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := &policyStore{health: &tt.health}
			svc := New(store)
			row, _, err := svc.SetTablePolicy(ctx, table, tt.policy, nil, pgtype.UUID{})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := svc.PlanTable(ctx, table, row, maintenance.DryRun, now)
			if err != nil {
				t.Fatal(err)
			}
			next, ok := plan.Next()
			if !ok || next.Operation != tt.want || next.Reason.Fired != tt.fired {
				t.Fatalf("next = %+v, %v; want %s fired by %s", next, ok, tt.want, tt.fired)
			}
		})
	}
}

func TestService_PlanTable_MaintainedElsewhere(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := New(&policyStore{})
	row, _, err := svc.SetTablePolicy(ctx, table, expiryPolicy(maintenance.ModeMaintainedElsewhere), nil, pgtype.UUID{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PlanTable(ctx, table, row, maintenance.DryRun, time.Now()); !errors.Is(err, maintenance.ErrMaintainedElsewhere) {
		t.Fatalf("PlanTable() error = %v, want ErrMaintainedElsewhere", err)
	}
}
