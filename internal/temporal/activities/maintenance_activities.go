package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/monitoring"
	"github.com/galileostudio/baleia/internal/monitoring/maintenance"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/activity"
)

// snapshotMetrics is what one Iceberg table's metadata says (#500).
type snapshotMetrics struct {
	dataFiles      *int32
	totalSize      *int64
	deleteFiles    *int64
	equalityDel    *int64
	positionDel    *int64
	avgFileSize    *int64
	commitsHour    *int32
	commitsDay     *int32
	oldestAgeHours *int32
	formatVersion  *int32
	properties     map[string]string
}

// readSnapshotMetrics reads the current snapshot's summary, the snapshot log
// and the format version. A value the metadata does not carry stays nil.
func readSnapshotMetrics(meta *connections.TableMeta, now time.Time) snapshotMetrics {
	m := snapshotMetrics{properties: meta.TableProperties}
	if meta.FormatVersion > 0 {
		v := clampInt32(int64(meta.FormatVersion))
		m.formatVersion = &v
	}
	if len(meta.Snapshots) == 0 {
		return m
	}
	current := meta.Snapshots[len(meta.Snapshots)-1]
	if id, err := strconv.ParseInt(meta.Properties["current_snapshot_id"], 10, 64); err == nil {
		for _, s := range meta.Snapshots {
			if s.ID == id {
				current = s
			}
		}
	}
	summary := func(key string) *int64 {
		v, err := strconv.ParseInt(current.Summary[key], 10, 64)
		if err != nil {
			return nil
		}
		return &v
	}
	if n := summary("total-data-files"); n != nil {
		v := clampInt32(*n)
		m.dataFiles = &v
	}
	m.totalSize = summary("total-files-size")
	if m.dataFiles != nil && m.totalSize != nil && *m.dataFiles > 0 {
		v := *m.totalSize / int64(*m.dataFiles)
		m.avgFileSize = &v
	}
	// Delete counts exist from format v2 on; a v1 table has none to count.
	if meta.FormatVersion >= 2 {
		m.deleteFiles = summary("total-delete-files")
		m.equalityDel = summary("total-equality-deletes")
		m.positionDel = summary("total-position-deletes")
	}
	var hour, day int32
	for _, s := range meta.Snapshots {
		age := now.Sub(time.UnixMilli(s.TimestampMs))
		if age <= time.Hour {
			hour++
		}
		if age <= 24*time.Hour {
			day++
		}
	}
	m.commitsHour, m.commitsDay = &hour, &day
	oldest := int32(now.Sub(time.UnixMilli(meta.Snapshots[0].TimestampMs)).Hours())
	m.oldestAgeHours = &oldest
	return m
}

// defaultScore is the score of a table without maintenance parameters: the
// thresholds the health pass always used.
func defaultScore(snapshots int32, m snapshotMetrics) (score int32, needsCompaction, needsExpiry bool) {
	score = 100
	switch {
	case snapshots > 50:
		score -= 30
		needsExpiry = true
	case snapshots > 20:
		score -= 10
	}
	if m.dataFiles != nil {
		switch {
		case *m.dataFiles > 100:
			score -= 30
			needsCompaction = true
		case *m.dataFiles > 50:
			score -= 10
		}
	}
	if m.oldestAgeHours != nil && *m.oldestAgeHours > 168 {
		score -= 20
	}
	return max(score, 0), needsCompaction, needsExpiry
}

// policyScore scores a table with parameters: 100 less 25 per due operation.
func policyScore(due []string) int32 {
	return max(100-25*clampInt32(int64(len(due))), 0)
}

// clampInt32 narrows a count to the int32 columns, saturating at the bounds.
func clampInt32(v int64) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}

