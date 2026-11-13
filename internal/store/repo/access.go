package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// CheckWorkspaceAccess reports whether the user is a member of the workspace.
func (r *Repo) CheckWorkspaceAccess(ctx context.Context, arg db.CheckWorkspaceAccessParams) (bool, error) {
	role, err := r.WorkspaceRole(ctx, UUIDString(arg.ID), UUIDString(arg.UserID))
	if err != nil {
		return false, err
	}
	return role != "", nil
}

// AgentWorkspaceBound reports whether the agent is bound to the workspace.
func (r *Repo) AgentWorkspaceBound(ctx context.Context, arg db.AgentWorkspaceBoundParams) (bool, error) {
	var count int64
	err := r.Users.db.WithContext(ctx).Model(&model.AgentWorkspace{}).
		Where("agent_id = ? AND workspace_id = ?", UUIDString(arg.AgentID), UUIDString(arg.WorkspaceID)).
		Count(&count).Error
	return count > 0, err
}

// GetWorkspaceByID returns one workspace or db.ErrNoRows.
func (r *Repo) GetWorkspaceByID(ctx context.Context, id db.UUID) (db.Workspace, error) {
	if !id.Valid {
		return db.Workspace{}, db.ErrNoRows
	}
	var row model.Workspace
	err := r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Workspace{}, db.ErrNoRows
	}
	if err != nil {
		return db.Workspace{}, err
	}
	return toDBWorkspace(row), nil
}

// ListWorkspaceTargets lists every binding of a catalog the workspace may use.
func (r *Repo) ListWorkspaceTargets(ctx context.Context, workspaceID db.UUID) ([]db.ListWorkspaceTargetsRow, error) {
	var rows []struct {
		BindingID         model.UUID
		EngineCatalogName string
		Mode              string
		BindingEnabled    bool
		EngineID          model.UUID
		EngineName        string
		EngineKind        string
		EngineEnabled     bool
		CatalogID         model.UUID
		CatalogName       string
		CatalogEnabled    bool
	}
	err := r.Users.db.WithContext(ctx).
		Table("workspace_catalog_grants AS g").
		Select(`b.id AS binding_id, b.engine_catalog_name, b.mode, b.enabled AS binding_enabled,
			e.id AS engine_id, e.name AS engine_name, e.kind AS engine_kind, e.enabled AS engine_enabled,
			c.id AS catalog_id, c.name AS catalog_name, c.enabled AS catalog_enabled`).
		Joins("JOIN catalogs c ON c.id = g.catalog_id").
		Joins("JOIN catalog_bindings b ON b.catalog_id = c.id").
		Joins("JOIN engines e ON e.id = b.engine_id").
		Where("g.workspace_id = ?", UUIDString(workspaceID)).
		Order("c.name, e.name").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]db.ListWorkspaceTargetsRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListWorkspaceTargetsRow{
			BindingID:         PGUUID(string(row.BindingID)),
			EngineCatalogName: row.EngineCatalogName,
			Mode:              row.Mode,
			BindingEnabled:    row.BindingEnabled,
			EngineID:          PGUUID(string(row.EngineID)),
			EngineName:        row.EngineName,
			EngineKind:        row.EngineKind,
			EngineEnabled:     row.EngineEnabled,
			CatalogID:         PGUUID(string(row.CatalogID)),
			CatalogName:       row.CatalogName,
			CatalogEnabled:    row.CatalogEnabled,
		}
	}
	return out, nil
}

