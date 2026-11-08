package repo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/monitoring"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The monitoring and status services read through these narrow interfaces;
// *Repo implements each of them (ADR 0020, #773).
var (
	_ monitoring.Store = (*Repo)(nil)
)

// ── engine snapshots ──────────────────────────────────────────────────────────

// InsertClusterSnapshot records one cluster sample.
func (r *Repo) InsertClusterSnapshot(ctx context.Context, arg db.InsertClusterSnapshotParams) (db.ClusterSnapshot, error) {
	row := model.ClusterSnapshot{
		ID:               model.UUID(NewUUID()),
		CapturedAt:       time.Now().UTC(),
		ActiveWorkers:    arg.ActiveWorkers,
		RunningQueries:   arg.RunningQueries,
		QueuedQueries:    arg.QueuedQueries,
		BlockedQueries:   arg.BlockedQueries,
		TotalMemoryBytes: arg.TotalMemoryBytes,
		FreeMemoryBytes:  arg.FreeMemoryBytes,
		RawPayload:       modelJSONPtr(arg.RawPayload),
		EngineID:         model.UUID(UUIDString(arg.EngineID)),
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.ClusterSnapshot{}, err
	}
	return toDBClusterSnapshot(row), nil
}

// GetLatestClusterSnapshotByEngine is the newest sample of one engine.
func (r *Repo) GetLatestClusterSnapshotByEngine(ctx context.Context, engineID pgtype.UUID) (db.ClusterSnapshot, error) {
	var row model.ClusterSnapshot
	err := r.Users.db.WithContext(ctx).
		Where("engine_id = ?", UUIDString(engineID)).
		Order("captured_at DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.ClusterSnapshot{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.ClusterSnapshot{}, err
	}
	return toDBClusterSnapshot(row), nil
}

// ListClusterSnapshotsByEngine returns an engine's samples from a time on, oldest first.
func (r *Repo) ListClusterSnapshotsByEngine(ctx context.Context, arg db.ListClusterSnapshotsByEngineParams) ([]db.ClusterSnapshot, error) {
	q := r.Users.db.WithContext(ctx).Where("engine_id = ?", UUIDString(arg.EngineID))
	if arg.FromTime.Valid {
		q = q.Where("captured_at >= ?", arg.FromTime.Time)
	}
	var rows []model.ClusterSnapshot
	if err := q.Order("captured_at ASC").Limit(int(arg.Lim)).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ClusterSnapshot, len(rows))
	for i, row := range rows {
		out[i] = toDBClusterSnapshot(row)
	}
	return out, nil
}

// UpsertTrinoQuerySnapshot stores the snapshot one writer has of an engine
// query, merging two writers' figures as the sqlc ON CONFLICT did: a late poll
// of a terminal query does not roll the state back and only fills a missing
// figure; otherwise the newer non-null figure wins.
func (r *Repo) UpsertTrinoQuerySnapshot(ctx context.Context, arg db.UpsertTrinoQuerySnapshotParams) (db.TrinoQuerySnapshot, error) {
	var out db.TrinoQuerySnapshot
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		createdAt := now
		if arg.CreatedAt.Valid {
			createdAt = arg.CreatedAt.Time
		}
		row := model.TrinoQuerySnapshot{
			ID:                 model.UUID(NewUUID()),
			TrinoQueryID:       model.Text(arg.TrinoQueryID),
			ExecutionID:        modelUUIDPtr(arg.ExecutionID),
			Origin:             model.Text(arg.Origin),
			User:               model.Text(arg.User),
			State:              model.Text(arg.State),
			SQL:                arg.Sql,
			Catalog:            arg.Catalog,
			Schema:             arg.Schema,
			CreatedAt:          createdAt,
			ElapsedMs:          arg.ElapsedMs,
			CPUMs:              arg.CpuMs,
			PhysicalInputBytes: arg.PhysicalInputBytes,
			OutputRows:         arg.OutputRows,
			ErrorMessage:       arg.ErrorMessage,
			RawTrinoPayload:    modelJSONPtr(arg.RawTrinoPayload),
			FirstSeenAt:        now,
			LastSeenAt:         now,
			EngineID:           model.UUID(UUIDString(arg.EngineID)),
		}
		// A concurrent first write by the other writer must merge, not fail
		// the poller: DO NOTHING keeps the transaction usable, and the row is
		// re-read under a lock for the merge below.
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			out = toDBTrinoQuerySnapshot(row)
			return nil
		}
		var current model.TrinoQuerySnapshot
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("trino_query_id = ?", arg.TrinoQueryID).
			First(&current).Error; err != nil {
			return err
		}
		merged, err := mergeTrinoQuerySnapshot(tx, current, arg, now)
		if err != nil {
			return err
		}
		out = toDBTrinoQuerySnapshot(merged)
		return nil
	})
	return out, err
}

