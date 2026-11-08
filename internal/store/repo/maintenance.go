package repo

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SetMaintenanceRunAttempts records how many times a run was attempted.
func (r *Repo) SetMaintenanceRunAttempts(ctx context.Context, arg db.SetMaintenanceRunAttemptsParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.MaintenanceRun{}).
		Where("id = ?", UUIDString(arg.ID)).
		Update("attempts", arg.Attempts).Error
}

// CountRunningMaintenanceRuns counts the runs in progress across catalogs.
func (r *Repo) CountRunningMaintenanceRuns(ctx context.Context) (int32, error) {
	var n int64
	if err := r.Users.db.WithContext(ctx).Model(&model.MaintenanceRun{}).
		Where("status = ?", "running").
		Count(&n).Error; err != nil {
		return 0, err
	}
	return int32(n), nil //nolint:gosec // bounded by the configured concurrency limit
}

// LockMaintenanceCapacity serializes the maintenance rounds' capacity check and
// run creation until the transaction ends, replacing the sqlc advisory lock
// with the model API (ADR 0020): every engine row is locked in id order, so
// concurrent catalog rounds take turns instead of overfilling the limit.
func (r *Repo) LockMaintenanceCapacity(ctx context.Context) error {
	var engines []model.Engine
	return r.Users.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Order("id").
		Find(&engines).Error
}

// TxOps runs fn in one transaction over a Repo bound to it. Callers on the
// monitoring/ops path use it so the capacity check and the runs it creates
// commit together (#773).
func (r *Repo) TxOps(ctx context.Context, fn func(*Repo) error) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}

// ── table maintenance policies ────────────────────────────────────────────────

// GetTableMaintenancePolicy returns one table's parameters.
func (r *Repo) GetTableMaintenancePolicy(ctx context.Context, arg db.GetTableMaintenancePolicyParams) (db.TableMaintenancePolicy, error) {
	var row model.TableMaintenancePolicy
	err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND namespace = ? AND table_name = ?",
			UUIDString(arg.CatalogID), arg.Namespace, arg.TableName).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.TableMaintenancePolicy{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.TableMaintenancePolicy{}, err
	}
	return toDBTableMaintenancePolicy(row), nil
}

// ListTableMaintenancePolicies lists every table of a catalog with parameters.
func (r *Repo) ListTableMaintenancePolicies(ctx context.Context, catalogID pgtype.UUID) ([]db.TableMaintenancePolicy, error) {
	return r.listTableMaintenancePolicies(ctx, catalogID, "")
}

// ListManagedMaintenancePolicies lists the managed tables of a catalog.
func (r *Repo) ListManagedMaintenancePolicies(ctx context.Context, catalogID pgtype.UUID) ([]db.TableMaintenancePolicy, error) {
	return r.listTableMaintenancePolicies(ctx, catalogID, "managed")
}

func (r *Repo) listTableMaintenancePolicies(ctx context.Context, catalogID pgtype.UUID, mode string) ([]db.TableMaintenancePolicy, error) {
	q := r.Users.db.WithContext(ctx).Where("catalog_id = ?", UUIDString(catalogID))
	if mode != "" {
		q = q.Where("mode = ?", mode)
	}
	var rows []model.TableMaintenancePolicy
	if err := q.Order("namespace, table_name").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.TableMaintenancePolicy, len(rows))
	for i, row := range rows {
		out[i] = toDBTableMaintenancePolicy(row)
	}
	return out, nil
}