// engineMetrics counts the table's small files and manifests through the
// engine, only for what its parameters use; a failure leaves them unknown.
func (a *Activities) engineMetrics(ctx context.Context, eng queryengine.Engine, table []string, p maintenance.Policy) (small, manifests *int64) {
	maintainer, ok := queryengine.As[queryengine.IcebergMaintainer](eng)
	if !ok {
		return nil, nil
	}
	target := int64(0)
	if p.Optimize.Enabled && p.Optimize.Trigger.SmallFileRatio != nil && p.Optimize.Params.TargetFileSizeBytes != nil {
		target = *p.Optimize.Params.TargetFileSizeBytes
	}
	wantManifests := p.OptimizeManifests.Enabled && p.OptimizeManifests.Trigger.Manifests != nil
	if target == 0 && !wantManifests {
		return nil, nil
	}
	smallSQL, manifestSQL, err := maintainer.IcebergMetricsStatements(table, max(target, 1))
	if err != nil {
		return nil, nil
	}
	spec := queryengine.SessionSpec{Tags: queryengine.Tags{Source: "maintenance"}}
	count := func(sql string) *int64 {
		qctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		rows, err := queryengine.CollectStringsWith(qctx, eng, spec, sql, 1, queryengine.Callbacks{})
		if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
			return nil
		}
		v, err := strconv.ParseInt(rows[0][0], 10, 64)
		if err != nil {
			return nil
		}
		return &v
	}
	if target > 0 {
		small = count(smallSQL)
	}
	if wantManifests {
		manifests = count(manifestSQL)
	}
	return small, manifests
}

// capableEngine reports whether an engine supports Iceberg maintenance.
func (a *Activities) capableEngine(ctx context.Context, engineID pgtype.UUID) bool {
	eng, err := a.engines.Get(ctx, engineID)
	return err == nil && eng.Capabilities().IcebergMaintenance
}

// PlanMaintenanceRoundParams names the catalog of one round.
type PlanMaintenanceRoundParams struct {
	CatalogID string
}

// PlannedRun is a recorded automatic run, ready to start.
type PlannedRun struct {
	RunID          string
	WorkflowID     string
	TimeoutSeconds int64
}

// PlanMaintenanceRound decides, for every table of the catalog with managed
// parameters, the one operation due now, and records each run before it
// starts (#500). Runs stop at the global concurrency limit; the remaining
// tables are evaluated again next round.
func PlanMaintenanceRound(ctx context.Context, params PlanMaintenanceRoundParams) ([]PlannedRun, error) {
	return defaultActivities.planMaintenanceRound(ctx, params)
}

func (a *Activities) planMaintenanceRound(ctx context.Context, params PlanMaintenanceRoundParams) ([]PlannedRun, error) {
	catalogID := engineUUID(params.CatalogID)
	if !catalogID.Valid {
		return nil, fmt.Errorf("invalid catalog id %q", params.CatalogID)
	}
	policies, err := a.queries.ListManagedMaintenancePolicies(ctx, catalogID)
	if err != nil || len(policies) == 0 {
		return nil, err
	}
	bindings, err := a.queries.ListBindingsByCatalog(ctx, catalogID)
	if err != nil {
		return nil, err
	}
	binding, ok := monitoring.MaintenanceBinding(ctx, bindings, a.capableEngine)
	if !ok {
		return nil, nil
	}
	running, err := a.queries.CountRunningMaintenanceRuns(ctx)
	if err != nil {
		return nil, err
	}
	limit := a.cfg.MaintenanceConcurrency()
	svc := monitoring.New(a.queries)
	now := time.Now()
	var out []PlannedRun
	for _, row := range policies {
		if int(running) >= limit {
			break
		}
		key := monitoring.TableKey{CatalogID: catalogID, Namespace: row.Namespace, Table: row.TableName}
		plan, err := svc.PlanTable(ctx, key, row, maintenance.Automatic, now)
		if err != nil {
			a.logger.WarnContext(ctx, "maintenance plan failed", "catalog", params.CatalogID, "table", row.Namespace+"."+row.TableName, "err", err)
			continue
		}
		next, ok := plan.Next()
		if !ok {
			continue
		}
		p, err := monitoring.DecodePolicy(row)
		if err != nil {
			continue
		}
		settings := p.Operation(next.Operation)
		run, err := a.recordRun(ctx, key, binding, row, next, settings, string(maintenance.Automatic), pgtype.UUID{})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			continue // a run started on the table meanwhile
		}
		if err != nil {
			return out, err
		}
		running++
		out = append(out, PlannedRun{RunID: uuid.UUID(run.ID.Bytes).String(), WorkflowID: run.WorkflowID, TimeoutSeconds: settings.TimeoutSeconds})
	}
	return out, nil
}

