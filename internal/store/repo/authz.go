package repo

import (
	"context"
	"database/sql"
	"errors"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
)

// GetCatalogConnectionByID returns one connection or pgx.ErrNoRows.
func (r *Repo) GetCatalogConnectionByID(ctx context.Context, id pgtype.UUID) (db.CatalogConnection, error) {
	if !id.Valid {
		return db.CatalogConnection{}, pgx.ErrNoRows
	}
	var row model.CatalogConnection
	err := r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.CatalogConnection{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.CatalogConnection{}, err
	}
	return db.CatalogConnection{
		ID:                  PGUUID(string(row.ID)),
		Name:                string(row.Name),
		Type:                row.Type,
		Url:                 row.URL,
		CredentialsEnc:      row.CredentialsEnc,
		CreatedAt:           PGTime(row.CreatedAt),
		UpdatedAt:           PGTime(row.UpdatedAt),
		Host:                row.Host,
		Port:                row.Port,
		CreatedBy:           uuidPtr(row.CreatedBy),
		Config:              []byte(row.Config),
		CredentialsReadOnly: row.CredentialsReadOnly,
	}, nil
}

// GetCatalogByID returns one catalog or pgx.ErrNoRows.
func (r *Repo) GetCatalogByID(ctx context.Context, id pgtype.UUID) (db.Catalog, error) {
	if !id.Valid {
		return db.Catalog{}, pgx.ErrNoRows
	}
	var row model.Catalog
	err := r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Catalog{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.Catalog{}, err
	}
	return db.Catalog{
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
	}, nil
}

// GetEngineByID returns one engine or pgx.ErrNoRows.
func (r *Repo) GetEngineByID(ctx context.Context, id pgtype.UUID) (db.Engine, error) {
	if !id.Valid {
		return db.Engine{}, pgx.ErrNoRows
	}
	var row model.Engine
	err := r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Engine{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.Engine{}, err
	}
	return db.Engine{
		ID:                         PGUUID(string(row.ID)),
		Name:                       string(row.Name),
		Kind:                       row.Kind,
		Class:                      row.Class,
		ConnectionID:               PGUUID(string(row.ConnectionID)),
		IdentityMode:               string(row.IdentityMode),
		Enabled:                    row.Enabled,
		CreatedBy:                  uuidPtr(row.CreatedBy),
		CreatedAt:                  PGTime(row.CreatedAt),
		UpdatedAt:                  PGTime(row.UpdatedAt),
		MaxRunningTotal:            row.MaxRunningTotal,
		CostRoutingEnabled:         row.CostRoutingEnabled,
		CostThresholdBytes:         row.CostThresholdBytes,
		ExternalUrl:                row.ExternalURL,
		TrinoResourceGroupsEnabled: row.TrinoResourceGroupsEnabled,
	}, nil
}

// GetEffectiveSpaceRole is the Go replacement for the
// baleia_effective_space_role() PL/pgSQL function removed by ADR 0020.
func (r *Repo) GetEffectiveSpaceRole(ctx context.Context, arg db.GetEffectiveSpaceRoleParams) (string, error) {
	if !arg.WorkspaceID.Valid || !arg.SpaceID.Valid || !arg.UserID.Valid {
		return "", nil
	}
	var role string
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Where("id = ?", UUIDString(arg.UserID)).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		wsRole, err := workspaceRole(tx, UUIDString(arg.WorkspaceID), UUIDString(arg.UserID))
		if err != nil {
			return err
		}

		var space model.Space
		if err := tx.Where("id = ? AND workspace_id = ?", UUIDString(arg.SpaceID), UUIDString(arg.WorkspaceID)).
			First(&space).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}

		role, err = spaceRoleFor(tx, user, wsRole, space, UUIDString(arg.UserID))
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return role, err
}

// spaceRoleFor computes the effective role over already-loaded user, workspace
// role and space, so a listing does not re-read them per space.
func spaceRoleFor(tx *gorm.DB, user model.User, wsRole string, space model.Space, userID string) (string, error) {
	if user.Role == "admin" {
		wsRole = "admin"
	}
	if wsRole == "" {
		return "", nil
	}
	capRank := roleRank(wsRole)

	maxRank := 0
	if wsRole == "admin" {
		maxRank = 3
	}
	if space.Visibility == "workspace" {
		r := 1
		if space.DefaultRole == "editor" {
			r = 2
		}
		if r > maxRank {
			maxRank = r
		}
	}
	var member model.SpaceMember
	err := tx.Where("space_id = ? AND principal_type = ? AND principal_id = ?", string(space.ID), "user", userID).
		First(&member).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	if err == nil && roleRank(member.Role) > maxRank {
		maxRank = roleRank(member.Role)
	}
	teams := tx.Model(&model.TeamMember{}).Select("team_id").Where("user_id = ?", userID)
	var teamMembers []model.SpaceMember
	if err := tx.Where("space_id = ? AND principal_type = ? AND principal_id IN (?)", string(space.ID), "team", teams).
		Find(&teamMembers).Error; err != nil {
		return "", err
	}
	for _, tm := range teamMembers {
		if roleRank(tm.Role) > maxRank {
			maxRank = roleRank(tm.Role)
		}
	}
	if maxRank == 0 {
		return "", nil
	}
	if maxRank > capRank {
		maxRank = capRank
	}
	switch maxRank {
	case 3:
		return "manager", nil
	case 2:
		return "editor", nil
	case 1:
		return "viewer", nil
	}
	return "", nil
}

func roleRank(role string) int {
	switch role {
	case "admin", "manager":
		return 3
	case "editor":
		return 2
	case "viewer":
		return 1
	default:
		return 0
	}
}
