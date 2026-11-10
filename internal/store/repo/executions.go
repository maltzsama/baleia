package repo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/webhooks"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ── executions ────────────────────────────────────────────────────────────────

// CreateQueryExecution inserts a running execution with an application-
// generated id and now's start time.
func (r *Repo) CreateQueryExecution(ctx context.Context, arg db.CreateQueryExecutionParams) (db.QueryExecution, error) {
	row := model.QueryExecution{
		ID:              model.UUID(NewUUID()),
		WorkspaceID:     model.UUID(UUIDString(arg.WorkspaceID)),
		UserID:          model.UUID(UUIDString(arg.UserID)),
		SavedQueryID:    modelUUIDPtr(arg.SavedQueryID),
		Status:          model.Text(arg.Status),
		SQL:             model.Text(arg.Sql),
		StartedAt:       time.Now().UTC(),
		TrinoCatalog:    arg.TrinoCatalog,
		RequestID:       arg.RequestID,
		EngineID:        modelUUIDPtr(arg.EngineID),
		TargetBindingID: modelUUIDPtr(arg.TargetBindingID),
		TargetCatalogID: modelUUIDPtr(arg.TargetCatalogID),
		TargetSchema:    arg.TargetSchema,
		TargetSnapshot:  modelJSONPtr(arg.TargetSnapshot),
		AgentID:         modelUUIDPtr(arg.AgentID),
		QueueClass:      arg.QueueClass,
		QueueReason:     arg.QueueReason,
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.QueryExecution{}, err
	}
	return toDBQueryExecution(row), nil
}

// GetQueryExecutionByID returns one execution or db.ErrNoRows.
func (r *Repo) GetQueryExecutionByID(ctx context.Context, id db.UUID) (db.QueryExecution, error) {
	if !id.Valid {
		return db.QueryExecution{}, db.ErrNoRows
	}
	return r.getExecution(ctx, "id = ?", UUIDString(id))
}

// ListQueryExecutionsByWorkspace returns a page of the workspace's executions,
// newest first.
func (r *Repo) ListQueryExecutionsByWorkspace(ctx context.Context, arg db.ListQueryExecutionsByWorkspaceParams) ([]db.QueryExecution, error) {
	var rows []model.QueryExecution
	if err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ?", UUIDString(arg.WorkspaceID)).
		Order("started_at DESC").
		Limit(int(arg.Limit)).
		Offset(int(arg.Offset)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.QueryExecution, len(rows))
	for i, row := range rows {
		out[i] = toDBQueryExecution(row)
	}
	return out, nil
}

// UpdateQueryExecution ends a running execution. Only a running row
// transitions, so a late completion never overwrites a cancellation; zero
// matched rows is db.ErrNoRows.
func (r *Repo) UpdateQueryExecution(ctx context.Context, arg db.UpdateQueryExecutionParams) (db.QueryExecution, error) {
	if !arg.ID.Valid {
		return db.QueryExecution{}, db.ErrNoRows
	}
	var out db.QueryExecution
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.QueryExecution{}).
			Where("id = ? AND status = ?", UUIDString(arg.ID), "running").
			Updates(map[string]any{
				"status":          arg.Status,
				"engine_query_id": arg.EngineQueryID,
				"rows_returned":   arg.RowsReturned,
				"duration_ms":     arg.DurationMs,
				"finished_at":     TimePtr(arg.FinishedAt),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return db.ErrNoRows
		}
		row, err := getExecutionTx(ctx, tx, "id = ?", UUIDString(arg.ID))
		out = row
		if err == nil {
			err = r.enqueueTx(tx, "execution", UUIDString(arg.ID))
		}
		if err == nil && terminalStatus(out.Status) {
			err = r.enqueueTx(tx, "webhook", webhooks.ExecutionFinishedEvent(
				UUIDString(out.WorkspaceID), UUIDString(out.ID), out.Status,
				out.RowsReturned, out.DurationMs, out.ErrorCode,
			).Marshal())
		}
		return err
	})
	return out, err
}

