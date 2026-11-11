package repo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// This file completes the GORM store's sqlc-shaped surface so the runtime no
// longer needs a db.Querier (stage A of #761). Each method mirrors one
// generated query while keeping the persistence GORM-only (ADR 0020).

// ── API keys ─────────────────────────────────────────────────────────────────

// GetAPIKeyByHash returns the key with this hash.
func (s *APIKeys) GetAPIKeyByHash(ctx context.Context, keyHash string) (db.ApiKey, error) {
	var row model.APIKey
	err := s.db.WithContext(ctx).Where("key_hash = ?", keyHash).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.ApiKey{}, db.ErrNoRows
	}
	if err != nil {
		return db.ApiKey{}, err
	}
	return toDBApiKey(row), nil
}

// GetAPIKeyByID returns a key only when it belongs to the user.
func (s *APIKeys) GetAPIKeyByID(ctx context.Context, arg db.GetAPIKeyByIDParams) (db.ApiKey, error) {
	var row model.APIKey
	err := s.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", UUIDString(arg.ID), UUIDString(arg.UserID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.ApiKey{}, db.ErrNoRows
	}
	if err != nil {
		return db.ApiKey{}, err
	}
	return toDBApiKey(row), nil
}

// TouchAPIKey records that the key was used.
func (s *APIKeys) TouchAPIKey(ctx context.Context, id db.UUID) error {
	return s.db.WithContext(ctx).Model(&model.APIKey{}).
		Where("id = ?", UUIDString(id)).
		Update("last_used_at", time.Now().UTC()).Error
}

// ── sessions ─────────────────────────────────────────────────────────────────

// GetRefreshTokenByHash returns a live token, unlike GetRefreshTokenByHashAny.
func (s *Sessions) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (db.RefreshToken, error) {
	var row model.RefreshToken
	err := s.db.WithContext(ctx).
		Where("token_hash = ? AND revoked = ? AND expires_at > ?", tokenHash, false, time.Now().UTC()).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.RefreshToken{}, db.ErrNoRows
	}
	if err != nil {
		return db.RefreshToken{}, err
	}
	return toDBRefreshToken(row), nil
}

// RevokeRefreshToken revokes one rotation member.
func (s *Sessions) RevokeRefreshToken(ctx context.Context, id db.UUID) error {
	return s.db.WithContext(ctx).Model(&model.RefreshToken{}).
		Where("id = ?", UUIDString(id)).
		Update("revoked", true).Error
}

// DeleteExpiredRefreshTokens prunes tokens past their expiry.
func (s *Sessions) DeleteExpiredRefreshTokens(ctx context.Context) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).
		Where("expires_at < ? OR revoked = ?", now, true).
		Delete(&model.RefreshToken{}).Error
}

// DeleteExpiredOIDCLoginAttempts prunes stale login attempts.
func (r *Repo) DeleteExpiredOIDCLoginAttempts(ctx context.Context) error {
	return r.Users.db.WithContext(ctx).
		Where("expires_at <= ?", time.Now().UTC()).
		Delete(&model.OIDCLoginAttempt{}).Error
}

// DeleteExpiredPasswordResetTokens prunes stale reset tokens.
func (r *Repo) DeleteExpiredPasswordResetTokens(ctx context.Context) error {
	return r.Users.db.WithContext(ctx).
		Where("expires_at < ? OR used_at IS NOT NULL", time.Now().UTC()).
		Delete(&model.PasswordResetToken{}).Error
}

// ── users and teams ──────────────────────────────────────────────────────────

// UpdateUserPersonalWorkspace points the user at their personal workspace.
func (s *Users) UpdateUserPersonalWorkspace(ctx context.Context, arg db.UpdateUserPersonalWorkspaceParams) error {
	return s.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"personal_workspace_id": model.UUID(UUIDString(arg.PersonalWorkspaceID)),
			"updated_at":            time.Now().UTC(),
		}).Error
}

// UpdateTeam renames a team and returns the row.
func (s *Teams) UpdateTeam(ctx context.Context, arg db.UpdateTeamParams) (db.Team, error) {
	if err := s.db.WithContext(ctx).Model(&model.Team{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"name":        arg.Name,
			"description": arg.Description,
			"updated_at":  time.Now().UTC(),
		}).Error; err != nil {
		return db.Team{}, err
	}
	return s.GetTeamByID(ctx, arg.ID)
}

// GetWorkspaceByPolarisName returns the workspace with this Polaris catalog.
func (r *Repo) GetWorkspaceByPolarisName(ctx context.Context, polarisCatalogName string) (db.Workspace, error) {
	var row model.Workspace
	err := r.Users.db.WithContext(ctx).
		Where("polaris_catalog_name = ?", polarisCatalogName).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Workspace{}, db.ErrNoRows
	}
	if err != nil {
		return db.Workspace{}, err
	}
	return toDBWorkspace(row), nil
}

// ── agents ───────────────────────────────────────────────────────────────────

