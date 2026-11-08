package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ── grants and requests ──────────────────────────────────────────────────────

// ListCatalogGrants lists the workspaces a catalog is granted to.
func (r *Repo) ListCatalogGrants(ctx context.Context, catalogID pgtype.UUID) ([]db.ListCatalogGrantsRow, error) {
	var rows []struct {
		WorkspaceID   model.UUID
		WorkspaceName string
		WorkspaceType string
		GrantedBy     model.UUID
		GrantedAt     time.Time
	}
	err := r.Users.db.WithContext(ctx).
		Table("workspace_catalog_grants AS g").
		Select("g.workspace_id, w.name AS workspace_name, w.type AS workspace_type, g.granted_by, g.granted_at").
		Joins("JOIN workspaces w ON w.id = g.workspace_id").
		Where("g.catalog_id = ?", UUIDString(catalogID)).
		Order("w.name").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]db.ListCatalogGrantsRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListCatalogGrantsRow{
			WorkspaceID:   PGUUID(string(row.WorkspaceID)),
			WorkspaceName: row.WorkspaceName,
			WorkspaceType: row.WorkspaceType,
			GrantedBy:     PGUUID(string(row.GrantedBy)),
			GrantedAt:     PGTime(row.GrantedAt),
		}
	}
	return out, nil
}

// ListCatalogGrantRequestsForWorkspace lists a workspace's pending requests
// with the catalog and connection names the UI shows.
func (r *Repo) ListCatalogGrantRequestsForWorkspace(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListCatalogGrantRequestsForWorkspaceRow, error) {
	var rows []struct {
		WorkspaceID       model.UUID
		CatalogID         model.UUID
		InitiatedBySide   string
		RequestedBy       *model.UUID
		RequestedAt       time.Time
		CatalogName       string
		EngineCatalogName string
		ConnectionName    string
	}
	err := r.Users.db.WithContext(ctx).
		Table("catalog_grant_requests AS r").
		Select(`r.workspace_id, r.catalog_id, r.initiated_by_side, r.requested_by, r.requested_at,
			c.name AS catalog_name, COALESCE(c.trino_catalog_name, c.name) AS engine_catalog_name,
			cc.name AS connection_name`).
		Joins("JOIN catalogs c ON c.id = r.catalog_id").
		Joins("JOIN catalog_connections cc ON cc.id = c.connection_id").
		Where("r.workspace_id = ?", UUIDString(workspaceID)).
		Order("r.requested_at").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]db.ListCatalogGrantRequestsForWorkspaceRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListCatalogGrantRequestsForWorkspaceRow{
			WorkspaceID:       PGUUID(string(row.WorkspaceID)),
			CatalogID:         PGUUID(string(row.CatalogID)),
			InitiatedBySide:   row.InitiatedBySide,
			RequestedBy:       uuidPtr(row.RequestedBy),
			RequestedAt:       PGTime(row.RequestedAt),
			CatalogName:       row.CatalogName,
			EngineCatalogName: row.EngineCatalogName,
			ConnectionName:    row.ConnectionName,
		}
	}
	return out, nil
}

// ListCatalogGrantRequestsForCatalog lists a catalog's pending requests.
func (r *Repo) ListCatalogGrantRequestsForCatalog(ctx context.Context, catalogID pgtype.UUID) ([]db.ListCatalogGrantRequestsForCatalogRow, error) {
	var rows []struct {
		WorkspaceID     model.UUID
		CatalogID       model.UUID
		InitiatedBySide string
		RequestedBy     *model.UUID
		RequestedAt     time.Time
		WorkspaceName   string
		WorkspaceType   string
	}
	err := r.Users.db.WithContext(ctx).
		Table("catalog_grant_requests AS r").
		Select(`r.workspace_id, r.catalog_id, r.initiated_by_side, r.requested_by, r.requested_at,
			w.name AS workspace_name, w.type AS workspace_type`).
		Joins("JOIN workspaces w ON w.id = r.workspace_id").
		Where("r.catalog_id = ?", UUIDString(catalogID)).
		Order("r.requested_at").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]db.ListCatalogGrantRequestsForCatalogRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListCatalogGrantRequestsForCatalogRow{
			WorkspaceID:     PGUUID(string(row.WorkspaceID)),
			CatalogID:       PGUUID(string(row.CatalogID)),
			InitiatedBySide: row.InitiatedBySide,
			RequestedBy:     uuidPtr(row.RequestedBy),
			RequestedAt:     PGTime(row.RequestedAt),
			WorkspaceName:   row.WorkspaceName,
			WorkspaceType:   row.WorkspaceType,
		}
	}
	return out, nil
}

// CreateCatalogGrantRequest stores a pending offer or request, flipping the
// side when the other party initiates.
func (r *Repo) CreateCatalogGrantRequest(ctx context.Context, arg db.CreateCatalogGrantRequestParams) (db.CatalogGrantRequest, error) {
	now := time.Now().UTC()
	row := model.CatalogGrantRequest{
		WorkspaceID:     model.UUID(UUIDString(arg.WorkspaceID)),
		CatalogID:       model.UUID(UUIDString(arg.CatalogID)),
		InitiatedBySide: arg.InitiatedBySide,
		RequestedBy:     modelUUIDPtr(arg.RequestedBy),
		RequestedAt:     now,
	}
	err := r.Users.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "workspace_id"}, {Name: "catalog_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"initiated_by_side": arg.InitiatedBySide,
			"requested_by":      modelUUIDPtr(arg.RequestedBy),
			"requested_at":      now,
		}),
	}).Create(&row).Error
	if err != nil {
		return db.CatalogGrantRequest{}, err
	}
	return r.GetCatalogGrantRequest(ctx, db.GetCatalogGrantRequestParams{
		WorkspaceID: arg.WorkspaceID, CatalogID: arg.CatalogID,
	})
}