// CancelQueryExecution cancels a running or queued execution. Zero matched
// rows means it is gone, foreign, or already terminal.
func (r *Repo) CancelQueryExecution(ctx context.Context, arg db.CancelQueryExecutionParams) (db.QueryExecution, error) {
	if !arg.ID.Valid || !arg.WorkspaceID.Valid {
		return db.QueryExecution{}, db.ErrNoRows
	}
	var out db.QueryExecution
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.QueryExecution{}).
			Where("id = ? AND workspace_id = ? AND status IN ?", UUIDString(arg.ID), UUIDString(arg.WorkspaceID), []string{"running", "queued"}).
			Updates(map[string]any{"status": "cancelled", "finished_at": time.Now().UTC()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return db.ErrNoRows
		}
		row, err := getExecutionTx(ctx, tx, "id = ?", UUIDString(arg.ID))
		out = row
		if err == nil {
			err = r.enqueueTx(tx, "execution", UUIDString(arg.ID))
		}
		if err == nil && terminalStatus(out.Status) {
			err = r.enqueueTx(tx, "webhook", webhooks.ExecutionFinishedEvent(
				UUIDString(out.WorkspaceID), UUIDString(out.ID), out.Status,
				out.RowsReturned, out.DurationMs, out.ErrorCode,
			).Marshal())
		}
		return err
	})
	return out, err
}

// FailQueryExecution records the error code of a run that failed before any
// statement ran; only a running or queued row transitions.
func (r *Repo) FailQueryExecution(ctx context.Context, arg db.FailQueryExecutionParams) (db.QueryExecution, error) {
	if !arg.ID.Valid {
		return db.QueryExecution{}, db.ErrNoRows
	}
	var out db.QueryExecution
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.QueryExecution{}).
			Where("id = ? AND status IN ?", UUIDString(arg.ID), []string{"running", "queued"}).
			Updates(map[string]any{
				"status":      "failed",
				"error_code":  arg.ErrorCode,
				"finished_at": time.Now().UTC(),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return db.ErrNoRows
		}
		row, err := getExecutionTx(ctx, tx, "id = ?", UUIDString(arg.ID))
		out = row
		if err == nil {
			err = r.enqueueTx(tx, "execution", UUIDString(arg.ID))
		}
		if err == nil && terminalStatus(out.Status) {
			err = r.enqueueTx(tx, "webhook", webhooks.ExecutionFinishedEvent(
				UUIDString(out.WorkspaceID), UUIDString(out.ID), out.Status,
				out.RowsReturned, out.DurationMs, out.ErrorCode,
			).Marshal())
		}
		return err
	})
	return out, err
}

// QueueQueryExecution moves a running execution into its admission queue.
func (r *Repo) QueueQueryExecution(ctx context.Context, id db.UUID) (db.QueryExecution, error) {
	if !id.Valid {
		return db.QueryExecution{}, db.ErrNoRows
	}
	var out db.QueryExecution
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.QueryExecution{}).
			Where("id = ? AND status = ?", UUIDString(id), "running").
			Updates(map[string]any{"status": "queued", "queued_at": time.Now().UTC()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return db.ErrNoRows
		}
		row, err := getExecutionTx(ctx, tx, "id = ?", UUIDString(id))
		out = row
		if err == nil {
			err = r.enqueueTx(tx, "execution", UUIDString(id))
		}
		return err
	})
	return out, err
}

// AdmitQueryExecution moves a queued execution back to running and records
// the wait.
func (r *Repo) AdmitQueryExecution(ctx context.Context, id db.UUID) (db.QueryExecution, error) {
	if !id.Valid {
		return db.QueryExecution{}, db.ErrNoRows
	}
	var out db.QueryExecution
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.QueryExecution
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", UUIDString(id)).
			First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return db.ErrNoRows
		}
		if err != nil {
			return err
		}
		if string(row.Status) != "queued" {
			return db.ErrNoRows
		}
		now := time.Now().UTC()
		var wait int64
		if row.QueuedAt != nil {
			wait = now.Sub(*row.QueuedAt).Milliseconds()
		}
		if err := tx.Model(&model.QueryExecution{}).
			Where("id = ?", UUIDString(id)).
			Updates(map[string]any{
				"status":        "running",
				"admitted_at":   now,
				"queue_wait_ms": wait,
			}).Error; err != nil {
			return err
		}
		row.Status = "running"
		row.AdmittedAt = &now
		row.QueueWaitMs = &wait
		out = toDBQueryExecution(row)
		return r.enqueueTx(tx, "execution", UUIDString(id))
	})
	return out, err
}