// ListWorkspacePrincipalUsers lists the users who are members of the
// workspace, by name, up to lim.
func (r *Repo) ListWorkspacePrincipalUsers(ctx context.Context, arg db.ListWorkspacePrincipalUsersParams) ([]db.ListWorkspacePrincipalUsersRow, error) {
	if arg.Lim <= 0 {
		return nil, nil
	}
	var out []db.ListWorkspacePrincipalUsersRow
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var users []model.User
		if err := tx.Order("name").Find(&users).Error; err != nil {
			return err
		}
		for _, user := range users {
			if len(out) >= int(arg.Lim) {
				break
			}
			role, err := workspaceRole(tx, UUIDString(arg.WorkspaceID), string(user.ID))
			if err != nil {
				return err
			}
			if role == "" {
				continue
			}
			out = append(out, db.ListWorkspacePrincipalUsersRow{
				ID: PGUUID(string(user.ID)), Name: user.Name, Email: string(user.Email),
			})
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return out, err
}

// UserOwnsTeam reports whether the user owns at least one team.
func (r *Repo) UserOwnsTeam(ctx context.Context, userID db.UUID) (bool, error) {
	var count int64
	err := r.Users.db.WithContext(ctx).Model(&model.TeamMember{}).
		Where("user_id = ? AND role = ?", UUIDString(userID), "owner").
		Count(&count).Error
	return count > 0, err
}

// ListWorkspacesByUser lists the workspaces the user is a member of, by name.
func (r *Repo) ListWorkspacesByUser(ctx context.Context, userID db.UUID) ([]db.Workspace, error) {
	var out []db.Workspace
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []model.Workspace
		if err := tx.Order("name").Find(&rows).Error; err != nil {
			return err
		}
		for _, ws := range rows {
			role, err := workspaceRole(tx, string(ws.ID), UUIDString(userID))
			if err != nil {
				return err
			}
			if role != "" {
				out = append(out, toDBWorkspace(ws))
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return out, err
}

// CreateWorkspace inserts a workspace and, when the schema has no default-space
// trigger, its default space in the same transaction (ADR 0020 removed the
// trigger). On the goose schema the trigger wins and the check skips the write.
func (r *Repo) CreateWorkspace(ctx context.Context, arg db.CreateWorkspaceParams) (db.Workspace, error) {
	var out db.Workspace
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		row := model.Workspace{
			ID:                 model.UUID(NewUUID()),
			Name:               arg.Name,
			Description:        arg.Description,
			Type:               arg.Type,
			PolarisCatalogName: model.Text(arg.PolarisCatalogName),
			OwnerUserID:        modelUUIDPtr(arg.OwnerUserID),
			OwnerTeamID:        modelUUIDPtr(arg.OwnerTeamID),
			CreatedAt:          now,
			UpdatedAt:          now,
			BIEnabled:          true,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.Space{}).
			Where("workspace_id = ? AND is_default = ?", string(row.ID), true).
			Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			space := model.Space{
				ID:          model.UUID(NewUUID()),
				WorkspaceID: row.ID,
				Name:        "General",
				Visibility:  "workspace",
				DefaultRole: "editor",
				IsDefault:   true,
				CreatedAt:   now,
				UpdatedAt:   now,
				Slug:        "general",
			}
			if err := tx.Create(&space).Error; err != nil {
				return err
			}
		}
		out = toDBWorkspace(row)
		return nil
	})
	if err != nil {
		return db.Workspace{}, err
	}
	return out, nil
}

// GetDefaultTrinoCluster returns the default cluster or db.ErrNoRows.
func (r *Repo) GetDefaultTrinoCluster(ctx context.Context) (db.TrinoCluster, error) {
	var row model.TrinoCluster
	err := r.Users.db.WithContext(ctx).Where("name = ?", "default").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.TrinoCluster{}, db.ErrNoRows
	}
	if err != nil {
		return db.TrinoCluster{}, err
	}
	return db.TrinoCluster{
		ID:             PGUUID(string(row.ID)),
		Name:           string(row.Name),
		CoordinatorUrl: row.CoordinatorURL,
		NodeVersion:    row.NodeVersion,
		CreatedAt:      PGTime(row.CreatedAt),
		UpdatedAt:      PGTimePtr(row.UpdatedAt),
		EngineID:       uuidPtr(row.EngineID),
	}, nil
}

// ListEnabledIcebergCatalogs lists the enabled source catalogs whose
// connection is one of kinds (connections.Registry.TableHealthKinds).
func (r *Repo) ListEnabledIcebergCatalogs(ctx context.Context, kinds []string) ([]db.ListEnabledIcebergCatalogsRow, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	var rows []struct {
		ID               model.UUID
		ConnectionID     model.UUID
		Name             string
		TrinoCatalogName string
		SourceRef        *string
		Warehouse        *string
	}
	err := r.Users.db.WithContext(ctx).
		Table("catalogs c").
		Select("c.id, c.connection_id, c.name, COALESCE(c.trino_catalog_name, '') AS trino_catalog_name, c.source_ref, c.warehouse").
		Joins("JOIN catalog_connections cc ON cc.id = c.connection_id").
		Where("c.enabled = ? AND c.origin = ? AND cc.type IN ?", true, "source", kinds).
		Order("c.name").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]db.ListEnabledIcebergCatalogsRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListEnabledIcebergCatalogsRow{
			ID:               PGUUID(string(row.ID)),
			ConnectionID:     PGUUID(string(row.ConnectionID)),
			Name:             row.Name,
			TrinoCatalogName: row.TrinoCatalogName,
			SourceRef:        row.SourceRef,
			Warehouse:        row.Warehouse,
		}
	}
	return out, nil
}