// CountActiveAgentTokens counts live tokens of an agent.
func (r *Repo) CountActiveAgentTokens(ctx context.Context, agentID db.UUID) (int64, error) {
	var n int64
	err := r.Users.db.WithContext(ctx).Model(&model.AgentToken{}).
		Where("agent_id = ? AND revoked_at IS NULL AND expires_at > ?", UUIDString(agentID), time.Now().UTC()).
		Count(&n).Error
	return n, err
}

// CountRunningExecutionsByAgent counts the agent's running statements.
func (r *Repo) CountRunningExecutionsByAgent(ctx context.Context, agentID db.UUID) (int64, error) {
	var n int64
	err := r.Users.db.WithContext(ctx).Model(&model.QueryExecution{}).
		Where("agent_id = ? AND status = ?", UUIDString(agentID), "running").
		Count(&n).Error
	return n, err
}

// CountActiveExecutionsByAgent counts the runs holding an agent slot.
func (r *Repo) CountActiveExecutionsByAgent(ctx context.Context, agentID db.UUID) (int64, error) {
	var n int64
	err := r.Users.db.WithContext(ctx).Model(&model.QueryExecution{}).
		Where("agent_id = ? AND status IN ?", UUIDString(agentID), []string{"running", "queued"}).
		Count(&n).Error
	return n, err
}

// CountExecutionsByAgentSince counts the agent's runs started after the time.
func (r *Repo) CountExecutionsByAgentSince(ctx context.Context, arg db.CountExecutionsByAgentSinceParams) (int64, error) {
	q := r.Users.db.WithContext(ctx).Model(&model.QueryExecution{}).
		Where("agent_id = ?", UUIDString(arg.AgentID))
	if arg.StartedAt.Valid {
		q = q.Where("started_at > ?", arg.StartedAt.Time)
	}
	var n int64
	err := q.Count(&n).Error
	return n, err
}

// OldestExecutionByAgentSince returns the oldest run started after the time.
func (r *Repo) OldestExecutionByAgentSince(ctx context.Context, arg db.OldestExecutionByAgentSinceParams) (db.Timestamptz, error) {
	var row model.QueryExecution
	q := r.Users.db.WithContext(ctx).Where("agent_id = ?", UUIDString(arg.AgentID))
	if arg.StartedAt.Valid {
		q = q.Where("started_at > ?", arg.StartedAt.Time)
	}
	err := q.Order("started_at ASC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Timestamptz{}, db.ErrNoRows
	}
	if err != nil {
		return db.Timestamptz{}, err
	}
	return PGTime(row.StartedAt), nil
}

// CatalogGrantedToAgent reports whether the agent may use the catalog.
func (r *Repo) CatalogGrantedToAgent(ctx context.Context, arg db.CatalogGrantedToAgentParams) (bool, error) {
	var granted bool
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.WorkspaceCatalogGrant{}).
			Joins("JOIN agent_workspaces aw ON aw.workspace_id = workspace_catalog_grants.workspace_id").
			Where("aw.agent_id = ? AND workspace_catalog_grants.catalog_id = ?", UUIDString(arg.AgentID), UUIDString(arg.CatalogID)).
			Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		var grants []model.WorkspaceCatalogGrant
		if err := tx.Where("catalog_id = ?", UUIDString(arg.CatalogID)).Find(&grants).Error; err != nil {
			return err
		}
		for _, g := range grants {
			role, err := workspaceRole(tx, string(g.WorkspaceID), UUIDString(arg.UserID))
			if err != nil {
				return err
			}
			if role != "" {
				granted = true
				return nil
			}
		}
		return nil
	})
	return granted, err
}

// ── engines and bindings ─────────────────────────────────────────────────────

// GetEngineWithConnection returns the engine and the connection it runs on.
func (r *Repo) GetEngineWithConnection(ctx context.Context, id db.UUID) (db.GetEngineWithConnectionRow, error) {
	var row model.Engine
	err := r.Users.db.WithContext(ctx).Preload("Connection").
		Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetEngineWithConnectionRow{}, db.ErrNoRows
	}
	if err != nil {
		return db.GetEngineWithConnectionRow{}, err
	}
	if row.Connection == nil {
		return db.GetEngineWithConnectionRow{}, db.ErrNoRows
	}
	return db.GetEngineWithConnectionRow{
		Engine:            toDBEngine(row),
		CatalogConnection: toDBCatalogConnection(*row.Connection),
	}, nil
}