// SetExecutionResult points the execution at its last result set.
func (r *Repo) SetExecutionResult(ctx context.Context, arg db.SetExecutionResultParams) error {
	if !arg.ID.Valid {
		return nil
	}
	return r.Users.db.WithContext(ctx).Model(&model.QueryExecution{}).
		Where("id = ?", UUIDString(arg.ID)).
		Update("result_key", arg.ResultKey).Error
}

// ListExecutionHistory returns a keyset page of the workspace's executions,
// newest first, with each runner's name.
func (r *Repo) ListExecutionHistory(ctx context.Context, arg db.ListExecutionHistoryParams) ([]db.ListExecutionHistoryRow, error) {
	q := r.Users.db.WithContext(ctx).Model(&model.QueryExecution{}).
		Where("workspace_id = ?", UUIDString(arg.WorkspaceID))
	if arg.UserID.Valid {
		q = q.Where("user_id = ?", UUIDString(arg.UserID))
	}
	if arg.Status != nil {
		q = q.Where("status = ?", *arg.Status)
	}
	if arg.QueueClass != nil {
		q = q.Where("queue_class = ?", *arg.QueueClass)
	}
	if arg.FromTime.Valid {
		q = q.Where("started_at >= ?", arg.FromTime.Time)
	}
	if arg.ToTime.Valid {
		q = q.Where("started_at <= ?", arg.ToTime.Time)
	}
	if arg.Q != nil {
		q = q.Where("LOWER(sql) LIKE ?", "%"+strings.ToLower(*arg.Q)+"%")
	}
	if arg.CatalogID.Valid {
		q = q.Where("target_catalog_id = ?", UUIDString(arg.CatalogID))
	}
	if arg.EngineID.Valid {
		q = q.Where("engine_id = ?", UUIDString(arg.EngineID))
	}
	if arg.CursorAt.Valid && arg.CursorID.Valid {
		at := arg.CursorAt.Time
		q = q.Where("started_at < ? OR (started_at = ? AND id < ?)", at, at, UUIDString(arg.CursorID))
	}
	var rows []model.QueryExecution
	if err := q.Order("started_at DESC, id DESC").Limit(int(arg.Lim)).Find(&rows).Error; err != nil {
		return nil, err
	}
	names, err := r.executionUserNames(ctx, rows)
	if err != nil {
		return nil, err
	}
	out := make([]db.ListExecutionHistoryRow, len(rows))
	for i, row := range rows {
		out[i] = toDBListExecutionHistoryRow(row, names[string(row.UserID)])
	}
	return out, nil
}

// IsWorkspaceStructuralOwner reports whether the user is the personal
// workspace's owner or an owner of the owning team (#36, #611).
func (r *Repo) IsWorkspaceStructuralOwner(ctx context.Context, arg db.IsWorkspaceStructuralOwnerParams) (bool, error) {
	if !arg.ID.Valid || !arg.UserID.Valid {
		return false, nil
	}
	var ws model.Workspace
	err := r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(arg.ID)).First(&ws).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	userID := UUIDString(arg.UserID)
	if ws.OwnerUserID != nil && sameUUID(string(*ws.OwnerUserID), userID) {
		return true, nil
	}
	if ws.OwnerTeamID == nil {
		return false, nil
	}
	var count int64
	err = r.Users.db.WithContext(ctx).Model(&model.TeamMember{}).
		Where("team_id = ? AND user_id = ? AND role = ?", string(*ws.OwnerTeamID), userID, "owner").
		Count(&count).Error
	return count > 0, err
}

// ListRunningStatementHandles returns the persisted handles of an execution's
// running statements, by position.
func (r *Repo) ListRunningStatementHandles(ctx context.Context, executionID db.UUID) ([]db.ListRunningStatementHandlesRow, error) {
	var rows []model.QueryStatement
	if err := r.Users.db.WithContext(ctx).
		Where("execution_id = ? AND query_handle IS NOT NULL AND status = ?", UUIDString(executionID), "running").
		Order("position").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ListRunningStatementHandlesRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListRunningStatementHandlesRow{Position: row.Position, QueryHandle: []byte(modelJSONValue(row.QueryHandle))}
	}
	return out, nil
}