// GetCatalogGrantRequest returns one pending request or pgx.ErrNoRows.
func (r *Repo) GetCatalogGrantRequest(ctx context.Context, arg db.GetCatalogGrantRequestParams) (db.CatalogGrantRequest, error) {
	var row model.CatalogGrantRequest
	err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND catalog_id = ?", UUIDString(arg.WorkspaceID), UUIDString(arg.CatalogID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.CatalogGrantRequest{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.CatalogGrantRequest{}, err
	}
	return toDBCatalogGrantRequest(row), nil
}

// DeleteCatalogGrantRequest removes a pending request and returns the rows.
func (r *Repo) DeleteCatalogGrantRequest(ctx context.Context, arg db.DeleteCatalogGrantRequestParams) (int64, error) {
	res := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND catalog_id = ?", UUIDString(arg.WorkspaceID), UUIDString(arg.CatalogID)).
		Delete(&model.CatalogGrantRequest{})
	return res.RowsAffected, res.Error
}

// ListGrantableWorkspaces lists the workspaces a catalog owner may offer to.
func (r *Repo) ListGrantableWorkspaces(ctx context.Context) ([]db.ListGrantableWorkspacesRow, error) {
	var rows []struct {
		ID          model.UUID
		Name        string
		Type        string
		OwnerTeamID *model.UUID
		TeamName    *string
	}
	err := r.Users.db.WithContext(ctx).
		Table("workspaces AS w").
		Select("w.id, w.name, w.type, w.owner_team_id, t.name AS team_name").
		Joins("LEFT JOIN teams t ON t.id = w.owner_team_id").
		Order("w.name").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]db.ListGrantableWorkspacesRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListGrantableWorkspacesRow{
			ID:       PGUUID(string(row.ID)),
			Name:     row.Name,
			Type:     row.Type,
			TeamName: row.TeamName,
		}
	}
	return out, nil
}

// ListGrantedCatalogsOfWorkspace lists the enabled catalogs granted to a
// workspace, with their connection.
func (r *Repo) ListGrantedCatalogsOfWorkspace(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListGrantedCatalogsOfWorkspaceRow, error) {
	var rows []struct {
		ID               model.UUID
		ConnectionID     model.UUID
		Name             model.Text
		Description      *string
		TrinoCatalogName *model.Text
		ClusterID        *model.UUID
		Warehouse        *string
		Connector        *model.Text
		ExtraProps       model.JSON
		Enabled          bool
		CreatedAt        time.Time
		UpdatedAt        time.Time
		Origin           model.Text
		SourceRef        *string
		Discoverable     bool
		ConnectionName   string
		ConnectionType   string
	}
	err := r.Users.db.WithContext(ctx).
		Table("workspace_catalog_grants AS g").
		Select(`c.id, c.connection_id, c.name, c.description, c.trino_catalog_name, c.cluster_id,
			c.warehouse, c.connector, c.extra_props, c.enabled, c.created_at, c.updated_at,
			c.origin, c.source_ref, c.discoverable, cc.name AS connection_name, cc.type AS connection_type`).
		Joins("JOIN catalogs c ON c.id = g.catalog_id").
		Joins("JOIN catalog_connections cc ON cc.id = c.connection_id").
		Where("g.workspace_id = ? AND c.enabled", UUIDString(workspaceID)).
		Order("c.trino_catalog_name").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]db.ListGrantedCatalogsOfWorkspaceRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListGrantedCatalogsOfWorkspaceRow{
			ID:               PGUUID(string(row.ID)),
			ConnectionID:     PGUUID(string(row.ConnectionID)),
			Name:             string(row.Name),
			Description:      row.Description,
			TrinoCatalogName: textPtr(row.TrinoCatalogName),
			ClusterID:        uuidPtr(row.ClusterID),
			Warehouse:        row.Warehouse,
			Connector:        textPtr(row.Connector),
			ExtraProps:       []byte(row.ExtraProps),
			Enabled:          row.Enabled,
			CreatedAt:        PGTime(row.CreatedAt),
			UpdatedAt:        PGTime(row.UpdatedAt),
			Origin:           string(row.Origin),
			SourceRef:        row.SourceRef,
			Discoverable:     row.Discoverable,
			ConnectionName:   row.ConnectionName,
			ConnectionType:   row.ConnectionType,
		}
	}
	return out, nil
}

func toDBCatalogGrantRequest(r model.CatalogGrantRequest) db.CatalogGrantRequest {
	return db.CatalogGrantRequest{
		WorkspaceID:     PGUUID(string(r.WorkspaceID)),
		CatalogID:       PGUUID(string(r.CatalogID)),
		InitiatedBySide: r.InitiatedBySide,
		RequestedBy:     uuidPtr(r.RequestedBy),
		RequestedAt:     PGTime(r.RequestedAt),
	}
}