// recordRun inserts a run with its frozen settings and reason.
func (a *Activities) recordRun(ctx context.Context, k monitoring.TableKey, b db.CatalogBinding, row db.TableMaintenancePolicy, step maintenance.Step, s maintenance.Settings, trigger string, actor pgtype.UUID) (db.MaintenanceRun, error) {
	return RecordMaintenanceRun(ctx, a.queries, k, b, row, step, s, trigger, actor)
}

// RecordMaintenanceRun stores a run of one operation, shared by the round
// and the API's manual run. The unique index on running runs refuses a
// second run on the table (23505).
func RecordMaintenanceRun(ctx context.Context, q db.Querier, k monitoring.TableKey, b db.CatalogBinding, row db.TableMaintenancePolicy, step maintenance.Step, s maintenance.Settings, trigger string, actor pgtype.UUID) (db.MaintenanceRun, error) {
	params, err := json.Marshal(s)
	if err != nil {
		return db.MaintenanceRun{}, err
	}
	reason, err := json.Marshal(step.Reason)
	if err != nil {
		return db.MaintenanceRun{}, err
	}
	revision := row.Revision
	return q.CreatePlannedMaintenanceRun(ctx, db.CreatePlannedMaintenanceRunParams{
		CatalogID: k.CatalogID, BindingID: b.ID, Namespace: k.Namespace, TableName: k.Table,
		Operations: []string{step.Operation}, StartedBy: actor, WorkflowID: MaintenanceWorkflowID(uuid.NewString()),
		Trigger: trigger, Reason: reason, Parameters: params, PolicyRevision: &revision,
	})
}

// MaintenanceWorkflowID names the workflow of one operation run.
func MaintenanceWorkflowID(runID string) string { return "maintenance-op-" + runID }

// RunMaintenanceOperationParams names the recorded run to execute.
type RunMaintenanceOperationParams struct {
	RunID string
}

// conflictBackoff is how long a run waits before each retry of a commit
// conflict.
var conflictBackoff = []time.Duration{30 * time.Second, 2 * time.Minute, 5 * time.Minute}

// RunMaintenanceOperation executes one recorded run: its single operation
// with the frozen settings, on its binding's engine. A commit conflict is
// retried with backoff or reported as gave_up, as the settings say; every
// outcome is written to the run.
func RunMaintenanceOperation(ctx context.Context, params RunMaintenanceOperationParams) error {
	return defaultActivities.runMaintenanceOperation(ctx, params)
}

