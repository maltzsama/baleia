package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/monitoring/maintenance"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Policies reads and writes per-table maintenance parameters (#500);
// *db.Queries implements it.
type Policies interface {
	GetTableMaintenancePolicy(ctx context.Context, arg db.GetTableMaintenancePolicyParams) (db.TableMaintenancePolicy, error)
	CreateTableMaintenancePolicy(ctx context.Context, arg db.CreateTableMaintenancePolicyParams) (db.TableMaintenancePolicy, error)
	UpdateTableMaintenancePolicy(ctx context.Context, arg db.UpdateTableMaintenancePolicyParams) (db.TableMaintenancePolicy, error)
	DeleteTableMaintenancePolicy(ctx context.Context, arg db.DeleteTableMaintenancePolicyParams) (db.TableMaintenancePolicy, error)
	LastSuccessfulMaintenanceByOperation(ctx context.Context, arg db.LastSuccessfulMaintenanceByOperationParams) ([]db.LastSuccessfulMaintenanceByOperationRow, error)
	GetRunningMaintenanceRun(ctx context.Context, arg db.GetRunningMaintenanceRunParams) (db.MaintenanceRun, error)
	ListTableMaintenanceRuns(ctx context.Context, arg db.ListTableMaintenanceRunsParams) ([]db.MaintenanceRun, error)
	CreatePlannedMaintenanceRun(ctx context.Context, arg db.CreatePlannedMaintenanceRunParams) (db.MaintenanceRun, error)
	GetTableHealthByTable(ctx context.Context, arg db.GetTableHealthByTableParams) (db.TableHealthSnapshot, error)
}

// TableKey names one table of a catalog.
type TableKey struct {
	CatalogID pgtype.UUID
	Namespace string
	Table     string
}

var (
	// ErrPolicyNotFound: the table has no maintenance parameters.
	ErrPolicyNotFound = errors.New("table has no maintenance parameters")
	// ErrPolicyRevision: the parameters changed since the caller read them,
	// or were created by someone else meanwhile.
	ErrPolicyRevision = errors.New("maintenance parameters changed meanwhile")
)

// DefaultTableRuns is how many recent runs a table's maintenance view shows.
const DefaultTableRuns = 10