// CreateTableMaintenancePolicy inserts the table's parameters, or returns
// pgx.ErrNoRows when it already has some (the sqlc ON CONFLICT DO NOTHING).
func (r *Repo) CreateTableMaintenancePolicy(ctx context.Context, arg db.CreateTableMaintenancePolicyParams) (db.TableMaintenancePolicy, error) {
	now := time.Now().UTC()
	row := model.TableMaintenancePolicy{
		ID:                          model.UUID(NewUUID()),
		CatalogID:                   model.UUID(UUIDString(arg.CatalogID)),
		Namespace:                   model.Text(arg.Namespace),
		TableNameValue:              model.Text(arg.TableName),
		Mode:                        arg.Mode,
		WriterCommitIntervalSeconds: arg.WriterCommitIntervalSeconds,
		Optimize:                    jsonOrDefault(arg.Optimize),
		OptimizeManifests:           jsonOrDefault(arg.OptimizeManifests),
		ExpireSnapshots:             jsonOrDefault(arg.ExpireSnapshots),
		RemoveOrphanFiles:           jsonOrDefault(arg.RemoveOrphanFiles),
		Revision:                    1,
		CreatedBy:                   modelUUIDPtr(arg.Actor),
		UpdatedBy:                   modelUUIDPtr(arg.Actor),
		CreatedAt:                   now,
		UpdatedAt:                   now,
	}
	values := map[string]any{
		"id":                             row.ID,
		"catalog_id":                     row.CatalogID,
		"namespace":                      row.Namespace,
		"table_name":                     row.TableNameValue,
		"mode":                           row.Mode,
		"writer_commit_interval_seconds": row.WriterCommitIntervalSeconds,
		"optimize":                       row.Optimize,
		"optimize_manifests":             row.OptimizeManifests,
		"expire_snapshots":               row.ExpireSnapshots,
		"remove_orphan_files":            row.RemoveOrphanFiles,
		"revision":                       row.Revision,
		"created_by":                     row.CreatedBy,
		"updated_by":                     row.UpdatedBy,
		"created_at":                     row.CreatedAt,
		"updated_at":                     row.UpdatedAt,
	}
	if err := r.Users.db.WithContext(ctx).Model(&model.TableMaintenancePolicy{}).Create(values).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return db.TableMaintenancePolicy{}, pgx.ErrNoRows
		}
		return db.TableMaintenancePolicy{}, err
	}
	return toDBTableMaintenancePolicy(row), nil
}

// UpdateTableMaintenancePolicy replaces the parameters at the caller's
// revision and bumps it. No row means the revision moved on or the table has
// no parameters (pgx.ErrNoRows).
func (r *Repo) UpdateTableMaintenancePolicy(ctx context.Context, arg db.UpdateTableMaintenancePolicyParams) (db.TableMaintenancePolicy, error) {
	res := r.Users.db.WithContext(ctx).Model(&model.TableMaintenancePolicy{}).
		Where("catalog_id = ? AND namespace = ? AND table_name = ? AND revision = ?",
			UUIDString(arg.CatalogID), arg.Namespace, arg.TableName, arg.Revision).
		Updates(map[string]any{
			"mode":                           arg.Mode,
			"writer_commit_interval_seconds": arg.WriterCommitIntervalSeconds,
			"optimize":                       jsonOrDefault(arg.Optimize),
			"optimize_manifests":             jsonOrDefault(arg.OptimizeManifests),
			"expire_snapshots":               jsonOrDefault(arg.ExpireSnapshots),
			"remove_orphan_files":            jsonOrDefault(arg.RemoveOrphanFiles),
			"revision":                       arg.Revision + 1,
			"updated_by":                     modelUUIDPtr(arg.Actor),
			"updated_at":                     time.Now().UTC(),
		})
	if res.Error != nil {
		return db.TableMaintenancePolicy{}, res.Error
	}
	if res.RowsAffected == 0 {
		return db.TableMaintenancePolicy{}, pgx.ErrNoRows
	}
	return r.GetTableMaintenancePolicy(ctx, db.GetTableMaintenancePolicyParams{
		CatalogID: arg.CatalogID, Namespace: arg.Namespace, TableName: arg.TableName,
	})
}

// DeleteTableMaintenancePolicy removes the table's parameters and returns them.
func (r *Repo) DeleteTableMaintenancePolicy(ctx context.Context, arg db.DeleteTableMaintenancePolicyParams) (db.TableMaintenancePolicy, error) {
	var out db.TableMaintenancePolicy
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.TableMaintenancePolicy
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("catalog_id = ? AND namespace = ? AND table_name = ?",
				UUIDString(arg.CatalogID), arg.Namespace, arg.TableName).
			First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pgx.ErrNoRows
		}
		if err != nil {
			return err
		}
		if err := tx.Where("id = ?", string(row.ID)).Delete(&model.TableMaintenancePolicy{}).Error; err != nil {
			return err
		}
		out = toDBTableMaintenancePolicy(row)
		return nil
	})
	return out, err
}