// terminalSnapshotState reports a state a late running poll must not undo.
func terminalSnapshotState(state string) bool {
	return state == "finished" || state == "failed" || state == "cancelled"
}

// mergeTrinoQuerySnapshot applies the ON CONFLICT merge rules to the row a
// concurrent writer created and returns the stored row.
func mergeTrinoQuerySnapshot(tx *gorm.DB, current model.TrinoQuerySnapshot, arg db.UpsertTrinoQuerySnapshotParams, now time.Time) (model.TrinoQuerySnapshot, error) {
	stale := terminalSnapshotState(string(current.State)) && arg.State == "running"
	state := arg.State
	elapsed := firstNonNil(arg.ElapsedMs, current.ElapsedMs)
	cpu := firstNonNil(arg.CpuMs, current.CPUMs)
	physical := firstNonNil(arg.PhysicalInputBytes, current.PhysicalInputBytes)
	outputRows := firstNonNil(arg.OutputRows, current.OutputRows)
	errorMessage := firstNonNil(arg.ErrorMessage, current.ErrorMessage)
	if stale {
		state = string(current.State)
		elapsed = firstNonNil(current.ElapsedMs, arg.ElapsedMs)
		cpu = firstNonNil(current.CPUMs, arg.CpuMs)
		physical = firstNonNil(current.PhysicalInputBytes, arg.PhysicalInputBytes)
		// The final figures belong to the terminal row, not the late poll.
		outputRows = current.OutputRows
		errorMessage = current.ErrorMessage
	}
	updates := map[string]any{
		"state":                state,
		"elapsed_ms":           elapsed,
		"cpu_ms":               cpu,
		"physical_input_bytes": physical,
		"output_rows":          outputRows,
		"error_message":        errorMessage,
		"last_seen_at":         now,
	}
	if err := tx.Model(&model.TrinoQuerySnapshot{}).
		Where("id = ?", string(current.ID)).Updates(updates).Error; err != nil {
		return model.TrinoQuerySnapshot{}, err
	}
	if err := tx.Where("id = ?", string(current.ID)).First(&current).Error; err != nil {
		return model.TrinoQuerySnapshot{}, err
	}
	return current, nil
}