// GetDefaultTrinoEngine returns the engine linked to the 'default' cluster.
func (r *Repo) GetDefaultTrinoEngine(ctx context.Context) (db.GetDefaultTrinoEngineRow, error) {
	var cluster model.TrinoCluster
	err := r.Users.db.WithContext(ctx).Where("name = ?", "default").First(&cluster).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetDefaultTrinoEngineRow{}, db.ErrNoRows
	}
	if err != nil {
		return db.GetDefaultTrinoEngineRow{}, err
	}
	if cluster.EngineID == nil {
		return db.GetDefaultTrinoEngineRow{}, db.ErrNoRows
	}
	var row model.Engine
	err = r.Users.db.WithContext(ctx).Preload("Connection").
		Where("id = ?", string(*cluster.EngineID)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetDefaultTrinoEngineRow{}, db.ErrNoRows
	}
	if err != nil {
		return db.GetDefaultTrinoEngineRow{}, err
	}
	if row.Connection == nil {
		return db.GetDefaultTrinoEngineRow{}, db.ErrNoRows
	}
	return db.GetDefaultTrinoEngineRow{
		Engine:            toDBEngine(row),
		CatalogConnection: toDBCatalogConnection(*row.Connection),
	}, nil
}

// ListEnabledEnginesWithConnection returns every enabled engine and its
// connection, with the engine's Trino cluster when it has one.
func (r *Repo) ListEnabledEnginesWithConnection(ctx context.Context) ([]db.ListEnabledEnginesWithConnectionRow, error) {
	var engines []model.Engine
	if err := r.Users.db.WithContext(ctx).Preload("Connection").
		Where("enabled").Order("name").Find(&engines).Error; err != nil {
		return nil, err
	}
	var clusters []model.TrinoCluster
	if err := r.Users.db.WithContext(ctx).Find(&clusters).Error; err != nil {
		return nil, err
	}
	clusterOf := make(map[string]db.UUID, len(clusters))
	for _, c := range clusters {
		if c.EngineID != nil {
			clusterOf[string(*c.EngineID)] = PGUUID(string(c.ID))
		}
	}
	out := make([]db.ListEnabledEnginesWithConnectionRow, 0, len(engines))
	for _, e := range engines {
		if e.Connection == nil {
			continue
		}
		out = append(out, db.ListEnabledEnginesWithConnectionRow{
			Engine:            toDBEngine(e),
			CatalogConnection: toDBCatalogConnection(*e.Connection),
			ClusterID:         clusterOf[string(e.ID)],
		})
	}
	return out, nil
}

// GetBindingForTarget returns the engine and catalog an execution target needs.
func (r *Repo) GetBindingForTarget(ctx context.Context, id db.UUID) (db.GetBindingForTargetRow, error) {
	var row model.CatalogBinding
	err := r.Users.db.WithContext(ctx).Preload("Engine").Preload("Catalog").
		Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetBindingForTargetRow{}, db.ErrNoRows
	}
	if err != nil {
		return db.GetBindingForTargetRow{}, err
	}
	if row.Engine == nil || row.Catalog == nil {
		return db.GetBindingForTargetRow{}, db.ErrNoRows
	}
	return db.GetBindingForTargetRow{
		BindingID:         PGUUID(string(row.ID)),
		EngineCatalogName: string(row.EngineCatalogName),
		BindingEnabled:    row.Enabled,
		Mode:              row.Mode,
		EngineID:          PGUUID(string(row.Engine.ID)),
		EngineName:        string(row.Engine.Name),
		EngineKind:        row.Engine.Kind,
		EngineEnabled:     row.Engine.Enabled,
		CatalogID:         PGUUID(string(row.Catalog.ID)),
		CatalogName:       string(row.Catalog.Name),
		CatalogEnabled:    row.Catalog.Enabled,
	}, nil
}

// ListEnabledCatalogs lists every enabled catalog.
func (r *Repo) ListEnabledCatalogs(ctx context.Context) ([]db.Catalog, error) {
	var rows []model.Catalog
	if err := r.Users.db.WithContext(ctx).Where("enabled").Order("name").Find(&rows).Error; err != nil {
		return nil, err
	}
	return toDBCatalogs(rows), nil
}

// ListBindingStatusByCatalog reproduces the binding_status_v view per binding.
func (r *Repo) ListBindingStatusByCatalog(ctx context.Context, catalogID db.UUID) ([]db.BindingStatusV, error) {
	var rows []model.CatalogBinding
	if err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ?", UUIDString(catalogID)).
		Order("created_at").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	set, err := r.loadBindingStatusSet(r.Users.db.WithContext(ctx), rows)
	if err != nil {
		return nil, err
	}
	out := make([]db.BindingStatusV, 0, len(rows))
	for _, row := range rows {
		out = append(out, set.status(row))
	}
	return out, nil
}

// SetExecutionTarget stores the resolved target of an execution.
func (r *Repo) SetExecutionTarget(ctx context.Context, arg db.SetExecutionTargetParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.QueryExecution{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"engine_id":         model.UUID(UUIDString(arg.EngineID)),
			"target_binding_id": model.UUID(UUIDString(arg.TargetBindingID)),
			"target_catalog_id": model.UUID(UUIDString(arg.TargetCatalogID)),
			"target_schema":     arg.TargetSchema,
			"target_snapshot":   modelJSONPtr(arg.TargetSnapshot),
		}).Error
}