// ── statements ────────────────────────────────────────────────────────────────

// CreateQueryStatement inserts a statement record.
func (r *Repo) CreateQueryStatement(ctx context.Context, arg db.CreateQueryStatementParams) (db.QueryStatement, error) {
	row := model.QueryStatement{
		ID:          model.UUID(NewUUID()),
		ExecutionID: model.UUID(UUIDString(arg.ExecutionID)),
		Position:    arg.Position,
		SQL:         arg.Sql,
		Status:      arg.Status,
		StartedAt:   time.Now().UTC(),
		ResultSets:  model.JSON("[]"),
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.QueryStatement{}, err
	}
	return toDBQueryStatement(row), nil
}

// ListQueryStatements returns an execution's statements by position.
func (r *Repo) ListQueryStatements(ctx context.Context, executionID db.UUID) ([]db.QueryStatement, error) {
	var rows []model.QueryStatement
	if err := r.Users.db.WithContext(ctx).
		Where("execution_id = ?", UUIDString(executionID)).
		Order("position").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.QueryStatement, len(rows))
	for i, row := range rows {
		out[i] = toDBQueryStatement(row)
	}
	return out, nil
}

// ListQueryStatementsByWorkspace returns the statements of an execution of the
// workspace, by position.
func (r *Repo) ListQueryStatementsByWorkspace(ctx context.Context, arg db.ListQueryStatementsByWorkspaceParams) ([]db.QueryStatement, error) {
	if !r.executionInWorkspace(ctx, arg.ExecutionID, arg.WorkspaceID) {
		return nil, nil
	}
	return r.ListQueryStatements(ctx, arg.ExecutionID)
}

// GetQueryStatementByPosition returns one statement of an execution of the
// workspace or db.ErrNoRows.
func (r *Repo) GetQueryStatementByPosition(ctx context.Context, arg db.GetQueryStatementByPositionParams) (db.QueryStatement, error) {
	if !r.executionInWorkspace(ctx, arg.ExecutionID, arg.WorkspaceID) {
		return db.QueryStatement{}, db.ErrNoRows
	}
	var row model.QueryStatement
	err := r.Users.db.WithContext(ctx).
		Where("execution_id = ? AND position = ?", UUIDString(arg.ExecutionID), arg.Position).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.QueryStatement{}, db.ErrNoRows
	}
	if err != nil {
		return db.QueryStatement{}, err
	}
	return toDBQueryStatement(row), nil
}

// UpdateQueryStatement ends a running statement. Zero matched rows is
// db.ErrNoRows.
func (r *Repo) UpdateQueryStatement(ctx context.Context, arg db.UpdateQueryStatementParams) (db.QueryStatement, error) {
	if !arg.ID.Valid {
		return db.QueryStatement{}, db.ErrNoRows
	}
	resultSets := arg.ResultSets
	if resultSets == nil {
		resultSets = []byte("[]")
	}
	var out db.QueryStatement
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.QueryStatement{}).
			Where("id = ? AND status = ?", UUIDString(arg.ID), "running").
			Updates(map[string]any{
				"status":          arg.Status,
				"engine_query_id": arg.EngineQueryID,
				"rows_returned":   arg.RowsReturned,
				"duration_ms":     arg.DurationMs,
				"error_message":   arg.ErrorMessage,
				"finished_at":     TimePtr(arg.FinishedAt),
				"result_key":      arg.ResultKey,
				"result_bytes":    arg.ResultBytes,
				"truncated":       arg.Truncated,
				"result_sets":     model.JSON(resultSets),
				"error_code":      arg.ErrorCode,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return db.ErrNoRows
		}
		var row model.QueryStatement
		if err := tx.Where("id = ?", UUIDString(arg.ID)).First(&row).Error; err != nil {
			return err
		}
		out = toDBQueryStatement(row)
		return r.enqueueTx(tx, "execution", string(row.ExecutionID))
	})
	return out, err
}