// GetTrinoQuerySnapshotByEngine returns one query snapshot of an engine.
func (r *Repo) GetTrinoQuerySnapshotByEngine(ctx context.Context, arg db.GetTrinoQuerySnapshotByEngineParams) (db.TrinoQuerySnapshot, error) {
	var row model.TrinoQuerySnapshot
	err := r.Users.db.WithContext(ctx).
		Where("engine_id = ? AND trino_query_id = ?", UUIDString(arg.EngineID), arg.TrinoQueryID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.TrinoQuerySnapshot{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.TrinoQuerySnapshot{}, err
	}
	return toDBTrinoQuerySnapshot(row), nil
}

// ListTrinoQuerySnapshotsByEngine lists an engine's queries, newest first.
func (r *Repo) ListTrinoQuerySnapshotsByEngine(ctx context.Context, arg db.ListTrinoQuerySnapshotsByEngineParams) ([]db.TrinoQuerySnapshot, error) {
	q := r.Users.db.WithContext(ctx).Where("engine_id = ?", UUIDString(arg.EngineID))
	if arg.State != nil {
		q = q.Where("state = ?", *arg.State)
	}
	if arg.UserName != nil {
		q = q.Where(map[string]any{"user": *arg.UserName})
	}
	if arg.Q != nil && *arg.Q != "" {
		// Both operands are lower-cased by the database and the caller: no
		// ILIKE and no Go-only case folding for the stored column.
		pattern := "%" + strings.ToLower(*arg.Q) + "%"
		q = q.Where("LOWER(sql) LIKE ?", pattern)
	}
	var rows []model.TrinoQuerySnapshot
	if err := q.Order("created_at DESC").
		Limit(int(arg.Lim)).Offset(int(arg.Off)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.TrinoQuerySnapshot, len(rows))
	for i, row := range rows {
		out[i] = toDBTrinoQuerySnapshot(row)
	}
	return out, nil
}

// SetTrinoQueryProfile replaces the stored profile payload of one query.
func (r *Repo) SetTrinoQueryProfile(ctx context.Context, arg db.SetTrinoQueryProfileParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.TrinoQuerySnapshot{}).
		Where("trino_query_id = ?", arg.TrinoQueryID).
		Update("raw_trino_payload", modelJSONPtr(arg.RawTrinoPayload)).Error
}

// ListQueryClasses returns the queue class of the executions that have one,
// matched by execution id or, for engine-direct snapshots, by query id (#828).
func (r *Repo) ListQueryClasses(ctx context.Context, arg db.ListQueryClassesParams) ([]db.ListQueryClassesRow, error) {
	if len(arg.ExecutionIds) == 0 && len(arg.QueryIds) == 0 {
		return nil, nil
	}
	executionIDs := make([]string, len(arg.ExecutionIds))
	for i, id := range arg.ExecutionIds {
		executionIDs[i] = UUIDString(id)
	}
	q := r.Users.db.WithContext(ctx).Model(&model.QueryExecution{}).
		Select("id", "engine_query_id", "queue_class").
		Where("queue_class IS NOT NULL")
	switch {
	case len(executionIDs) > 0 && len(arg.QueryIds) > 0:
		q = q.Where("id IN ? OR engine_query_id IN ?", executionIDs, arg.QueryIds)
	case len(executionIDs) > 0:
		q = q.Where("id IN ?", executionIDs)
	default:
		q = q.Where("engine_query_id IN ?", arg.QueryIds)
	}
	var rows []model.QueryExecution
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ListQueryClassesRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListQueryClassesRow{
			ID:            PGUUID(string(row.ID)),
			EngineQueryID: row.EngineQueryID,
			QueueClass:    row.QueueClass,
		}
	}
	return out, nil
}

// ── table health ──────────────────────────────────────────────────────────────