// ── monitoring ───────────────────────────────────────────────────────────────

// GetLatestClusterSnapshot returns the newest sample on the server.
func (r *Repo) GetLatestClusterSnapshot(ctx context.Context) (db.ClusterSnapshot, error) {
	var row model.ClusterSnapshot
	err := r.Users.db.WithContext(ctx).Order("captured_at DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.ClusterSnapshot{}, db.ErrNoRows
	}
	if err != nil {
		return db.ClusterSnapshot{}, err
	}
	return toDBClusterSnapshot(row), nil
}

// ListClusterSnapshots pages every cluster sample, newest first.
func (r *Repo) ListClusterSnapshots(ctx context.Context, arg db.ListClusterSnapshotsParams) ([]db.ClusterSnapshot, error) {
	var rows []model.ClusterSnapshot
	if err := r.Users.db.WithContext(ctx).
		Order("captured_at DESC").
		Limit(int(arg.Limit)).Offset(int(arg.Offset)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ClusterSnapshot, len(rows))
	for i, row := range rows {
		out[i] = toDBClusterSnapshot(row)
	}
	return out, nil
}

// ListActiveTrinoQueries lists the snapshots still running.
func (r *Repo) ListActiveTrinoQueries(ctx context.Context) ([]db.TrinoQuerySnapshot, error) {
	var rows []model.TrinoQuerySnapshot
	if err := r.Users.db.WithContext(ctx).
		Where("state = ?", "running").
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.TrinoQuerySnapshot, len(rows))
	for i, row := range rows {
		out[i] = toDBTrinoQuerySnapshot(row)
	}
	return out, nil
}

// ListTrinoQuerySnapshots pages every query snapshot, newest first.
func (r *Repo) ListTrinoQuerySnapshots(ctx context.Context, arg db.ListTrinoQuerySnapshotsParams) ([]db.TrinoQuerySnapshot, error) {
	var rows []model.TrinoQuerySnapshot
	if err := r.Users.db.WithContext(ctx).
		Order("created_at DESC").
		Limit(int(arg.Limit)).Offset(int(arg.Offset)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.TrinoQuerySnapshot, len(rows))
	for i, row := range rows {
		out[i] = toDBTrinoQuerySnapshot(row)
	}
	return out, nil
}

// ListTrinoQuerySnapshotsByUser pages one user's snapshots.
func (r *Repo) ListTrinoQuerySnapshotsByUser(ctx context.Context, arg db.ListTrinoQuerySnapshotsByUserParams) ([]db.TrinoQuerySnapshot, error) {
	var rows []model.TrinoQuerySnapshot
	if err := r.Users.db.WithContext(ctx).
		Where(`"user" = ?`, arg.User).
		Order("created_at DESC").
		Limit(int(arg.Limit)).Offset(int(arg.Offset)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.TrinoQuerySnapshot, len(rows))
	for i, row := range rows {
		out[i] = toDBTrinoQuerySnapshot(row)
	}
	return out, nil
}

// GetTableHealthSnapshot returns one table's health row.
func (r *Repo) GetTableHealthSnapshot(ctx context.Context, arg db.GetTableHealthSnapshotParams) (db.TableHealthSnapshot, error) {
	var row model.TableHealthSnapshot
	err := r.Users.db.WithContext(ctx).
		Where("catalog_connection_id = ? AND catalog = ? AND namespace = ? AND table_name = ?",
			UUIDString(arg.CatalogConnectionID), string(arg.Catalog), string(arg.Namespace), string(arg.TableName)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.TableHealthSnapshot{}, db.ErrNoRows
	}
	if err != nil {
		return db.TableHealthSnapshot{}, err
	}
	return toDBTableHealthSnapshot(row), nil
}

// ListTableHealthSnapshots pages table health, worst score first.
func (r *Repo) ListTableHealthSnapshots(ctx context.Context, arg db.ListTableHealthSnapshotsParams) ([]db.TableHealthSnapshot, error) {
	var rows []model.TableHealthSnapshot
	if err := r.Users.db.WithContext(ctx).
		Order("health_score ASC").
		Limit(int(arg.Limit)).Offset(int(arg.Offset)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.TableHealthSnapshot, len(rows))
	for i, row := range rows {
		out[i] = toDBTableHealthSnapshot(row)
	}
	return out, nil
}

// ListTableHealthByConnection pages one connection's table health.
func (r *Repo) ListTableHealthByConnection(ctx context.Context, arg db.ListTableHealthByConnectionParams) ([]db.TableHealthSnapshot, error) {
	var rows []model.TableHealthSnapshot
	if err := r.Users.db.WithContext(ctx).
		Where("catalog_connection_id = ?", UUIDString(arg.CatalogConnectionID)).
		Order("health_score ASC").
		Limit(int(arg.Limit)).Offset(int(arg.Offset)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.TableHealthSnapshot, len(rows))
	for i, row := range rows {
		out[i] = toDBTableHealthSnapshot(row)
	}
	return out, nil
}

