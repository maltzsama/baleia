// Package monitoring reads what the engines report and runs Iceberg table
// maintenance (#117): cluster snapshots, query snapshots, table health, and
// maintenance runs with their validation and binding choice. The caller
// checks access to the engine or catalog and starts the maintenance
// workflow.
package monitoring

import (
	"context"
	"errors"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Engines reads engine snapshots; *db.Queries implements it.
type Engines interface {
	ListEngines(ctx context.Context) ([]db.Engine, error)
	GetLatestClusterSnapshotByEngine(ctx context.Context, engineID pgtype.UUID) (db.ClusterSnapshot, error)
	ListClusterSnapshotsByEngine(ctx context.Context, arg db.ListClusterSnapshotsByEngineParams) ([]db.ClusterSnapshot, error)
	ListTrinoQuerySnapshotsByEngine(ctx context.Context, arg db.ListTrinoQuerySnapshotsByEngineParams) ([]db.TrinoQuerySnapshot, error)
	GetTrinoQuerySnapshotByEngine(ctx context.Context, arg db.GetTrinoQuerySnapshotByEngineParams) (db.TrinoQuerySnapshot, error)
}

// Catalogs reads catalog health and maintenance and records runs;
// *db.Queries implements it.
type Catalogs interface {
	GetCatalogByID(ctx context.Context, id pgtype.UUID) (db.Catalog, error)
	ListTableHealthByCatalog(ctx context.Context, arg db.ListTableHealthByCatalogParams) ([]db.TableHealthSnapshot, error)
	ListBindingsByCatalog(ctx context.Context, catalogID pgtype.UUID) ([]db.CatalogBinding, error)
	ListMaintenanceRuns(ctx context.Context, arg db.ListMaintenanceRunsParams) ([]db.MaintenanceRun, error)
	CreateMaintenanceRun(ctx context.Context, arg db.CreateMaintenanceRunParams) (db.MaintenanceRun, error)
	UpdateMaintenanceRun(ctx context.Context, arg db.UpdateMaintenanceRunParams) (db.MaintenanceRun, error)
}

// Store is Engines and Catalogs.
type Store interface {
	Engines
	Catalogs
	Policies
}

// Operations are the maintenance operations, in the order they run.
var Operations = []string{"optimize", "expire_snapshots", "remove_orphan_files"}

// RetentionRe is a retention such as 7d, 12h or 30m.
var RetentionRe = regexp.MustCompile(`^[1-9][0-9]{0,4}[dhm]$`)

// Page defaults.
const (
	DefaultQueries     = 20
	DefaultTableHealth = 50
	DefaultRuns        = 20
	// MaxClusterPoints bounds a cluster series.
	MaxClusterPoints = 300
)

var (
	// ErrQueryNotFound: the engine has no snapshot of the query.
	ErrQueryNotFound = errors.New("query not found")
	// ErrCatalogNotFound: no catalog has the id.
	ErrCatalogNotFound = errors.New("catalog not found")
	// ErrNoCapableBinding: no enabled binding of the catalog supports Iceberg
	// maintenance.
	ErrNoCapableBinding = errors.New("no enabled binding of this catalog supports Iceberg maintenance")
)

// InvalidError says why a maintenance request was refused.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// Service reads monitoring data and records maintenance runs.
type Service struct{ store Store }

// New builds the service.
func New(store Store) *Service { return &Service{store: store} }

// Engines lists every engine; the caller keeps the ones the user may see.
func (s *Service) Engines(ctx context.Context) ([]db.Engine, error) {
	return s.store.ListEngines(ctx)
}

// Cluster is an engine's latest snapshot and its series over a window.
type Cluster struct {
	// Latest is nil when the engine has no snapshot yet.
	Latest *db.ClusterSnapshot
	Series []db.ClusterSnapshot
}

// Cluster returns an engine's cluster snapshots over window: 1h (the
// default), 6h or 24h.
func (s *Service) Cluster(ctx context.Context, engineID pgtype.UUID, window string) (Cluster, error) {
	duration := time.Hour
	switch window {
	case "6h":
		duration = 6 * time.Hour
	case "24h":
		duration = 24 * time.Hour
	}
	var out Cluster
	if latest, err := s.store.GetLatestClusterSnapshotByEngine(ctx, engineID); err == nil {
		out.Latest = &latest
	}
	rows, err := s.store.ListClusterSnapshotsByEngine(ctx, db.ListClusterSnapshotsByEngineParams{
		EngineID: engineID,
		FromTime: pgtype.Timestamptz{Time: time.Now().Add(-duration), Valid: true},
		Lim:      MaxClusterPoints,
	})
	if err != nil {
		return Cluster{}, err
	}
	out.Series = rows
	return out, nil
}

// QueryFilter narrows a query list. A nil UserName lists every user's
// queries; a nil Limit is DefaultQueries.
type QueryFilter struct {
	UserName *string
	State    *string
	Q        *string
	Limit    *int
	Offset   *int
}

// Queries lists an engine's query snapshots.
func (s *Service) Queries(ctx context.Context, engineID pgtype.UUID, f QueryFilter) ([]db.TrinoQuerySnapshot, error) {
	limit, offset := page(f.Limit, f.Offset, DefaultQueries)
	return s.store.ListTrinoQuerySnapshotsByEngine(ctx, db.ListTrinoQuerySnapshotsByEngineParams{
		EngineID: engineID, UserName: f.UserName, Q: f.Q, State: f.State, Lim: limit, Off: offset,
	})
}

// Query returns one query snapshot of an engine.
func (s *Service) Query(ctx context.Context, engineID pgtype.UUID, queryID string) (db.TrinoQuerySnapshot, error) {
	q, err := s.store.GetTrinoQuerySnapshotByEngine(ctx, db.GetTrinoQuerySnapshotByEngineParams{EngineID: engineID, TrinoQueryID: queryID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.TrinoQuerySnapshot{}, ErrQueryNotFound
	}
	return q, err
}

// Catalog returns a catalog, before the caller checks access to it.
func (s *Service) Catalog(ctx context.Context, id pgtype.UUID) (db.Catalog, error) {
	c, err := s.store.GetCatalogByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Catalog{}, ErrCatalogNotFound
	}
	return c, err
}

// HealthFilter narrows table health. A nil Limit is DefaultTableHealth.
type HealthFilter struct {
	Namespace *string
	Needs     *string
	MaxScore  *int
	Limit     *int
	Offset    *int
}

// TableHealth lists a catalog's table health snapshots.
func (s *Service) TableHealth(ctx context.Context, catalogID pgtype.UUID, f HealthFilter) ([]db.TableHealthSnapshot, error) {
	limit, offset := page(f.Limit, f.Offset, DefaultTableHealth)
	params := db.ListTableHealthByCatalogParams{CatalogID: catalogID, Ns: f.Namespace, Needs: f.Needs, Lim: limit, Off: offset}
	if f.MaxScore != nil {
		score := toInt32(*f.MaxScore)
		params.MaxScore = &score
	}
	return s.store.ListTableHealthByCatalog(ctx, params)
}

// RunFilter narrows a catalog's runs; nil fields do not filter.
type RunFilter struct {
	Trigger   *string
	Operation *string
	Status    *string
	Namespace *string
	Table     *string
	Limit     *int
	Offset    *int
}

// Runs lists a catalog's maintenance runs. A nil limit is DefaultRuns.
func (s *Service) Runs(ctx context.Context, catalogID pgtype.UUID, f RunFilter) ([]db.MaintenanceRun, error) {
	lim, off := page(f.Limit, f.Offset, DefaultRuns)
	return s.store.ListMaintenanceRuns(ctx, db.ListMaintenanceRunsParams{
		CatalogID: catalogID, Trigger: f.Trigger, Operation: f.Operation, Status: f.Status,
		Ns: f.Namespace, Tbl: f.Table, Lim: lim, Off: off,
	})
}

// Maintenance is a maintenance request for one table.
type Maintenance struct {
	CatalogID  pgtype.UUID
	Namespace  string
	Table      string
	Operations []string
	// Retention is required by expire_snapshots and remove_orphan_files.
	Retention *string
	// Binding names the binding to run on; nil picks the first capable one.
	Binding   *pgtype.UUID
	StartedBy pgtype.UUID
}

// Capable reports whether an engine supports Iceberg maintenance.
type Capable func(ctx context.Context, engineID pgtype.UUID) bool

// Plan is a validated maintenance run, ready to record.
type Plan struct {
	Maintenance
	// Retention is trimmed; empty when no operation needs it.
	Retention string
	// Binding is the enabled, capable binding of the catalog the run uses:
	// its engine runs the statements under its engine catalog name.
	Binding db.CatalogBinding
}

// Prepare validates m and picks its binding.
func (s *Service) Prepare(ctx context.Context, m Maintenance, capable Capable) (Plan, error) {
	if len(m.Operations) == 0 {
		return Plan{}, &InvalidError{Msg: "operations are required"}
	}
	for _, op := range m.Operations {
		if !slices.Contains(Operations, op) {
			return Plan{}, &InvalidError{Msg: "unknown operation " + op}
		}
	}
	retention := ""
	if m.Retention != nil {
		retention = strings.TrimSpace(*m.Retention)
	}
	needsRetention := slices.Contains(m.Operations, "expire_snapshots") || slices.Contains(m.Operations, "remove_orphan_files")
	if needsRetention && !RetentionRe.MatchString(retention) {
		return Plan{}, &InvalidError{Msg: "retention must match ^[1-9][0-9]{0,4}[dhm]$ for expire_snapshots and remove_orphan_files"}
	}
	plan := Plan{Maintenance: m, Retention: retention}
	bindings, err := s.store.ListBindingsByCatalog(ctx, m.CatalogID)
	if err != nil {
		return Plan{}, err
	}
	if m.Binding != nil {
		i := slices.IndexFunc(bindings, func(b db.CatalogBinding) bool { return b.ID == *m.Binding })
		if i < 0 || !bindings[i].Enabled {
			return Plan{}, &InvalidError{Msg: "binding_id is not an enabled binding of this catalog"}
		}
		if !capable(ctx, bindings[i].EngineID) {
			return Plan{}, ErrNoCapableBinding
		}
		plan.Binding = bindings[i]
		return plan, nil
	}
	for _, b := range bindings {
		if b.Enabled && capable(ctx, b.EngineID) {
			plan.Binding = b
			return plan, nil
		}
	}
	return Plan{}, ErrNoCapableBinding
}

// Record stores a planned run under workflowID.
func (s *Service) Record(ctx context.Context, p Plan, workflowID string) (db.MaintenanceRun, error) {
	var retention *string
	if p.Retention != "" {
		retention = &p.Retention
	}
	return s.store.CreateMaintenanceRun(ctx, db.CreateMaintenanceRunParams{
		CatalogID: p.CatalogID, BindingID: p.Binding.ID, Namespace: p.Namespace, TableName: p.Table,
		Operations: p.Operations, Retention: retention, StartedBy: p.StartedBy, WorkflowID: workflowID,
	})
}

// Fail marks a run failed because its workflow did not start. It is best
// effort: the caller already has an error to return.
func (s *Service) Fail(ctx context.Context, runID pgtype.UUID, cause error) {
	code, msg := "workflow_start_failed", cause.Error()
	_, _ = s.store.UpdateMaintenanceRun(ctx, db.UpdateMaintenanceRunParams{ID: runID, Status: "failed", ErrorCode: &code, ErrorMessage: &msg})
}

func page(limit, offset *int, def int) (lim, off int32) {
	l, o := def, 0
	if limit != nil {
		l = *limit
	}
	if offset != nil {
		o = *offset
	}
	return toInt32(l), toInt32(o)
}

// toInt32 saturates instead of wrapping.
func toInt32(v int) int32 {
	switch {
	case v > math.MaxInt32:
		return math.MaxInt32
	case v < math.MinInt32:
		return math.MinInt32
	}
	return int32(v)
}