// UpsertTableHealthSnapshot stores the latest health pass of one table,
// keeping the last maintenance time when this pass does not know it.
func (r *Repo) UpsertTableHealthSnapshot(ctx context.Context, arg db.UpsertTableHealthSnapshotParams) (db.TableHealthSnapshot, error) {
	var out db.TableHealthSnapshot
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		due := arg.DueOperations
		if due == nil {
			due = []string{}
		}
		var insertedLast *time.Time
		if arg.LastMaintenanceAt.Valid {
			t := arg.LastMaintenanceAt.Time
			insertedLast = &t
		}
		values := map[string]any{
			"id":                        model.UUID(NewUUID()),
			"catalog_connection_id":     model.UUID(UUIDString(arg.CatalogConnectionID)),
			"catalog":                   model.Text(arg.Catalog),
			"namespace":                 model.Text(arg.Namespace),
			"table_name":                model.Text(arg.TableName),
			"captured_at":               now,
			"snapshot_count":            arg.SnapshotCount,
			"file_count":                arg.FileCount,
			"total_size_bytes":          arg.TotalSizeBytes,
			"oldest_snapshot_age_hours": arg.OldestSnapshotAgeHours,
			"last_maintenance_at":       insertedLast,
			"health_score":              arg.HealthScore,
			"needs_compaction":          arg.NeedsCompaction,
			"needs_snapshot_expiry":     arg.NeedsSnapshotExpiry,
			"catalog_id":                model.UUID(UUIDString(arg.CatalogID)),
			"delete_file_count":         arg.DeleteFileCount,
			"equality_delete_count":     arg.EqualityDeleteCount,
			"position_delete_count":     arg.PositionDeleteCount,
			"small_file_count":          arg.SmallFileCount,
			"commits_last_hour":         arg.CommitsLastHour,
			"commits_last_day":          arg.CommitsLastDay,
			"avg_file_size_bytes":       arg.AvgFileSizeBytes,
			"manifest_count":            arg.ManifestCount,
			"format_version":            arg.FormatVersion,
			"table_properties":          jsonOrDefault(arg.TableProperties),
			"due_operations":            model.StringArray(due),
			"policy_revision":           arg.PolicyRevision,
		}
		// Concurrent health passes for the same table must merge: DO NOTHING
		// keeps the transaction usable, then the stored row is re-read under a
		// lock and updated with the merge rules.
		res := tx.Model(&model.TableHealthSnapshot{}).
			Clauses(clause.OnConflict{DoNothing: true}).Create(values)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			var row model.TableHealthSnapshot
			if err := tx.Where("id = ?", values["id"]).First(&row).Error; err != nil {
				return err
			}
			out = toDBTableHealthSnapshot(row)
			return nil
		}
		var current model.TableHealthSnapshot
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("catalog_id = ? AND namespace = ? AND table_name = ?",
				UUIDString(arg.CatalogID), arg.Namespace, arg.TableName).
			First(&current).Error; err != nil {
			return err
		}
		lastMaintenance := current.LastMaintenanceAt
		if arg.LastMaintenanceAt.Valid {
			t := arg.LastMaintenanceAt.Time
			lastMaintenance = &t
		}
		updates := map[string]any{
			"captured_at":               now,
			"catalog_connection_id":     model.UUID(UUIDString(arg.CatalogConnectionID)),
			"catalog":                   model.Text(arg.Catalog),
			"snapshot_count":            arg.SnapshotCount,
			"file_count":                arg.FileCount,
			"total_size_bytes":          arg.TotalSizeBytes,
			"oldest_snapshot_age_hours": arg.OldestSnapshotAgeHours,
			"last_maintenance_at":       lastMaintenance,
			"health_score":              arg.HealthScore,
			"needs_compaction":          arg.NeedsCompaction,
			"needs_snapshot_expiry":     arg.NeedsSnapshotExpiry,
			"delete_file_count":         arg.DeleteFileCount,
			"equality_delete_count":     arg.EqualityDeleteCount,
			"position_delete_count":     arg.PositionDeleteCount,
			"small_file_count":          arg.SmallFileCount,
			"commits_last_hour":         arg.CommitsLastHour,
			"commits_last_day":          arg.CommitsLastDay,
			"avg_file_size_bytes":       arg.AvgFileSizeBytes,
			"manifest_count":            arg.ManifestCount,
			"format_version":            arg.FormatVersion,
			"table_properties":          jsonOrDefault(arg.TableProperties),
			"due_operations":            model.StringArray(due),
			"policy_revision":           arg.PolicyRevision,
		}
		if err := tx.Model(&model.TableHealthSnapshot{}).
			Where("id = ?", string(current.ID)).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", string(current.ID)).First(&current).Error; err != nil {
			return err
		}
		out = toDBTableHealthSnapshot(current)
		return nil
	})
	return out, err
}

