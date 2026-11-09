package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/agents/manage"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TxAgents runs fn in one transaction over the agent store.
func (r *Repo) TxAgents(ctx context.Context, fn func(manage.TxStore) error) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}

// ListAllAgents returns the external agents, newest first.
func (r *Repo) ListAllAgents(ctx context.Context) ([]db.Agent, error) {
	var rows []model.Agent
	if err := r.Users.db.WithContext(ctx).Where("kind = ?", "external").Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.Agent, len(rows))
	for i, row := range rows {
		out[i] = toDBAgent(row)
	}
	return out, nil
}

// ListAgentsByOwner returns the owner's external agents, newest first.
func (r *Repo) ListAgentsByOwner(ctx context.Context, ownerID db.UUID) ([]db.Agent, error) {
	var rows []model.Agent
	if err := r.Users.db.WithContext(ctx).
		Where("owner_id = ? AND kind = ?", UUIDString(ownerID), "external").
		Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.Agent, len(rows))
	for i, row := range rows {
		out[i] = toDBAgent(row)
	}
	return out, nil
}

// ListAgentWorkspaces returns the workspaces the agent is bound to, by name.
func (r *Repo) ListAgentWorkspaces(ctx context.Context, agentID db.UUID) ([]db.Workspace, error) {
	var rows []model.Workspace
	if err := r.Users.db.WithContext(ctx).
		Joins("JOIN agent_workspaces aw ON aw.workspace_id = workspaces.id").
		Where("aw.agent_id = ?", UUIDString(agentID)).
		Order("workspaces.name").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.Workspace, len(rows))
	for i, row := range rows {
		out[i] = toDBWorkspace(row)
	}
	return out, nil
}

// ListAgentTokensByAgent returns the agent's tokens, newest first.
func (r *Repo) ListAgentTokensByAgent(ctx context.Context, agentID db.UUID) ([]db.AgentToken, error) {
	var rows []model.AgentToken
	if err := r.Users.db.WithContext(ctx).
		Where("agent_id = ?", UUIDString(agentID)).
		Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.AgentToken, len(rows))
	for i, row := range rows {
		out[i] = toDBAgentToken(row)
	}
	return out, nil
}

// GetAgentTokenByID returns one of the agent's tokens or db.ErrNoRows.
func (r *Repo) GetAgentTokenByID(ctx context.Context, arg db.GetAgentTokenByIDParams) (db.AgentToken, error) {
	var row model.AgentToken
	err := r.Users.db.WithContext(ctx).
		Where("id = ? AND agent_id = ?", UUIDString(arg.ID), UUIDString(arg.AgentID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.AgentToken{}, db.ErrNoRows
	}
	if err != nil {
		return db.AgentToken{}, err
	}
	return toDBAgentToken(row), nil
}

// ListBindingsByCatalog returns the bindings of a catalog, oldest first.
func (r *Repo) ListBindingsByCatalog(ctx context.Context, catalogID db.UUID) ([]db.CatalogBinding, error) {
	var rows []model.CatalogBinding
	if err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ?", UUIDString(catalogID)).
		Order("created_at").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.CatalogBinding, len(rows))
	for i, row := range rows {
		out[i] = toDBCatalogBinding(row)
	}
	return out, nil
}

