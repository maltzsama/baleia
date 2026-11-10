package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	wsmanage "github.com/galileostudio/baleia/internal/workspaces/manage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TxWorkspace runs fn in one transaction over the workspace store.
func (r *Repo) TxWorkspace(ctx context.Context, fn func(wsmanage.TxStore) error) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}

// ListWorkspaceCatalogGrants returns the catalogs granted to the workspace.
func (r *Repo) ListWorkspaceCatalogGrants(ctx context.Context, workspaceID db.UUID) ([]db.ListWorkspaceCatalogGrantsRow, error) {
	var rows []struct {
		WorkspaceID model.UUID
		CatalogID   model.UUID
		GrantedAt   time.Time
		CatalogName string
	}
	err := r.Users.db.WithContext(ctx).
		Table("workspace_catalog_grants g").
		Select("g.workspace_id, g.catalog_id, g.granted_at, COALESCE(c.trino_catalog_name, c.name) AS catalog_name").
		Joins("JOIN catalogs c ON c.id = g.catalog_id").
		Where("g.workspace_id = ?", UUIDString(workspaceID)).
		Order("catalog_name").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]db.ListWorkspaceCatalogGrantsRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListWorkspaceCatalogGrantsRow{
			WorkspaceID: PGUUID(string(row.WorkspaceID)),
			CatalogID:   PGUUID(string(row.CatalogID)),
			GrantedAt:   PGTime(row.GrantedAt),
			CatalogName: row.CatalogName,
		}
	}
	return out, nil
}

// CountWorkspaceRunningExecutions counts the workspace's running executions.
func (r *Repo) CountWorkspaceRunningExecutions(ctx context.Context, workspaceID db.UUID) (int64, error) {
	var count int64
	err := r.Users.db.WithContext(ctx).Model(&model.QueryExecution{}).
		Where("workspace_id = ? AND status = ?", UUIDString(workspaceID), "running").
		Count(&count).Error
	return count, err
}

// CountWorkspaceImpact reports what deleting the workspace would affect.
func (r *Repo) CountWorkspaceImpact(ctx context.Context, workspaceID db.UUID) (db.CountWorkspaceImpactRow, error) {
	q := r.Users.db.WithContext(ctx)
	ws := UUIDString(workspaceID)
	var out db.CountWorkspaceImpactRow
	for _, c := range []struct {
		model any
		where string
		dst   *int64
	}{
		{&model.SavedQuery{}, "workspace_id = ?", &out.SavedQueries},
		{&model.SemanticMetric{}, "workspace_id = ?", &out.Metrics},
		{&model.Space{}, "workspace_id = ?", &out.Spaces},
		{&model.QueryExecution{}, "workspace_id = ? AND status = 'running'", &out.RunningExecutions},
	} {
		if err := q.Model(c.model).Where(c.where, ws).Count(c.dst).Error; err != nil {
			return db.CountWorkspaceImpactRow{}, err
		}
	}
	var wsRow model.Workspace
	if err := q.Select("default_binding_id").Where("id = ?", ws).First(&wsRow).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return db.CountWorkspaceImpactRow{}, err
	}
	out.DefaultTargetAffected = wsRow.DefaultBindingID != nil
	return out, nil
}

// CountCatalogAccessImpact reports what revoking the catalog grant would affect.
func (r *Repo) CountCatalogAccessImpact(ctx context.Context, arg db.CountCatalogAccessImpactParams) (db.CountCatalogAccessImpactRow, error) {
	q := r.Users.db.WithContext(ctx)
	ws, cat := UUIDString(arg.WorkspaceID), UUIDString(arg.CatalogID)
	var out db.CountCatalogAccessImpactRow
	if err := q.Model(&model.SavedQuery{}).
		Joins("JOIN catalog_bindings b ON b.id = saved_queries.target_binding_id").
		Where("saved_queries.workspace_id = ? AND b.catalog_id = ?", ws, cat).
		Count(&out.SavedQueries).Error; err != nil {
		return db.CountCatalogAccessImpactRow{}, err
	}
	if err := q.Model(&model.SemanticMetric{}).
		Where("workspace_id = ? AND catalog_id = ?", ws, cat).
		Count(&out.Metrics).Error; err != nil {
		return db.CountCatalogAccessImpactRow{}, err
	}
	if err := q.Model(&model.QueryExecution{}).
		Joins("JOIN catalog_bindings b ON b.id = query_executions.target_binding_id").
		Where("query_executions.workspace_id = ? AND b.catalog_id = ? AND query_executions.status = 'running'", ws, cat).
		Count(&out.RunningExecutions).Error; err != nil {
		return db.CountCatalogAccessImpactRow{}, err
	}
	var count int64
	if err := q.Model(&model.Workspace{}).
		Joins("JOIN catalog_bindings b ON b.id = workspaces.default_binding_id").
		Where("workspaces.id = ? AND b.catalog_id = ?", ws, cat).
		Count(&count).Error; err != nil {
		return db.CountCatalogAccessImpactRow{}, err
	}
	out.DefaultTargetAffected = count > 0
	return out, nil
}