// GetTableHealthByTable returns the health pass of one table.
func (r *Repo) GetTableHealthByTable(ctx context.Context, arg db.GetTableHealthByTableParams) (db.TableHealthSnapshot, error) {
	var row model.TableHealthSnapshot
	err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND namespace = ? AND table_name = ?",
			UUIDString(arg.CatalogID), arg.Namespace, arg.TableName).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.TableHealthSnapshot{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.TableHealthSnapshot{}, err
	}
	return toDBTableHealthSnapshot(row), nil
}

// ListTableHealthByCatalog lists a catalog's table health by score, namespace
// and table. The optional needs filter matches the same predicates as the sqlc
// query (compaction, snapshot_expiry, or an operation in due_operations).
func (r *Repo) ListTableHealthByCatalog(ctx context.Context, arg db.ListTableHealthByCatalogParams) ([]db.TableHealthSnapshot, error) {
	q := r.Users.db.WithContext(ctx).Where("catalog_id = ?", UUIDString(arg.CatalogID))
	if arg.Ns != nil {
		q = q.Where("namespace = ?", *arg.Ns)
	}
	if arg.MaxScore != nil {
		q = q.Where("health_score <= ?", *arg.MaxScore)
	}
	// The boolean needs filters are portable; the operation membership is not
	// (due_operations is an array on PostgreSQL), so only that one is reduced
	// in Go.
	pageInDB := true
	if arg.Needs != nil {
		switch *arg.Needs {
		case "compaction":
			q = q.Where("needs_compaction = ?", true)
		case "snapshot_expiry":
			q = q.Where("needs_snapshot_expiry = ?", true)
		default:
			pageInDB = false
		}
	}
	q = q.Order("health_score ASC, namespace, table_name")
	if pageInDB {
		var rows []model.TableHealthSnapshot
		if err := q.Limit(int(arg.Lim)).Offset(int(arg.Off)).Find(&rows).Error; err != nil {
			return nil, err
		}
		out := make([]db.TableHealthSnapshot, len(rows))
		for i, row := range rows {
			out[i] = toDBTableHealthSnapshot(row)
		}
		return out, nil
	}
	var rows []model.TableHealthSnapshot
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.TableHealthSnapshot, 0, len(rows))
	for _, row := range rows {
		if !tableHealthNeeds(row, arg.Needs) {
			continue
		}
		out = append(out, toDBTableHealthSnapshot(row))
	}
	return pageTableHealth(out, arg.Off, arg.Lim), nil
}

// ListLastMaintenancePerOperation is the latest finished run of each operation
// of the given tables of a catalog.
func (r *Repo) ListLastMaintenancePerOperation(ctx context.Context, arg db.ListLastMaintenancePerOperationParams) ([]db.ListLastMaintenancePerOperationRow, error) {
	if len(arg.Namespaces) == 0 || len(arg.Tables) == 0 {
		return nil, nil
	}
	namespaces := make(map[string]bool, len(arg.Namespaces))
	for _, ns := range arg.Namespaces {
		namespaces[ns] = true
	}
	tables := make(map[string]bool, len(arg.Tables))
	for _, table := range arg.Tables {
		tables[table] = true
	}
	runs, err := r.catalogFinishedRuns(ctx, arg.CatalogID)
	if err != nil {
		return nil, err
	}
	type key struct{ namespace, table string }
	last := map[key]map[string]time.Time{}
	for _, run := range runs {
		k := key{string(run.Namespace), string(run.TableNameValue)}
		if !namespaces[k.namespace] || !tables[k.table] {
			continue
		}
		if last[k] == nil {
			last[k] = map[string]time.Time{}
		}
		for _, op := range run.Operations {
			if run.FinishedAt != nil {
				if at, ok := last[k][op]; !ok || run.FinishedAt.After(at) {
					last[k][op] = *run.FinishedAt
				}
			}
		}
	}
	var out []db.ListLastMaintenancePerOperationRow
	for k, ops := range last {
		for op, at := range ops {
			out = append(out, db.ListLastMaintenancePerOperationRow{
				Namespace: k.namespace, TableName: k.table, Operation: op, LastAt: PGTime(at),
			})
		}
	}
	return out, nil
}