// ListTableHealthCatalogs lists the enabled source catalogs that get table
// health: every catalog whose connection is one of restKinds
// (connections.Registry.TableHealthKinds), and the Iceberg-connector catalogs
// of the engineKinds connections (EngineTableHealthKinds, #997).
func (r *Repo) ListTableHealthCatalogs(ctx context.Context, restKinds, engineKinds []string) ([]db.ListEnabledIcebergCatalogsRow, error) {
	var conds []string
	var args []any
	if len(restKinds) > 0 {
		conds = append(conds, "cc.type IN ?")
		args = append(args, restKinds)
	}
	if len(engineKinds) > 0 {
		conds = append(conds, "(cc.type IN ? AND c.connector = ?)")
		args = append(args, engineKinds, "iceberg")
	}
	if len(conds) == 0 {
		return nil, nil
	}
	var rows []struct {
		ID               model.UUID
		ConnectionID     model.UUID
		Name             string
		TrinoCatalogName string
		SourceRef        *string
		Warehouse        *string
	}
	err := r.Users.db.WithContext(ctx).
		Table("catalogs c").
		Select("c.id, c.connection_id, c.name, COALESCE(c.trino_catalog_name, '') AS trino_catalog_name, c.source_ref, c.warehouse").
		Joins("JOIN catalog_connections cc ON cc.id = c.connection_id").
		Where("c.enabled = ? AND c.origin = ?", true, "source").
		Where("("+strings.Join(conds, " OR ")+")", args...).
		Order("c.name").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]db.ListEnabledIcebergCatalogsRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListEnabledIcebergCatalogsRow{
			ID:               PGUUID(string(row.ID)),
			ConnectionID:     PGUUID(string(row.ConnectionID)),
			Name:             row.Name,
			TrinoCatalogName: row.TrinoCatalogName,
			SourceRef:        row.SourceRef,
			Warehouse:        row.Warehouse,
		}
	}
	return out, nil
}

// IsWorkspaceOwner reports whether the user's effective role is admin.
func (r *Repo) IsWorkspaceOwner(ctx context.Context, arg db.IsWorkspaceOwnerParams) (bool, error) {
	role, err := r.WorkspaceRole(ctx, UUIDString(arg.ID), UUIDString(arg.UserID))
	if err != nil {
		return false, err
	}
	return role == "admin", nil
}

// GetWorkspaceMemberRole returns the user's effective workspace role, or "".
func (r *Repo) GetWorkspaceMemberRole(ctx context.Context, arg db.GetWorkspaceMemberRoleParams) (string, error) {
	return r.WorkspaceRole(ctx, UUIDString(arg.ID), UUIDString(arg.UserID))
}

// GetTeamMemberRole returns the user's team role, or db.ErrNoRows when the
// user is not a member.
func (r *Repo) GetTeamMemberRole(ctx context.Context, arg db.GetTeamMemberRoleParams) (string, error) {
	var row model.TeamMember
	err := r.Users.db.WithContext(ctx).
		Where("team_id = ? AND user_id = ?", UUIDString(arg.TeamID), UUIDString(arg.UserID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", db.ErrNoRows
	}
	if err != nil {
		return "", err
	}
	return string(row.Role), nil
}

func toDBWorkspace(w model.Workspace) db.Workspace {
	return db.Workspace{
		ID:                 PGUUID(string(w.ID)),
		Name:               w.Name,
		Description:        w.Description,
		Type:               string(w.Type),
		PolarisCatalogName: string(w.PolarisCatalogName),
		OwnerUserID:        uuidPtr(w.OwnerUserID),
		OwnerTeamID:        uuidPtr(w.OwnerTeamID),
		CreatedAt:          PGTime(w.CreatedAt),
		UpdatedAt:          PGTime(w.UpdatedAt),
		DefaultBindingID:   uuidPtr(w.DefaultBindingID),
		DefaultSchema:      w.DefaultSchema,
		BIEnabled:          w.BIEnabled,
	}
}