func (a *Activities) runMaintenanceOperation(ctx context.Context, params RunMaintenanceOperationParams) error {
	id := engineUUID(params.RunID)
	run, err := a.queries.GetMaintenanceRun(ctx, id)
	if err != nil {
		return fmt.Errorf("load maintenance run %s: %w", params.RunID, err)
	}
	if run.Status != "running" {
		return nil // finished or closed meanwhile
	}
	fail := func(code string, cause error) error {
		finishMaintenanceRun(a.queries, ctx, params.RunID, "failed", code, cause.Error())
		return cause
	}
	if len(run.Operations) != 1 || !run.BindingID.Valid {
		return fail("maintenance_failed", errors.New("run has no single operation or no binding"))
	}
	settings, err := maintenance.DecodeSettings(run.Parameters)
	if err != nil {
		return fail("maintenance_failed", err)
	}
	binding, err := a.queries.GetBinding(ctx, run.BindingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fail("maintenance_failed", errors.New("the run's binding no longer exists"))
	}
	if err != nil {
		return fail("maintenance_failed", err)
	}
	eng, err := a.engines.Get(ctx, binding.EngineID)
	if err != nil {
		return fail("engine_unavailable", err)
	}
	maintainer, ok := queryengine.As[queryengine.IcebergMaintainer](eng)
	if !ok {
		return fail("not_supported", fmt.Errorf("engine %s has no Iceberg maintenance", eng.Kind()))
	}
	sql, err := maintainer.IcebergStatement([]string{binding.EngineCatalogName, run.Namespace, run.TableName}, icebergOperation(run.Operations[0], settings))
	if err != nil {
		return fail("maintenance_failed", err)
	}

	var lastBeat time.Time
	cb := queryengine.Callbacks{OnProgress: func(queryengine.Progress) {
		if now := time.Now(); now.Sub(lastBeat) >= heartbeatInterval {
			lastBeat = now
			activity.RecordHeartbeat(ctx)
		}
	}}
	spec := queryengine.SessionSpec{Tags: queryengine.Tags{Source: "maintenance", ExecutionID: params.RunID}}
	for attempt := 1; ; attempt++ {
		if attempt > 1 {
			_ = a.queries.SetMaintenanceRunAttempts(ctx, db.SetMaintenanceRunAttemptsParams{ID: id, Attempts: int32(attempt)})
		}
		_, err = queryengine.CollectStringsWith(ctx, eng, spec, sql, maintenanceMaxResultRows, cb)
		if err == nil {
			recordMaintenanceSnapshot(a.queries, ctx, binding.EngineID, params.RunID, sql, "finished", 0, 0, "")
			finishMaintenanceRun(a.queries, ctx, params.RunID, "finished", "", "")
			return nil
		}
		recordMaintenanceSnapshot(a.queries, ctx, binding.EngineID, params.RunID, sql, "failed", 0, 0, err.Error())
		if ctx.Err() != nil {
			return fail("cancelled", ctx.Err())
		}
		if !isCommitConflict(err) {
			return fail("maintenance_failed", err)
		}
		if settings.OnConflict != maintenance.RetryWithBackoff || attempt > len(conflictBackoff) {
			finishMaintenanceRun(a.queries, ctx, params.RunID, "gave_up", "commit_conflict", err.Error())
			return nil
		}
		if err := sleepBeating(ctx, conflictBackoff[attempt-1]); err != nil {
			return fail("cancelled", err)
		}
	}
}

// sleepBeating waits d, heartbeating so the activity is not declared dead.
func sleepBeating(ctx context.Context, d time.Duration) error {
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	tick := time.NewTicker(heartbeatInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return nil
		case <-tick.C:
			activity.RecordHeartbeat(ctx)
		}
	}
}

// isCommitConflict reports an Iceberg commit that lost a race with another
// writer.
func isCommitConflict(err error) bool {
	var qe *queryengine.Error
	if errors.As(err, &qe) && (qe.Native == "ICEBERG_COMMIT_ERROR" || qe.Code == queryengine.CodeConflict) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "ICEBERG_COMMIT_ERROR") || strings.Contains(msg, "CommitFailedException")
}

// icebergOperation maps a table's settings to the engine's procedure.
func icebergOperation(op string, s maintenance.Settings) queryengine.IcebergOperation {
	out := queryengine.IcebergOperation{Name: op, SortedBy: s.Params.SortedBy, CleanExpiredMetadata: s.Params.CleanExpiredMetadata}
	if v := s.Params.TargetFileSizeBytes; v != nil {
		out.FileSizeThreshold = *v
	}
	if v := s.Params.PartitionFilter; v != nil {
		out.PartitionFilter = *v
	}
	if v := s.Params.SkipFilesNewerThanSeconds; v != nil {
		out.SkipFilesNewerThan = time.Duration(*v) * time.Second
	}
	if v := s.Params.RetainLast; v != nil {
		out.RetainLast = *v
	}
	switch op {
	case maintenance.OpExpireSnapshots:
		if v := s.Params.RetentionSeconds; v != nil {
			out.Retention = time.Duration(*v) * time.Second
		}
	case maintenance.OpRemoveOrphanFiles:
		if v := s.Params.OlderThanSeconds; v != nil {
			out.Retention = time.Duration(*v) * time.Second
		}
	}
	return out
}

// FailMaintenanceRunParams closes a run whose workflow did not start.
type FailMaintenanceRunParams struct {
	RunID   string
	Message string
}

// FailMaintenanceRun marks a planned run failed when its workflow did not
// start, so it does not block the table as "running".
func FailMaintenanceRun(ctx context.Context, params FailMaintenanceRunParams) error {
	finishMaintenanceRun(defaultActivities.queries, ctx, params.RunID, "failed", "workflow_start_failed", params.Message)
	return nil
}