// ── maintenance runs ──────────────────────────────────────────────────────────

// CreateMaintenanceRun records a run started by the manual maintenance path.
func (r *Repo) CreateMaintenanceRun(ctx context.Context, arg db.CreateMaintenanceRunParams) (db.MaintenanceRun, error) {
	row := model.MaintenanceRun{
		ID:             model.UUID(NewUUID()),
		CatalogID:      model.UUID(UUIDString(arg.CatalogID)),
		BindingID:      modelUUIDPtr(arg.BindingID),
		Namespace:      model.Text(arg.Namespace),
		TableNameValue: model.Text(arg.TableName),
		Operations:     model.StringArray(arg.Operations),
		Retention:      arg.Retention,
		Status:         "running",
		StartedBy:      modelUUIDPtr(arg.StartedBy),
		StartedAt:      time.Now().UTC(),
		WorkflowID:     arg.WorkflowID,
		Trigger:        model.Text("manual"),
		Reason:         model.JSON("{}"),
		Parameters:     model.JSON("{}"),
		Attempts:       1,
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.MaintenanceRun{}, asUniqueViolation(err, "maintenance_runs_one_running")
	}
	return toDBMaintenanceRun(row), nil
}

// UpdateMaintenanceRun closes a run with its outcome.
func (r *Repo) UpdateMaintenanceRun(ctx context.Context, arg db.UpdateMaintenanceRunParams) (db.MaintenanceRun, error) {
	res := r.Users.db.WithContext(ctx).Model(&model.MaintenanceRun{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"status":        arg.Status,
			"error_code":    arg.ErrorCode,
			"error_message": arg.ErrorMessage,
			"finished_at":   time.Now().UTC(),
		})
	if res.Error != nil {
		return db.MaintenanceRun{}, res.Error
	}
	if res.RowsAffected == 0 {
		return db.MaintenanceRun{}, pgx.ErrNoRows
	}
	return r.getMaintenanceRun(ctx, arg.ID)
}

// GetMaintenanceRun returns one run.
func (r *Repo) GetMaintenanceRun(ctx context.Context, id pgtype.UUID) (db.MaintenanceRun, error) {
	return r.getMaintenanceRun(ctx, id)
}

// ListMaintenanceRuns lists a catalog's runs, newest first, with the filters
// the monitoring list offers. The operation filter is applied in Go because
// operations is an array (ADR 0020: no dialect fragment); every other filter
// and the page stay in the database.
func (r *Repo) ListMaintenanceRuns(ctx context.Context, arg db.ListMaintenanceRunsParams) ([]db.MaintenanceRun, error) {
	q := r.Users.db.WithContext(ctx).Where("catalog_id = ?", UUIDString(arg.CatalogID))
	if arg.Trigger != nil {
		q = q.Where(map[string]any{"trigger": *arg.Trigger})
	}
	if arg.Status != nil {
		q = q.Where("status = ?", *arg.Status)
	}
	if arg.Ns != nil {
		q = q.Where("namespace = ?", *arg.Ns)
	}
	if arg.Tbl != nil {
		q = q.Where("table_name = ?", *arg.Tbl)
	}
	q = q.Order("started_at DESC")
	if arg.Operation == nil {
		var rows []model.MaintenanceRun
		if err := q.Limit(int(arg.Lim)).Offset(int(arg.Off)).Find(&rows).Error; err != nil {
			return nil, err
		}
		out := make([]db.MaintenanceRun, len(rows))
		for i, row := range rows {
			out[i] = toDBMaintenanceRun(row)
		}
		return out, nil
	}
	var rows []model.MaintenanceRun
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.MaintenanceRun, 0, len(rows))
	for _, row := range rows {
		if !containsString(row.Operations, *arg.Operation) {
			continue
		}
		out = append(out, toDBMaintenanceRun(row))
	}
	start, end := sliceBounds(len(out), arg.Off, arg.Lim)
	return out[start:end], nil
}