// ── runs and planning reads ───────────────────────────────────────────────────

// LastSuccessfulMaintenanceByOperation is the latest finished run of each
// operation on one table.
func (r *Repo) LastSuccessfulMaintenanceByOperation(ctx context.Context, arg db.LastSuccessfulMaintenanceByOperationParams) ([]db.LastSuccessfulMaintenanceByOperationRow, error) {
	var rows []model.MaintenanceRun
	if err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND namespace = ? AND table_name = ? AND status = ? AND finished_at IS NOT NULL",
			UUIDString(arg.CatalogID), arg.Namespace, arg.TableName, "finished").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	last := map[string]time.Time{}
	for _, row := range rows {
		if row.FinishedAt == nil {
			continue
		}
		for _, op := range row.Operations {
			if at, ok := last[op]; !ok || row.FinishedAt.After(at) {
				last[op] = *row.FinishedAt
			}
		}
	}
	out := make([]db.LastSuccessfulMaintenanceByOperationRow, 0, len(last))
	for op, at := range last {
		out = append(out, db.LastSuccessfulMaintenanceByOperationRow{Operation: op, FinishedAt: PGTime(at)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Operation < out[j].Operation })
	return out, nil
}

// CreatePlannedMaintenanceRun records a run with its trigger, reason and frozen
// parameters. A dry run is finished at once; any other run starts running.
func (r *Repo) CreatePlannedMaintenanceRun(ctx context.Context, arg db.CreatePlannedMaintenanceRunParams) (db.MaintenanceRun, error) {
	now := time.Now().UTC()
	status := "running"
	var finishedAt *time.Time
	if arg.Trigger == "dry_run" {
		status = "dry_run"
		finishedAt = &now
	}
	row := model.MaintenanceRun{
		ID:             model.UUID(NewUUID()),
		CatalogID:      model.UUID(UUIDString(arg.CatalogID)),
		BindingID:      modelUUIDPtr(arg.BindingID),
		Namespace:      model.Text(arg.Namespace),
		TableNameValue: model.Text(arg.TableName),
		Operations:     model.StringArray(arg.Operations),
		Retention:      arg.Retention,
		Status:         status,
		StartedBy:      modelUUIDPtr(arg.StartedBy),
		StartedAt:      now,
		FinishedAt:     finishedAt,
		WorkflowID:     arg.WorkflowID,
		Trigger:        model.Text(arg.Trigger),
		Reason:         jsonOrDefault(arg.Reason),
		Parameters:     jsonOrDefault(arg.Parameters),
		Attempts:       1,
		PolicyRevision: arg.PolicyRevision,
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.MaintenanceRun{}, err
	}
	return toDBMaintenanceRun(row), nil
}

// ListCatalogTableHealth lists every health row of a catalog.
func (r *Repo) ListCatalogTableHealth(ctx context.Context, catalogID pgtype.UUID) ([]db.TableHealthSnapshot, error) {
	var rows []model.TableHealthSnapshot
	if err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ?", UUIDString(catalogID)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.TableHealthSnapshot, len(rows))
	for i, row := range rows {
		out[i] = toDBTableHealthSnapshot(row)
	}
	return out, nil
}