// StartQueryStatement turns a pending statement into the running one; zero
// rows means it is no longer pending.
func (r *Repo) StartQueryStatement(ctx context.Context, arg db.StartQueryStatementParams) (int64, error) {
	var affected int64
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.QueryStatement{}).
			Where("execution_id = ? AND position = ? AND status = ?", UUIDString(arg.ExecutionID), arg.Position, "pending").
			Updates(map[string]any{"status": "running", "started_at": time.Now().UTC()})
		affected = res.RowsAffected
		if res.Error != nil {
			return res.Error
		}
		if affected > 0 {
			return r.enqueueTx(tx, "execution", UUIDString(arg.ExecutionID))
		}
		return nil
	})
	return affected, err
}

// SkipPendingStatements skips the statements that never ran.
func (r *Repo) SkipPendingStatements(ctx context.Context, executionID db.UUID) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.QueryStatement{}).
			Where("execution_id = ? AND status = ?", UUIDString(executionID), "pending").
			Updates(map[string]any{"status": "skipped", "finished_at": time.Now().UTC()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			return r.enqueueTx(tx, "execution", UUIDString(executionID))
		}
		return nil
	})
}

// AbandonRunningStatements ends the statements still running and returns
// their handles.
func (r *Repo) AbandonRunningStatements(ctx context.Context, arg db.AbandonRunningStatementsParams) ([]db.AbandonRunningStatementsRow, error) {
	var out []db.AbandonRunningStatementsRow
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []model.QueryStatement
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("execution_id = ? AND status = ?", UUIDString(arg.ExecutionID), "running").
			Order("position").
			Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]string, len(rows))
		out = make([]db.AbandonRunningStatementsRow, len(rows))
		for i, row := range rows {
			ids[i] = string(row.ID)
			out[i] = db.AbandonRunningStatementsRow{Position: row.Position, QueryHandle: []byte(modelJSONValue(row.QueryHandle))}
		}
		if err := tx.Model(&model.QueryStatement{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"status":        arg.Status,
				"error_code":    arg.ErrorCode,
				"error_message": arg.ErrorMessage,
				"finished_at":   time.Now().UTC(),
			}).Error; err != nil {
			return err
		}
		return r.enqueueTx(tx, "execution", UUIDString(arg.ExecutionID))
	})
	return out, err
}

// SetStatementQueryHandle persists the engine's handle of a running statement.
func (r *Repo) SetStatementQueryHandle(ctx context.Context, arg db.SetStatementQueryHandleParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.QueryStatement{}).
		Where("execution_id = ? AND position = ? AND status = ?", UUIDString(arg.ExecutionID), arg.Position, "running").
		Update("query_handle", modelJSONPtr(arg.QueryHandle)).Error
}

// ── queues and workloads ──────────────────────────────────────────────────────

// ListEngineQueueCapacities returns the per-class capacities of one engine.
func (r *Repo) ListEngineQueueCapacities(ctx context.Context, engineID db.UUID) ([]db.EngineQueueCapacity, error) {
	var rows []model.EngineQueueCapacity
	if err := r.Users.db.WithContext(ctx).
		Where("engine_id = ?", UUIDString(engineID)).
		Order("class").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.EngineQueueCapacity, len(rows))
	for i, row := range rows {
		out[i] = db.EngineQueueCapacity{
			EngineID:         PGUUID(string(row.EngineID)),
			Class:            string(row.Class),
			MaxRunning:       row.MaxRunning,
			MaxQueued:        row.MaxQueued,
			QueueTimeoutMs:   row.QueueTimeoutMs,
			CreatedAt:        PGTime(row.CreatedAt),
			UpdatedAt:        PGTime(row.UpdatedAt),
			RuntimeTimeoutMs: row.RuntimeTimeoutMs,
		}
	}
	return out, nil
}

// ListWorkloadRules returns the ordered workload rules (#399).
func (r *Repo) ListWorkloadRules(ctx context.Context) ([]db.WorkloadRule, error) {
	var rows []model.WorkloadRule
	if err := r.Users.db.WithContext(ctx).Order("position, created_at").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.WorkloadRule, len(rows))
	for i, row := range rows {
		out[i] = toDBWorkloadRule(row)
	}
	return out, nil
}