// ListTableMaintenanceRuns lists one table's runs, newest first.
func (r *Repo) ListTableMaintenanceRuns(ctx context.Context, arg db.ListTableMaintenanceRunsParams) ([]db.MaintenanceRun, error) {
	var rows []model.MaintenanceRun
	if err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND namespace = ? AND table_name = ?",
			UUIDString(arg.CatalogID), arg.Namespace, arg.TableName).
		Order("started_at DESC").
		Limit(int(arg.Lim)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.MaintenanceRun, len(rows))
	for i, row := range rows {
		out[i] = toDBMaintenanceRun(row)
	}
	return out, nil
}

// GetRunningMaintenanceRun returns the table's in-progress run.
func (r *Repo) GetRunningMaintenanceRun(ctx context.Context, arg db.GetRunningMaintenanceRunParams) (db.MaintenanceRun, error) {
	var row model.MaintenanceRun
	err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND namespace = ? AND table_name = ? AND status = ?",
			UUIDString(arg.CatalogID), arg.Namespace, arg.TableName, "running").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.MaintenanceRun{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.MaintenanceRun{}, err
	}
	return toDBMaintenanceRun(row), nil
}

func (r *Repo) getMaintenanceRun(ctx context.Context, id pgtype.UUID) (db.MaintenanceRun, error) {
	var row model.MaintenanceRun
	err := r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.MaintenanceRun{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.MaintenanceRun{}, err
	}
	return toDBMaintenanceRun(row), nil
}