// TablePolicy returns the table's stored parameters.
func (s *Service) TablePolicy(ctx context.Context, k TableKey) (db.TableMaintenancePolicy, error) {
	row, err := s.store.GetTableMaintenancePolicy(ctx, db.GetTableMaintenancePolicyParams{
		CatalogID: k.CatalogID, Namespace: k.Namespace, TableName: k.Table,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.TableMaintenancePolicy{}, ErrPolicyNotFound
	}
	return row, err
}

// SetTablePolicy stores p. With revision nil it creates the parameters and
// fails with ErrPolicyRevision when they already exist; otherwise it replaces
// them only at that revision. It returns the stored row and the previous one
// (zero when created).
func (s *Service) SetTablePolicy(ctx context.Context, k TableKey, p maintenance.Policy, revision *int64, actor pgtype.UUID) (stored, before db.TableMaintenancePolicy, err error) {
	ops, err := encodeOperations(p)
	if err != nil {
		return db.TableMaintenancePolicy{}, db.TableMaintenancePolicy{}, err
	}
	interval := toInt32(int(p.WriterCommitIntervalSeconds))
	if revision == nil {
		stored, err = s.store.CreateTableMaintenancePolicy(ctx, db.CreateTableMaintenancePolicyParams{
			CatalogID: k.CatalogID, Namespace: k.Namespace, TableName: k.Table, Mode: p.Mode,
			WriterCommitIntervalSeconds: interval,
			Optimize:                    ops[maintenance.OpOptimize],
			OptimizeManifests:           ops[maintenance.OpOptimizeManifests],
			ExpireSnapshots:             ops[maintenance.OpExpireSnapshots],
			RemoveOrphanFiles:           ops[maintenance.OpRemoveOrphanFiles],
			Actor:                       actor,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return db.TableMaintenancePolicy{}, db.TableMaintenancePolicy{}, ErrPolicyRevision
		}
		return stored, db.TableMaintenancePolicy{}, err
	}
	before, err = s.TablePolicy(ctx, k)
	if err != nil {
		return db.TableMaintenancePolicy{}, db.TableMaintenancePolicy{}, err
	}
	stored, err = s.store.UpdateTableMaintenancePolicy(ctx, db.UpdateTableMaintenancePolicyParams{
		CatalogID: k.CatalogID, Namespace: k.Namespace, TableName: k.Table, Mode: p.Mode,
		WriterCommitIntervalSeconds: interval,
		Optimize:                    ops[maintenance.OpOptimize],
		OptimizeManifests:           ops[maintenance.OpOptimizeManifests],
		ExpireSnapshots:             ops[maintenance.OpExpireSnapshots],
		RemoveOrphanFiles:           ops[maintenance.OpRemoveOrphanFiles],
		Actor:                       actor,
		Revision:                    *revision,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.TableMaintenancePolicy{}, db.TableMaintenancePolicy{}, ErrPolicyRevision
	}
	return stored, before, err
}

// DeleteTablePolicy removes the table's parameters and returns them.
func (s *Service) DeleteTablePolicy(ctx context.Context, k TableKey) (db.TableMaintenancePolicy, error) {
	row, err := s.store.DeleteTableMaintenancePolicy(ctx, db.DeleteTableMaintenancePolicyParams{
		CatalogID: k.CatalogID, Namespace: k.Namespace, TableName: k.Table,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.TableMaintenancePolicy{}, ErrPolicyNotFound
	}
	return row, err
}

// DecodePolicy turns a stored row into parameters.
func DecodePolicy(row db.TableMaintenancePolicy) (maintenance.Policy, error) {
	p := maintenance.Policy{Mode: row.Mode, WriterCommitIntervalSeconds: int64(row.WriterCommitIntervalSeconds)}
	for _, op := range []struct {
		raw []byte
		dst *maintenance.Settings
	}{
		{row.Optimize, &p.Optimize},
		{row.OptimizeManifests, &p.OptimizeManifests},
		{row.ExpireSnapshots, &p.ExpireSnapshots},
		{row.RemoveOrphanFiles, &p.RemoveOrphanFiles},
	} {
		s, err := maintenance.DecodeSettings(op.raw)
		if err != nil {
			return maintenance.Policy{}, err
		}
		*op.dst = s
	}
	return p, nil
}

func encodeOperations(p maintenance.Policy) (map[string][]byte, error) {
	out := make(map[string][]byte, len(maintenance.Priority))
	for _, op := range maintenance.Priority {
		raw, err := json.Marshal(p.Operation(op))
		if err != nil {
			return nil, err
		}
		out[op] = raw
	}
	return out, nil
}

// MaintenanceBinding picks the binding maintenance runs on: an enabled one
// on a capable engine, a managed one first, since only managed bindings carry
// Baleia's minimum retention.
func MaintenanceBinding(ctx context.Context, bindings []db.CatalogBinding, capable Capable) (db.CatalogBinding, bool) {
	var other *db.CatalogBinding
	for i, b := range bindings {
		if !b.Enabled || !capable(ctx, b.EngineID) {
			continue
		}
		if b.Mode == "managed" {
			return b, true
		}
		if other == nil {
			other = &bindings[i]
		}
	}
	if other != nil {
		return *other, true
	}
	return db.CatalogBinding{}, false
}

// RetentionFloor is the shortest retention the binding's engine accepts for
// expire_snapshots and remove_orphan_files: Baleia's projected minimum on a
// managed binding, Trino's default on an adopted or native one, which Baleia
// never changes.
func RetentionFloor(b db.CatalogBinding, managedFloor time.Duration) time.Duration {
	if b.Mode == "managed" && managedFloor > 0 {
		return managedFloor
	}
	return maintenance.UnmanagedRetentionFloor
}

// TableState reads what the table's runs say: the last success of each
// operation and whether a run is in progress. since is when the parameters
// took effect.
func (s *Service) TableState(ctx context.Context, k TableKey, since time.Time) (maintenance.State, error) {
	rows, err := s.store.LastSuccessfulMaintenanceByOperation(ctx, db.LastSuccessfulMaintenanceByOperationParams{
		CatalogID: k.CatalogID, Namespace: k.Namespace, TableName: k.Table,
	})
	if err != nil {
		return maintenance.State{}, err
	}
	st := maintenance.State{LastSuccess: make(map[string]time.Time, len(rows)), Since: since}
	for _, r := range rows {
		if r.FinishedAt.Valid {
			st.LastSuccess[r.Operation] = r.FinishedAt.Time
		}
	}
	_, err = s.store.GetRunningMaintenanceRun(ctx, db.GetRunningMaintenanceRunParams{
		CatalogID: k.CatalogID, Namespace: k.Namespace, TableName: k.Table,
	})
	switch {
	case err == nil:
		st.Running = true
	case !errors.Is(err, pgx.ErrNoRows):
		return maintenance.State{}, err
	}
	return st, nil
}

// TableMetrics reads the latest health pass of the table; a table the pass
// has not seen has no metrics, and nothing fires on it.
func (s *Service) TableMetrics(ctx context.Context, k TableKey) (maintenance.Metrics, error) {
	row, err := s.store.GetTableHealthByTable(ctx, db.GetTableHealthByTableParams{
		CatalogID: k.CatalogID, Namespace: k.Namespace, TableName: k.Table,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return maintenance.Metrics{}, nil
	}
	if err != nil {
		return maintenance.Metrics{}, err
	}
	m := maintenance.Metrics{}
	snapshots := int64(row.SnapshotCount)
	m.Snapshots = &snapshots
	if row.FileCount != nil {
		v := int64(*row.FileCount)
		m.DataFiles = &v
	}
	if row.OldestSnapshotAgeHours != nil {
		v := time.Duration(*row.OldestSnapshotAgeHours) * time.Hour
		m.OldestSnapshotAge = &v
	}
	return m, nil
}

// TableRuns lists the table's most recent runs.
func (s *Service) TableRuns(ctx context.Context, k TableKey) ([]db.MaintenanceRun, error) {
	return s.store.ListTableMaintenanceRuns(ctx, db.ListTableMaintenanceRunsParams{
		CatalogID: k.CatalogID, Namespace: k.Namespace, TableName: k.Table, Lim: DefaultTableRuns,
	})
}

// PlanTable evaluates the table's parameters now, as kind asks.
func (s *Service) PlanTable(ctx context.Context, k TableKey, row db.TableMaintenancePolicy, kind maintenance.Kind, now time.Time) (maintenance.Plan, error) {
	p, err := DecodePolicy(row)
	if err != nil {
		return maintenance.Plan{}, err
	}
	metrics, err := s.TableMetrics(ctx, k)
	if err != nil {
		return maintenance.Plan{}, err
	}
	st, err := s.TableState(ctx, k, row.CreatedAt.Time)
	if err != nil {
		return maintenance.Plan{}, err
	}
	return maintenance.Evaluate(p, metrics, st, kind, now)
}

// RecordDryRun stores a dry run: finished at once, with the operation the
// plan would run (none when nothing is due) and its reason.
func (s *Service) RecordDryRun(ctx context.Context, k TableKey, row db.TableMaintenancePolicy, plan maintenance.Plan, actor pgtype.UUID) (db.MaintenanceRun, error) {
	ops := []string{}
	reason := maintenance.Reason{}
	params := []byte(`{}`)
	if next, ok := plan.Next(); ok {
		ops = []string{next.Operation}
		reason = next.Reason
		p, err := DecodePolicy(row)
		if err != nil {
			return db.MaintenanceRun{}, err
		}
		if params, err = json.Marshal(p.Operation(next.Operation)); err != nil {
			return db.MaintenanceRun{}, err
		}
	}
	rawReason, err := json.Marshal(reason)
	if err != nil {
		return db.MaintenanceRun{}, err
	}
	revision := row.Revision
	return s.store.CreatePlannedMaintenanceRun(ctx, db.CreatePlannedMaintenanceRunParams{
		CatalogID: k.CatalogID, Namespace: k.Namespace, TableName: k.Table, Operations: ops,
		StartedBy: actor, WorkflowID: "", Trigger: string(maintenance.DryRun),
		Reason: rawReason, Parameters: params, PolicyRevision: &revision,
	})
}