func terminalStatus(status string) bool {
	return status == "finished" || status == "failed" || status == "cancelled"
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (r *Repo) getExecution(ctx context.Context, where string, args ...any) (db.QueryExecution, error) {
	return getExecutionTx(ctx, r.Users.db.WithContext(ctx), where, args...)
}

func getExecutionTx(ctx context.Context, tx *gorm.DB, where string, args ...any) (db.QueryExecution, error) {
	var row model.QueryExecution
	err := tx.WithContext(ctx).Where(where, args...).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.QueryExecution{}, db.ErrNoRows
	}
	if err != nil {
		return db.QueryExecution{}, err
	}
	return toDBQueryExecution(row), nil
}

// executionInWorkspace reports whether an execution belongs to the workspace.
func (r *Repo) executionInWorkspace(ctx context.Context, executionID, workspaceID db.UUID) bool {
	if !executionID.Valid || !workspaceID.Valid {
		return false
	}
	var count int64
	err := r.Users.db.WithContext(ctx).Model(&model.QueryExecution{}).
		Where("id = ? AND workspace_id = ?", UUIDString(executionID), UUIDString(workspaceID)).
		Count(&count).Error
	return err == nil && count > 0
}

// executionUserNames resolves the runner names of the given executions.
func (r *Repo) executionUserNames(ctx context.Context, rows []model.QueryExecution) (map[string]string, error) {
	ids := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		id := string(row.UserID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var users []model.User
	if err := r.Users.db.WithContext(ctx).Select("id", "name").Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	for _, user := range users {
		out[string(user.ID)] = user.Name
	}
	return out, nil
}

func modelJSONPtr(b []byte) *model.JSON {
	if b == nil {
		return nil
	}
	j := model.JSON(b)
	return &j
}

func modelJSONValue(j *model.JSON) model.JSON {
	if j == nil {
		return nil
	}
	return *j
}

func toDBQueryStatement(s model.QueryStatement) db.QueryStatement {
	return db.QueryStatement{
		ID:            PGUUID(string(s.ID)),
		ExecutionID:   PGUUID(string(s.ExecutionID)),
		Position:      s.Position,
		Sql:           string(s.SQL),
		Status:        string(s.Status),
		EngineQueryID: s.EngineQueryID,
		RowsReturned:  s.RowsReturned,
		DurationMs:    s.DurationMs,
		ErrorMessage:  s.ErrorMessage,
		StartedAt:     PGTime(s.StartedAt),
		FinishedAt:    PGTimePtr(s.FinishedAt),
		ResultKey:     s.ResultKey,
		ResultBytes:   s.ResultBytes,
		Truncated:     s.Truncated,
		QueryHandle:   []byte(modelJSONValue(s.QueryHandle)),
		ResultSets:    []byte(s.ResultSets),
		ErrorCode:     s.ErrorCode,
	}
}

func toDBListExecutionHistoryRow(e model.QueryExecution, userName string) db.ListExecutionHistoryRow {
	return db.ListExecutionHistoryRow{
		ID:              PGUUID(string(e.ID)),
		WorkspaceID:     PGUUID(string(e.WorkspaceID)),
		UserID:          PGUUID(string(e.UserID)),
		UserName:        userName,
		SavedQueryID:    uuidPtr(e.SavedQueryID),
		Status:          string(e.Status),
		Sql:             string(e.SQL),
		EngineQueryID:   e.EngineQueryID,
		RowsReturned:    e.RowsReturned,
		DurationMs:      e.DurationMs,
		StartedAt:       PGTime(e.StartedAt),
		FinishedAt:      PGTimePtr(e.FinishedAt),
		TrinoCatalog:    e.TrinoCatalog,
		ResultKey:       e.ResultKey,
		RequestID:       e.RequestID,
		EngineID:        uuidPtr(e.EngineID),
		TargetBindingID: uuidPtr(e.TargetBindingID),
		TargetCatalogID: uuidPtr(e.TargetCatalogID),
		TargetSchema:    e.TargetSchema,
		TargetSnapshot:  []byte(modelJSONValue(e.TargetSnapshot)),
		QueueClass:      e.QueueClass,
		QueueReason:     e.QueueReason,
		QueuedAt:        PGTimePtr(e.QueuedAt),
		AdmittedAt:      PGTimePtr(e.AdmittedAt),
		QueueWaitMs:     e.QueueWaitMs,
	}
}