// catalogFinishedRuns returns the finished runs of a catalog with a finish time.
func (r *Repo) catalogFinishedRuns(ctx context.Context, catalogID pgtype.UUID) ([]model.MaintenanceRun, error) {
	var rows []model.MaintenanceRun
	if err := r.Users.db.WithContext(ctx).
		Select("namespace", "table_name", "operations", "finished_at").
		Where("catalog_id = ? AND status = ? AND finished_at IS NOT NULL", UUIDString(catalogID), "finished").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ── conversions and helpers ───────────────────────────────────────────────────

func toDBClusterSnapshot(row model.ClusterSnapshot) db.ClusterSnapshot {
	return db.ClusterSnapshot{
		ID:               PGUUID(string(row.ID)),
		CapturedAt:       PGTime(row.CapturedAt),
		ActiveWorkers:    row.ActiveWorkers,
		RunningQueries:   row.RunningQueries,
		QueuedQueries:    row.QueuedQueries,
		BlockedQueries:   row.BlockedQueries,
		TotalMemoryBytes: row.TotalMemoryBytes,
		FreeMemoryBytes:  row.FreeMemoryBytes,
		RawPayload:       []byte(modelJSONValue(row.RawPayload)),
		EngineID:         PGUUID(string(row.EngineID)),
	}
}

func toDBTrinoQuerySnapshot(row model.TrinoQuerySnapshot) db.TrinoQuerySnapshot {
	return db.TrinoQuerySnapshot{
		ID:                 PGUUID(string(row.ID)),
		TrinoQueryID:       string(row.TrinoQueryID),
		ExecutionID:        uuidPtr(row.ExecutionID),
		Origin:             string(row.Origin),
		User:               string(row.User),
		State:              string(row.State),
		Sql:                row.SQL,
		Catalog:            row.Catalog,
		Schema:             row.Schema,
		CreatedAt:          PGTime(row.CreatedAt),
		ElapsedMs:          row.ElapsedMs,
		CpuMs:              row.CPUMs,
		PhysicalInputBytes: row.PhysicalInputBytes,
		OutputRows:         row.OutputRows,
		ErrorMessage:       row.ErrorMessage,
		RawTrinoPayload:    []byte(modelJSONValue(row.RawTrinoPayload)),
		FirstSeenAt:        PGTime(row.FirstSeenAt),
		LastSeenAt:         PGTime(row.LastSeenAt),
		EngineID:           PGUUID(string(row.EngineID)),
	}
}

func toDBTableHealthSnapshot(row model.TableHealthSnapshot) db.TableHealthSnapshot {
	return db.TableHealthSnapshot{
		ID:                     PGUUID(string(row.ID)),
		CatalogConnectionID:    PGUUID(string(row.CatalogConnectionID)),
		Catalog:                string(row.Catalog),
		Namespace:              string(row.Namespace),
		TableName:              string(row.TableNameValue),
		CapturedAt:             PGTime(row.CapturedAt),
		SnapshotCount:          row.SnapshotCount,
		FileCount:              row.FileCount,
		TotalSizeBytes:         row.TotalSizeBytes,
		OldestSnapshotAgeHours: row.OldestSnapshotAgeHours,
		LastMaintenanceAt:      PGTimePtr(row.LastMaintenanceAt),
		HealthScore:            row.HealthScore,
		NeedsCompaction:        row.NeedsCompaction,
		NeedsSnapshotExpiry:    row.NeedsSnapshotExpiry,
		CatalogID:              PGUUID(string(row.CatalogID)),
		DeleteFileCount:        row.DeleteFileCount,
		EqualityDeleteCount:    row.EqualityDeleteCount,
		PositionDeleteCount:    row.PositionDeleteCount,
		SmallFileCount:         row.SmallFileCount,
		CommitsLastHour:        row.CommitsLastHour,
		CommitsLastDay:         row.CommitsLastDay,
		AvgFileSizeBytes:       row.AvgFileSizeBytes,
		ManifestCount:          row.ManifestCount,
		FormatVersion:          row.FormatVersion,
		TableProperties:        []byte(row.TableProperties),
		DueOperations:          []string(row.DueOperations),
		PolicyRevision:         row.PolicyRevision,
	}
}

func toDBMaintenanceRun(row model.MaintenanceRun) db.MaintenanceRun {
	return db.MaintenanceRun{
		ID:             PGUUID(string(row.ID)),
		CatalogID:      PGUUID(string(row.CatalogID)),
		BindingID:      uuidPtr(row.BindingID),
		Namespace:      string(row.Namespace),
		TableName:      string(row.TableNameValue),
		Operations:     []string(row.Operations),
		Retention:      row.Retention,
		Status:         row.Status,
		ErrorCode:      row.ErrorCode,
		ErrorMessage:   row.ErrorMessage,
		StartedBy:      uuidPtr(row.StartedBy),
		StartedAt:      PGTime(row.StartedAt),
		FinishedAt:     PGTimePtr(row.FinishedAt),
		WorkflowID:     row.WorkflowID,
		Trigger:        string(row.Trigger),
		Reason:         []byte(row.Reason),
		Parameters:     []byte(row.Parameters),
		Attempts:       row.Attempts,
		PolicyRevision: row.PolicyRevision,
	}
}

// tableHealthNeeds matches the needs filter of the sqlc read.
func tableHealthNeeds(row model.TableHealthSnapshot, needs *string) bool {
	if needs == nil {
		return true
	}
	switch *needs {
	case "compaction":
		return row.NeedsCompaction
	case "snapshot_expiry":
		return row.NeedsSnapshotExpiry
	default:
		return containsString(row.DueOperations, *needs)
	}
}

// pageTableHealth applies the sqlc offset and limit after the Go-side filters.
func pageTableHealth(rows []db.TableHealthSnapshot, off, lim int32) []db.TableHealthSnapshot {
	start, end := sliceBounds(len(rows), off, lim)
	return rows[start:end]
}

// sliceBounds clamps an offset/limit pair to the slice length. A zero limit
// keeps SQL's LIMIT 0 meaning: no rows.
func sliceBounds(n int, off, lim int32) (int, int) {
	start := int(off)
	if start > n {
		start = n
	}
	if start < 0 {
		start = 0
	}
	if lim <= 0 {
		return start, start
	}
	end := n
	if start+int(lim) < end {
		end = start + int(lim)
	}
	return start, end
}

func firstNonNil[T any](a, b *T) *T {
	if a != nil {
		return a
	}
	return b
}