// ListCatalogLastSuccessfulMaintenance is the latest finished run of each
// operation of every table of a catalog.
func (r *Repo) ListCatalogLastSuccessfulMaintenance(ctx context.Context, catalogID pgtype.UUID) ([]db.ListCatalogLastSuccessfulMaintenanceRow, error) {
	runs, err := r.catalogFinishedRuns(ctx, catalogID)
	if err != nil {
		return nil, err
	}
	type key struct{ namespace, table, operation string }
	last := map[key]time.Time{}
	for _, run := range runs {
		if run.FinishedAt == nil {
			continue
		}
		for _, op := range run.Operations {
			k := key{string(run.Namespace), string(run.TableNameValue), op}
			if at, ok := last[k]; !ok || run.FinishedAt.After(at) {
				last[k] = *run.FinishedAt
			}
		}
	}
	out := make([]db.ListCatalogLastSuccessfulMaintenanceRow, 0, len(last))
	for k, at := range last {
		out = append(out, db.ListCatalogLastSuccessfulMaintenanceRow{
			Namespace: k.namespace, TableName: k.table, Operation: k.operation, FinishedAt: PGTime(at),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		if out[i].TableName != out[j].TableName {
			return out[i].TableName < out[j].TableName
		}
		return out[i].Operation < out[j].Operation
	})
	return out, nil
}

// ListCatalogRunningMaintenance lists the tables of a catalog with a running run.
func (r *Repo) ListCatalogRunningMaintenance(ctx context.Context, catalogID pgtype.UUID) ([]db.ListCatalogRunningMaintenanceRow, error) {
	var rows []model.MaintenanceRun
	if err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND status = ?", UUIDString(catalogID), "running").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ListCatalogRunningMaintenanceRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListCatalogRunningMaintenanceRow{Namespace: string(row.Namespace), TableName: string(row.TableNameValue)}
	}
	return out, nil
}

// ListCatalogLatestMaintenanceRuns is each table's most recent run of a catalog.
func (r *Repo) ListCatalogLatestMaintenanceRuns(ctx context.Context, catalogID pgtype.UUID) ([]db.MaintenanceRun, error) {
	var rows []model.MaintenanceRun
	if err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ?", UUIDString(catalogID)).
		Order("started_at DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	type key struct{ namespace, table string }
	seen := map[key]bool{}
	out := make([]db.MaintenanceRun, 0)
	for _, row := range rows {
		k := key{string(row.Namespace), string(row.TableNameValue)}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, toDBMaintenanceRun(row))
	}
	return out, nil
}

// GetAppliedCatalogRetention is the properties Baleia wrote for the catalog on
// its engine's cluster, and who confirmed them.
func (r *Repo) GetAppliedCatalogRetention(ctx context.Context, arg db.GetAppliedCatalogRetentionParams) (db.GetAppliedCatalogRetentionRow, error) {
	var cluster model.TrinoCluster
	err := r.Users.db.WithContext(ctx).
		Where("engine_id = ?", UUIDString(arg.EngineID)).
		First(&cluster).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetAppliedCatalogRetentionRow{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.GetAppliedCatalogRetentionRow{}, err
	}
	var row model.TrinoCatalogRegistry
	err = r.Users.db.WithContext(ctx).
		Where("cluster_id = ? AND catalog_name = ?", string(cluster.ID), model.Text(arg.CatalogName)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetAppliedCatalogRetentionRow{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.GetAppliedCatalogRetentionRow{}, err
	}
	return db.GetAppliedCatalogRetentionRow{
		Properties: []byte(row.Properties),
		UpdatedBy:  string(row.UpdatedBy),
	}, nil
}

func toDBTableMaintenancePolicy(row model.TableMaintenancePolicy) db.TableMaintenancePolicy {
	return db.TableMaintenancePolicy{
		ID:                          PGUUID(string(row.ID)),
		CatalogID:                   PGUUID(string(row.CatalogID)),
		Namespace:                   string(row.Namespace),
		TableName:                   string(row.TableNameValue),
		Mode:                        row.Mode,
		WriterCommitIntervalSeconds: row.WriterCommitIntervalSeconds,
		Optimize:                    []byte(row.Optimize),
		OptimizeManifests:           []byte(row.OptimizeManifests),
		ExpireSnapshots:             []byte(row.ExpireSnapshots),
		RemoveOrphanFiles:           []byte(row.RemoveOrphanFiles),
		Revision:                    row.Revision,
		CreatedBy:                   uuidPtr(row.CreatedBy),
		UpdatedBy:                   uuidPtr(row.UpdatedBy),
		CreatedAt:                   PGTime(row.CreatedAt),
		UpdatedAt:                   PGTime(row.UpdatedAt),
	}
}