// ListLastMaintenanceRuns returns, per operation, the last finished run of a
// table.
func (r *Repo) ListLastMaintenanceRuns(ctx context.Context, arg db.ListLastMaintenanceRunsParams) ([]db.ListLastMaintenanceRunsRow, error) {
	var runs []model.MaintenanceRun
	if err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND namespace = ? AND table_name = ? AND status = ? AND finished_at IS NOT NULL",
			UUIDString(arg.CatalogID), string(arg.Namespace), string(arg.TableName), "finished").
		Order("finished_at DESC").
		Find(&runs).Error; err != nil {
		return nil, err
	}
	latest := make(map[string]model.MaintenanceRun)
	for _, run := range runs {
		for _, op := range run.Operations {
			if _, ok := latest[op]; !ok {
				latest[op] = run
			}
		}
	}
	ops := make([]string, 0, len(latest))
	for op := range latest {
		ops = append(ops, op)
	}
	sort.Strings(ops)
	out := make([]db.ListLastMaintenanceRunsRow, 0, len(ops))
	for _, op := range ops {
		run := latest[op]
		out = append(out, db.ListLastMaintenanceRunsRow{
			Operation:  op,
			ID:         PGUUID(string(run.ID)),
			Status:     run.Status,
			FinishedAt: PGTimePtr(run.FinishedAt),
			WorkflowID: run.WorkflowID,
		})
	}
	return out, nil
}

// ── retrievers ───────────────────────────────────────────────────────────────

// GetRetrieverForRefresh returns a retriever without a workspace filter: the
// refresh activity runs off the due list.
func (r *Repo) GetRetrieverForRefresh(ctx context.Context, id db.UUID) (db.Retriever, error) {
	var row model.Retriever
	err := r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Retriever{}, db.ErrNoRows
	}
	if err != nil {
		return db.Retriever{}, err
	}
	return toDBRetriever(row), nil
}

// ListRetrieversDue returns the active retrievers whose schedule elapsed.
func (r *Repo) ListRetrieversDue(ctx context.Context) ([]db.Retriever, error) {
	var rows []model.Retriever
	if err := r.Users.db.WithContext(ctx).
		Where("status = ? AND next_refresh_at <= ?", "active", time.Now().UTC()).
		Order("next_refresh_at").
		Limit(50).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.Retriever, len(rows))
	for i, row := range rows {
		out[i] = toDBRetriever(row)
	}
	return out, nil
}

// AdvanceRetrieverSchedule moves the retriever's next fire time.
func (r *Repo) AdvanceRetrieverSchedule(ctx context.Context, arg db.AdvanceRetrieverScheduleParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.Retriever{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"next_refresh_at": TimePtr(arg.NextRefreshAt),
			"updated_at":      time.Now().UTC(),
		}).Error
}

// UpdateRetrieverAfterRefresh records the outcome of a refresh.
func (r *Repo) UpdateRetrieverAfterRefresh(ctx context.Context, arg db.UpdateRetrieverAfterRefreshParams) error {
	now := time.Now().UTC()
	return r.Users.db.WithContext(ctx).Model(&model.Retriever{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"status":          arg.Status,
			"error_code":      arg.ErrorCode,
			"last_refresh_at": now,
			"updated_at":      now,
		}).Error
}

// CreateRetrieverRefreshHistory opens a refresh history row.
func (r *Repo) CreateRetrieverRefreshHistory(ctx context.Context, arg db.CreateRetrieverRefreshHistoryParams) (db.RetrieverRefreshHistory, error) {
	row := model.RetrieverRefreshHistory{
		ID:                 model.UUID(NewUUID()),
		RetrieverID:        model.UUID(UUIDString(arg.RetrieverID)),
		TemporalWorkflowID: arg.TemporalWorkflowID,
		RefreshType:        arg.RefreshType,
		Status:             model.Text(arg.Status),
		StartedAt:          time.Now().UTC(),
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.RetrieverRefreshHistory{}, err
	}
	return toDBRetrieverRefreshHistory(row), nil
}

// UpdateRetrieverRefreshHistory closes a refresh history row.
func (r *Repo) UpdateRetrieverRefreshHistory(ctx context.Context, arg db.UpdateRetrieverRefreshHistoryParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.RetrieverRefreshHistory{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"status":         arg.Status,
			"rows_processed": arg.RowsProcessed,
			"duration_ms":    arg.DurationMs,
			"error_message":  arg.ErrorMessage,
			"error_code":     arg.ErrorCode,
			"finished_at":    time.Now().UTC(),
		}).Error
}

// ── migrations ───────────────────────────────────────────────────────────────