// GetQueryExecutionByIDAndWorkspace returns a workspace-scoped execution or
// db.ErrNoRows.
func (r *Repo) GetQueryExecutionByIDAndWorkspace(ctx context.Context, arg db.GetQueryExecutionByIDAndWorkspaceParams) (db.QueryExecution, error) {
	var row model.QueryExecution
	err := r.Users.db.WithContext(ctx).
		Where("id = ? AND workspace_id = ?", UUIDString(arg.ID), UUIDString(arg.WorkspaceID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.QueryExecution{}, db.ErrNoRows
	}
	if err != nil {
		return db.QueryExecution{}, err
	}
	return toDBQueryExecution(row), nil
}

// CreateAgent inserts an external agent.
func (r *Repo) CreateAgent(ctx context.Context, arg db.CreateAgentParams) (db.Agent, error) {
	now := time.Now().UTC()
	row := model.Agent{
		ID:              model.UUID(NewUUID()),
		OwnerID:         model.UUID(UUIDString(arg.OwnerID)),
		Kind:            model.Text(arg.Kind),
		Name:            arg.Name,
		Description:     model.Text(arg.Description),
		Status:          "active",
		CreatedAt:       now,
		UpdatedAt:       now,
		Policy:          model.JSON("{}"),
		PolicyUpdatedAt: now,
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.Agent{}, err
	}
	return toDBAgent(row), nil
}

// UpdateAgent updates the name and description.
func (r *Repo) UpdateAgent(ctx context.Context, arg db.UpdateAgentParams) (db.Agent, error) {
	if err := r.Users.db.WithContext(ctx).Model(&model.Agent{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{"name": arg.Name, "description": arg.Description, "updated_at": time.Now().UTC()}).Error; err != nil {
		return db.Agent{}, err
	}
	return r.GetAgentByID(ctx, arg.ID)
}

// SetAgentStatus changes the agent's status.
func (r *Repo) SetAgentStatus(ctx context.Context, arg db.SetAgentStatusParams) (db.Agent, error) {
	if err := r.Users.db.WithContext(ctx).Model(&model.Agent{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{"status": arg.Status, "updated_at": time.Now().UTC()}).Error; err != nil {
		return db.Agent{}, err
	}
	return r.GetAgentByID(ctx, arg.ID)
}

// DeleteAgent removes the agent.
func (r *Repo) DeleteAgent(ctx context.Context, id db.UUID) error {
	return r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).Delete(&model.Agent{}).Error
}

// AddAgentWorkspace binds the agent to the workspace.
func (r *Repo) AddAgentWorkspace(ctx context.Context, arg db.AddAgentWorkspaceParams) error {
	row := model.AgentWorkspace{
		AgentID:     model.UUID(UUIDString(arg.AgentID)),
		WorkspaceID: model.UUID(UUIDString(arg.WorkspaceID)),
	}
	return r.Users.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

// ClearAgentWorkspaces removes every binding of the agent.
func (r *Repo) ClearAgentWorkspaces(ctx context.Context, agentID db.UUID) error {
	return r.Users.db.WithContext(ctx).Where("agent_id = ?", UUIDString(agentID)).Delete(&model.AgentWorkspace{}).Error
}

// CreateAgentToken inserts a token.
func (r *Repo) CreateAgentToken(ctx context.Context, arg db.CreateAgentTokenParams) (db.AgentToken, error) {
	row := model.AgentToken{
		ID:        model.UUID(NewUUID()),
		AgentID:   model.UUID(UUIDString(arg.AgentID)),
		Name:      arg.Name,
		TokenHash: model.Text(arg.TokenHash),
		Prefix:    arg.Prefix,
		ExpiresAt: arg.ExpiresAt.Time,
		CreatedBy: model.UUID(UUIDString(arg.CreatedBy)),
		CreatedAt: time.Now().UTC(),
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.AgentToken{}, err
	}
	return toDBAgentToken(row), nil
}

// RevokeAgentToken revokes a live token of the agent.
func (r *Repo) RevokeAgentToken(ctx context.Context, arg db.RevokeAgentTokenParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.AgentToken{}).
		Where("id = ? AND agent_id = ? AND revoked_at IS NULL", UUIDString(arg.ID), UUIDString(arg.AgentID)).
		Update("revoked_at", time.Now().UTC()).Error
}

// UpdateAgentPolicy replaces the agent's policy.
func (r *Repo) UpdateAgentPolicy(ctx context.Context, arg db.UpdateAgentPolicyParams) (db.Agent, error) {
	if err := r.Users.db.WithContext(ctx).Model(&model.Agent{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"policy":            model.JSON(arg.Policy),
			"policy_updated_at": time.Now().UTC(),
			"policy_updated_by": modelUUIDPtr(arg.PolicyUpdatedBy),
		}).Error; err != nil {
		return db.Agent{}, err
	}
	return r.GetAgentByID(ctx, arg.ID)
}

func toDBCatalogBinding(b model.CatalogBinding) db.CatalogBinding {
	return db.CatalogBinding{
		ID:                PGUUID(string(b.ID)),
		CatalogID:         PGUUID(string(b.CatalogID)),
		EngineID:          PGUUID(string(b.EngineID)),
		EngineCatalogName: string(b.EngineCatalogName),
		Connector:         b.Connector,
		ExtraProps:        []byte(b.ExtraProps),
		Mode:              b.Mode,
		Enabled:           b.Enabled,
		CreatedAt:         PGTime(b.CreatedAt),
		UpdatedAt:         PGTime(b.UpdatedAt),
		LifecycleState:    string(b.LifecycleState),
		DesiredVersion:    b.DesiredVersion,
		LastErrorCode:     b.LastErrorCode,
		LastErrorMessage:  b.LastErrorMessage,
		StateChangedAt:    PGTime(b.StateChangedAt),
	}
}

func toDBQueryExecution(e model.QueryExecution) db.QueryExecution {
	return db.QueryExecution{
		ID:              PGUUID(string(e.ID)),
		WorkspaceID:     PGUUID(string(e.WorkspaceID)),
		UserID:          PGUUID(string(e.UserID)),
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
		TargetSnapshot:  jsonBytes(e.TargetSnapshot),
		AgentID:         uuidPtr(e.AgentID),
		QueueClass:      e.QueueClass,
		QueueReason:     e.QueueReason,
		FairnessKey:     e.FairnessKey,
		QueuedAt:        PGTimePtr(e.QueuedAt),
		AdmittedAt:      PGTimePtr(e.AdmittedAt),
		QueueWaitMs:     e.QueueWaitMs,
		ErrorCode:       e.ErrorCode,
	}
}

func jsonBytes(j *model.JSON) []byte {
	if j == nil {
		return nil
	}
	return []byte(*j)
}