// DeleteWorkspace deletes a shared workspace and returns the affected rows.
func (r *Repo) DeleteWorkspace(ctx context.Context, id db.UUID) (int64, error) {
	res := r.Users.db.WithContext(ctx).
		Where("id = ? AND type = ?", UUIDString(id), "shared").
		Delete(&model.Workspace{})
	return res.RowsAffected, res.Error
}

// UpsertWorkspaceCatalogGrant grants a catalog to the workspace.
func (r *Repo) UpsertWorkspaceCatalogGrant(ctx context.Context, arg db.UpsertWorkspaceCatalogGrantParams) (db.WorkspaceCatalogGrant, error) {
	now := time.Now().UTC()
	row := model.WorkspaceCatalogGrant{
		WorkspaceID: model.UUID(UUIDString(arg.WorkspaceID)),
		CatalogID:   model.UUID(UUIDString(arg.CatalogID)),
		GrantedBy:   model.UUID(UUIDString(arg.GrantedBy)),
		GrantedAt:   now,
	}
	res := r.Users.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "workspace_id"}, {Name: "catalog_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"granted_by", "granted_at"}),
	}).Create(&row)
	if res.Error != nil {
		return db.WorkspaceCatalogGrant{}, res.Error
	}
	return db.WorkspaceCatalogGrant{
		WorkspaceID: PGUUID(string(row.WorkspaceID)),
		CatalogID:   PGUUID(string(row.CatalogID)),
		GrantedBy:   PGUUID(string(row.GrantedBy)),
		GrantedAt:   PGTime(row.GrantedAt),
	}, nil
}

// UpdateWorkspace updates the name and description and returns the workspace.
func (r *Repo) UpdateWorkspace(ctx context.Context, arg db.UpdateWorkspaceParams) (db.Workspace, error) {
	changes := map[string]any{"name": arg.Name, "description": arg.Description, "updated_at": time.Now().UTC()}
	if arg.BIEnabled != nil {
		changes["bi_enabled"] = *arg.BIEnabled
	}
	if err := r.Users.db.WithContext(ctx).Model(&model.Workspace{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(changes).Error; err != nil {
		return db.Workspace{}, err
	}
	return r.GetWorkspaceByID(ctx, arg.ID)
}

// SetWorkspaceDefaultTarget sets the workspace's default binding and schema.
func (r *Repo) SetWorkspaceDefaultTarget(ctx context.Context, arg db.SetWorkspaceDefaultTargetParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.Workspace{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"default_binding_id": modelUUIDPtr(arg.DefaultBindingID),
			"default_schema":     arg.DefaultSchema,
			"updated_at":         time.Now().UTC(),
		}).Error
}

// DeleteWorkspaceCatalogGrant revokes a catalog grant and returns the rows.
func (r *Repo) DeleteWorkspaceCatalogGrant(ctx context.Context, arg db.DeleteWorkspaceCatalogGrantParams) (int64, error) {
	res := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND catalog_id = ?", UUIDString(arg.WorkspaceID), UUIDString(arg.CatalogID)).
		Delete(&model.WorkspaceCatalogGrant{})
	return res.RowsAffected, res.Error
}

// ClearWorkspaceDefaultForCatalog clears a default target that points at the
// catalog being revoked.
func (r *Repo) ClearWorkspaceDefaultForCatalog(ctx context.Context, arg db.ClearWorkspaceDefaultForCatalogParams) error {
	bindings := r.Users.db.Model(&model.CatalogBinding{}).Select("id").Where("catalog_id = ?", UUIDString(arg.CatalogID))
	return r.Users.db.WithContext(ctx).Model(&model.Workspace{}).
		Where("id = ? AND default_binding_id IN (?)", UUIDString(arg.WorkspaceID), bindings).
		Updates(map[string]any{"default_binding_id": nil, "default_schema": nil, "updated_at": time.Now().UTC()}).Error
}