// CreateMigration inserts a migration job.
func (r *Repo) CreateMigration(ctx context.Context, arg db.CreateMigrationParams) (db.Migration, error) {
	row := model.Migration{
		ID:                        model.UUID(NewUUID()),
		CreatedBy:                 model.UUID(UUIDString(arg.CreatedBy)),
		Name:                      arg.Name,
		SourceType:                arg.SourceType,
		SourceURL:                 arg.SourceUrl,
		SourceCredentialsEnc:      arg.SourceCredentialsEnc,
		TargetCatalogConnectionID: model.UUID(UUIDString(arg.TargetCatalogConnectionID)),
		Status:                    model.Text("pending"),
		CreatedAt:                 time.Now().UTC(),
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.Migration{}, err
	}
	return toDBMigration(row), nil
}

// GetMigrationByID returns one migration job.
func (r *Repo) GetMigrationByID(ctx context.Context, id db.UUID) (db.Migration, error) {
	var row model.Migration
	err := r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Migration{}, db.ErrNoRows
	}
	if err != nil {
		return db.Migration{}, err
	}
	return toDBMigration(row), nil
}

// ListMigrations lists the migration jobs, newest first.
func (r *Repo) ListMigrations(ctx context.Context) ([]db.Migration, error) {
	var rows []model.Migration
	if err := r.Users.db.WithContext(ctx).Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.Migration, len(rows))
	for i, row := range rows {
		out[i] = toDBMigration(row)
	}
	return out, nil
}

// UpdateMigrationStatus transitions a migration job.
func (r *Repo) UpdateMigrationStatus(ctx context.Context, arg db.UpdateMigrationStatusParams) error {
	now := time.Now().UTC()
	changes := map[string]any{"status": arg.Status, "temporal_workflow_id": arg.TemporalWorkflowID}
	if arg.Status == "running" {
		changes["started_at"] = now
	}
	if arg.Status == "finished" || arg.Status == "failed" || arg.Status == "cancelled" {
		changes["finished_at"] = now
	}
	return r.Users.db.WithContext(ctx).Model(&model.Migration{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(changes).Error
}

// UpdateMigrationProgress stores the migration's table counters.
func (r *Repo) UpdateMigrationProgress(ctx context.Context, arg db.UpdateMigrationProgressParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.Migration{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"migrated_tables": arg.MigratedTables,
			"failed_tables":   arg.FailedTables,
		}).Error
}

// CreateMigrationItem inserts one table of a migration job.
func (r *Repo) CreateMigrationItem(ctx context.Context, arg db.CreateMigrationItemParams) (db.MigrationItem, error) {
	row := model.MigrationItem{
		ID:              model.UUID(NewUUID()),
		MigrationID:     model.UUID(UUIDString(arg.MigrationID)),
		SourceNamespace: arg.SourceNamespace,
		SourceTable:     arg.SourceTable,
		TargetNamespace: arg.TargetNamespace,
		TargetTable:     arg.TargetTable,
		Status:          model.Text(arg.Status),
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.MigrationItem{}, err
	}
	return toDBMigrationItem(row), nil
}

// UpdateMigrationItem transitions one table of a migration job.
func (r *Repo) UpdateMigrationItem(ctx context.Context, arg db.UpdateMigrationItemParams) error {
	now := time.Now().UTC()
	changes := map[string]any{"status": arg.Status, "error_message": arg.ErrorMessage}
	if arg.Status == "running" {
		changes["started_at"] = now
	}
	if arg.Status == "finished" || arg.Status == "failed" || arg.Status == "skipped" {
		changes["finished_at"] = now
	}
	return r.Users.db.WithContext(ctx).Model(&model.MigrationItem{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(changes).Error
}

// ListMigrationItems lists a migration's tables in source order.
func (r *Repo) ListMigrationItems(ctx context.Context, migrationID db.UUID) ([]db.MigrationItem, error) {
	var rows []model.MigrationItem
	if err := r.Users.db.WithContext(ctx).
		Where("migration_id = ?", UUIDString(migrationID)).
		Order("source_namespace, source_table").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.MigrationItem, len(rows))
	for i, row := range rows {
		out[i] = toDBMigrationItem(row)
	}
	return out, nil
}

// ListPendingMigrationItems lists a migration's still-pending tables.
func (r *Repo) ListPendingMigrationItems(ctx context.Context, migrationID db.UUID) ([]db.MigrationItem, error) {
	var rows []model.MigrationItem
	if err := r.Users.db.WithContext(ctx).
		Where("migration_id = ? AND status = ?", UUIDString(migrationID), "pending").
		Order("source_namespace, source_table").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.MigrationItem, len(rows))
	for i, row := range rows {
		out[i] = toDBMigrationItem(row)
	}
	return out, nil
}

func toDBMigration(m model.Migration) db.Migration {
	return db.Migration{
		ID:                        PGUUID(string(m.ID)),
		CreatedBy:                 PGUUID(string(m.CreatedBy)),
		Name:                      m.Name,
		SourceType:                m.SourceType,
		SourceUrl:                 m.SourceURL,
		SourceCredentialsEnc:      m.SourceCredentialsEnc,
		TargetCatalogConnectionID: PGUUID(string(m.TargetCatalogConnectionID)),
		Status:                    string(m.Status),
		TotalTables:               m.TotalTables,
		MigratedTables:            m.MigratedTables,
		FailedTables:              m.FailedTables,
		TemporalWorkflowID:        m.TemporalWorkflowID,
		StartedAt:                 PGTimePtr(m.StartedAt),
		FinishedAt:                PGTimePtr(m.FinishedAt),
		CreatedAt:                 PGTime(m.CreatedAt),
	}
}

func toDBMigrationItem(m model.MigrationItem) db.MigrationItem {
	return db.MigrationItem{
		ID:              PGUUID(string(m.ID)),
		MigrationID:     PGUUID(string(m.MigrationID)),
		SourceNamespace: m.SourceNamespace,
		SourceTable:     m.SourceTable,
		TargetNamespace: m.TargetNamespace,
		TargetTable:     m.TargetTable,
		Status:          string(m.Status),
		ErrorMessage:    m.ErrorMessage,
		StartedAt:       PGTimePtr(m.StartedAt),
		FinishedAt:      PGTimePtr(m.FinishedAt),
	}
}

// ── service accounts ─────────────────────────────────────────────────────────

// CreateServiceAccount inserts a non-human principal.
func (r *Repo) CreateServiceAccount(ctx context.Context, arg db.CreateServiceAccountParams) (db.User, error) {
	return NewServiceAccounts(r.Users.db).CreateServiceAccount(ctx, arg)
}

// UpdateServiceAccount changes a non-human principal.
func (r *Repo) UpdateServiceAccount(ctx context.Context, arg db.UpdateServiceAccountParams) (db.User, error) {
	return NewServiceAccounts(r.Users.db).UpdateServiceAccount(ctx, arg)
}

// DeleteServiceAccount removes a non-human principal.
func (r *Repo) DeleteServiceAccount(ctx context.Context, id db.UUID) (int64, error) {
	return NewServiceAccounts(r.Users.db).DeleteServiceAccount(ctx, id)
}

// ListServiceAccounts lists every service account.
func (r *Repo) ListServiceAccounts(ctx context.Context) ([]db.User, error) {
	return NewServiceAccounts(r.Users.db).ListServiceAccounts(ctx)
}

// ListServiceAccountsOfWorkspaces lists the accounts of the given workspaces.
func (r *Repo) ListServiceAccountsOfWorkspaces(ctx context.Context, workspaceIDs []db.UUID) ([]db.User, error) {
	return NewServiceAccounts(r.Users.db).ListServiceAccountsOfWorkspaces(ctx, workspaceIDs)
}

// ListAdminWorkspaceIDs lists the workspaces a service account administers.
func (r *Repo) ListAdminWorkspaceIDs(ctx context.Context, userID db.UUID) ([]db.UUID, error) {
	return NewServiceAccounts(r.Users.db).ListAdminWorkspaceIDs(ctx, userID)
}

// ── metadata and statements ──────────────────────────────────────────────────

// ListSyncableCatalogs returns the catalogs the scheduled sync refreshes.
func (r *Repo) ListSyncableCatalogs(ctx context.Context) ([]db.UUID, error) {
	var catalogs []model.Catalog
	if err := r.Users.db.WithContext(ctx).
		Where("enabled").
		Order("id").
		Find(&catalogs).Error; err != nil {
		return nil, err
	}
	var bound []model.UUID
	if err := r.Users.db.WithContext(ctx).Model(&model.CatalogBinding{}).
		Where("enabled").Distinct().Pluck("catalog_id", &bound).Error; err != nil {
		return nil, err
	}
	withBinding := make(map[string]bool, len(bound))
	for _, id := range bound {
		withBinding[string(id)] = true
	}
	out := make([]db.UUID, 0, len(catalogs))
	for _, c := range catalogs {
		if string(c.Origin) == "source" || withBinding[string(c.ID)] {
			out = append(out, PGUUID(string(c.ID)))
		}
	}
	sort.Slice(out, func(i, j int) bool { return UUIDString(out[i]) < UUIDString(out[j]) })
	return out, nil
}

// MaxMetadataStalenessSeconds is the age of the stalest syncable catalog.
func (r *Repo) MaxMetadataStalenessSeconds(ctx context.Context) (float64, error) {
	ids, err := r.ListSyncableCatalogs(ctx)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	catalogIDs := make([]string, len(ids))
	for i, id := range ids {
		catalogIDs[i] = UUIDString(id)
	}
	var catalogs []model.Catalog
	if err := r.Users.db.WithContext(ctx).Where("id IN ?", catalogIDs).Find(&catalogs).Error; err != nil {
		return 0, err
	}
	lastByCatalog := make(map[string]time.Time, len(catalogs))
	for _, c := range catalogs {
		lastByCatalog[string(c.ID)] = c.CreatedAt
	}
	var runs []model.MetadataSyncRun
	if err := r.Users.db.WithContext(ctx).
		Where("catalog_id IN ? AND status = ? AND finished_at IS NOT NULL", catalogIDs, "finished").
		Order("finished_at DESC").
		Find(&runs).Error; err != nil {
		return 0, err
	}
	// The first run of a catalog (runs are ordered by finished_at DESC) is its
	// latest finished run and wins over the catalog's created_at, mirroring
	// the previous per-catalog lookup.
	finished := make(map[string]bool, len(catalogs))
	for _, run := range runs {
		if run.FinishedAt == nil {
			continue
		}
		key := string(run.CatalogID)
		if _, ok := lastByCatalog[key]; !ok || finished[key] {
			continue
		}
		lastByCatalog[key] = *run.FinishedAt
		finished[key] = true
	}
	now := time.Now().UTC()
	var maxAge float64
	for _, last := range lastByCatalog {
		if age := now.Sub(last).Seconds(); age > maxAge {
			maxAge = age
		}
	}
	return maxAge, nil
}

// ListStatementsForReferenceBackfill pages finished statements for the one-off
// reference backfill.
func (r *Repo) ListStatementsForReferenceBackfill(ctx context.Context, arg db.ListStatementsForReferenceBackfillParams) ([]db.ListStatementsForReferenceBackfillRow, error) {
	q := r.Users.db.WithContext(ctx).
		Table("query_statements AS qs").
		Select(`qs.id AS id, qs.sql AS sql, qe.workspace_id AS workspace_id,
			qe.target_catalog_id AS target_catalog_id, COALESCE(qe.target_schema, '') AS target_schema`).
		Joins("JOIN query_executions qe ON qe.id = qs.execution_id").
		Where("qs.status = ?", "finished")
	if arg.Cursor.Valid {
		q = q.Where("qs.id > ?", UUIDString(arg.Cursor))
	}
	var rows []db.ListStatementsForReferenceBackfillRow
	if err := q.Order("qs.id").Limit(int(arg.Lim)).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// TryMetadataSyncLock is retained for the sqlc-shaped surface. ADR 0020
// replaces the advisory lock with the row lease in BeginMetadataSyncRun, so
// this reports success and leaves serialization to the lease.
func (r *Repo) TryMetadataSyncLock(ctx context.Context, catalogID string) (bool, error) {
	return true, nil
}

// ReleaseMetadataSyncLock pairs with TryMetadataSyncLock and is a no-op.
func (r *Repo) ReleaseMetadataSyncLock(ctx context.Context, catalogID string) error {
	return nil
}

// EnsureAuditPartitions creates the daily partitions of the audit trail and of
// the query history from today through daysAhead days ahead (ADR 0026). It never drops one, and it is idempotent. A table that is still
// plain (migration 14 has not run, or the engine has no adapter) is left alone:
// provisioning would convert it, and converting belongs to the migration.
func (r *Repo) EnsureAuditPartitions(ctx context.Context, daysAhead int) error {
	pm, err := r.partitionManager()
	if err != nil {
		return err
	}
	if pm == nil {
		return nil
	}
	from := partitioning.Daily.Start(time.Now())
	until := from.AddDate(0, 0, daysAhead+1)
	for _, t := range model.PartitionedTables() {
		existing, err := pm.ListPartitions(ctx, t.Table)
		if err != nil {
			return err
		}
		if len(existing) == 0 {
			return fmt.Errorf("%s is not partitioned: run the store migrations", t.Table)
		}
		spec := partitioning.Spec{Table: t.Table, Column: t.Column, Strategy: partitioning.Range, Interval: partitioning.Daily}
		if err := pm.Provision(ctx, t.Model, spec, from, until); err != nil {
			return fmt.Errorf("provision %s: %w", t.Table, err)
		}
	}
	return nil
}

// Partitions returns the existing partitions of table (ADR 0024).
func (r *Repo) Partitions(ctx context.Context, table string) ([]partitioning.Partition, error) {
	pm, err := r.partitionManager()
	if err != nil || pm == nil {
		return nil, err
	}
	return pm.ListPartitions(ctx, table)
}

// DropPartitions drops the partitions of table covering [from, until) (ADR 0024).
func (r *Repo) DropPartitions(ctx context.Context, table string, from, until time.Time) error {
	pm, err := r.partitionManager()
	if err != nil || pm == nil {
		return err
	}
	return pm.DropPartition(ctx, table, from, until)
}

// partitionManager resolves the adapter, or (nil, nil) when the backend has no
// partitioning.
func (r *Repo) partitionManager() (partitioning.Manager, error) {
	if !partitioning.CapabilitiesFor(r.Users.db).Implemented {
		return nil, nil
	}
	pm, err := partitioning.New(r.Users.db)
	if err != nil {
		if errors.Is(err, partitioning.ErrNotImplemented) || errors.Is(err, partitioning.ErrUnsupportedPartitioning) {
			return nil, nil
		}
		return nil, err
	}
	return pm, nil
}
