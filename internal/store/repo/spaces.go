package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/workspaces/spaces"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TxSpaces runs fn in one transaction over a Repo bound to it, for creating a
// space with its first manager.
func (r *Repo) TxSpaces(ctx context.Context, fn func(spaces.TxStore) error) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}

// GetSpace returns one space or pgx.ErrNoRows.
func (r *Repo) GetSpace(ctx context.Context, arg db.GetSpaceParams) (db.Space, error) {
	if !arg.WorkspaceID.Valid || !arg.ID.Valid {
		return db.Space{}, pgx.ErrNoRows
	}
	var row model.Space
	err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND id = ?", UUIDString(arg.WorkspaceID), UUIDString(arg.ID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Space{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.Space{}, err
	}
	return toDBSpace(row), nil
}

// GetDefaultSpace returns the workspace's default space or pgx.ErrNoRows.
func (r *Repo) GetDefaultSpace(ctx context.Context, workspaceID pgtype.UUID) (db.Space, error) {
	var row model.Space
	err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND is_default = ?", UUIDString(workspaceID), true).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Space{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.Space{}, err
	}
	return toDBSpace(row), nil
}

// ListSpacesForPrincipal lists the workspace's spaces the user may see, with
// the effective role. A space with no role is absent, never forbidden.
func (r *Repo) ListSpacesForPrincipal(ctx context.Context, arg db.ListSpacesForPrincipalParams) ([]db.ListSpacesForPrincipalRow, error) {
	var out []db.ListSpacesForPrincipalRow
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
		var rows []model.Space
		if err := tx.Where("workspace_id = ?", UUIDString(arg.WorkspaceID)).
			Order("is_default DESC, name").
			Find(&rows).Error; err != nil {
			return err
		}
		for _, sp := range rows {
			role, err := spaceRoleFor(tx, user, wsRole, sp, UUIDString(arg.UserID))
			if err != nil {
				return err
			}
			if role == "" {
				continue
			}
			out = append(out, toDBListSpacesForPrincipalRow(sp, role))
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return out, err
}

// SpaceSlugExists reports whether the workspace already has the slug.
func (r *Repo) SpaceSlugExists(ctx context.Context, arg db.SpaceSlugExistsParams) (bool, error) {
	var count int64
	err := r.Users.db.WithContext(ctx).Model(&model.Space{}).
		Where("workspace_id = ? AND slug = ?", UUIDString(arg.WorkspaceID), arg.Slug).
		Count(&count).Error
	return count > 0, err
}

// CountSpaceContent counts every piece of content that lives in the space.
func (r *Repo) CountSpaceContent(ctx context.Context, spaceID pgtype.UUID) (int32, error) {
	id := UUIDString(spaceID)
	var total int64
	for _, m := range []any{&model.QueryFolder{}, &model.SavedQuery{}, &model.SemanticMetric{}, &model.SemanticModel{}} {
		var n int64
		if err := r.Users.db.WithContext(ctx).Model(m).Where("space_id = ?", id).Count(&n).Error; err != nil {
			return 0, err
		}
		total += n
	}
	return int32(total), nil
}

// CountSpaceContentKinds counts the space's content by kind.
func (r *Repo) CountSpaceContentKinds(ctx context.Context, spaceID pgtype.UUID) (db.CountSpaceContentKindsRow, error) {
	id := UUIDString(spaceID)
	var out db.CountSpaceContentKindsRow
	for _, c := range []struct {
		m   any
		dst *int64
	}{
		{&model.SavedQuery{}, &out.SavedQueries},
		{&model.SemanticMetric{}, &out.SemanticMetrics},
		{&model.SemanticModel{}, &out.SemanticModels},
		{&model.QueryFolder{}, &out.Folders},
	} {
		if err := r.Users.db.WithContext(ctx).Model(c.m).Where("space_id = ?", id).Count(c.dst).Error; err != nil {
			return db.CountSpaceContentKindsRow{}, err
		}
	}
	return out, nil
}

// ListSpaceMembers returns the space's access list.
func (r *Repo) ListSpaceMembers(ctx context.Context, spaceID pgtype.UUID) ([]db.SpaceMember, error) {
	var rows []model.SpaceMember
	if err := r.Users.db.WithContext(ctx).
		Where("space_id = ?", UUIDString(spaceID)).
		Order("principal_type, principal_id").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.SpaceMember, len(rows))
	for i, row := range rows {
		out[i] = toDBSpaceMember(row)
	}
	return out, nil
}

// CreateSpace inserts a space.
func (r *Repo) CreateSpace(ctx context.Context, arg db.CreateSpaceParams) (db.Space, error) {
	now := time.Now().UTC()
	row := model.Space{
		ID:          model.UUID(NewUUID()),
		WorkspaceID: model.UUID(UUIDString(arg.WorkspaceID)),
		Name:        model.Text(arg.Name),
		Description: arg.Description,
		Visibility:  arg.Visibility,
		DefaultRole: model.Text(arg.DefaultRole),
		CreatedBy:   modelUUIDPtr(arg.CreatedBy),
		CreatedAt:   now,
		UpdatedAt:   now,
		Slug:        model.Text(arg.Slug),
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.Space{}, err
	}
	return toDBSpace(row), nil
}

// UpdateSpace edits a space and returns the row.
func (r *Repo) UpdateSpace(ctx context.Context, arg db.UpdateSpaceParams) (db.Space, error) {
	if err := r.Users.db.WithContext(ctx).Model(&model.Space{}).
		Where("workspace_id = ? AND id = ?", UUIDString(arg.WorkspaceID), UUIDString(arg.ID)).
		Updates(map[string]any{
			"name":         arg.Name,
			"description":  arg.Description,
			"visibility":   arg.Visibility,
			"default_role": arg.DefaultRole,
			"updated_at":   time.Now().UTC(),
		}).Error; err != nil {
		return db.Space{}, err
	}
	return r.GetSpace(ctx, db.GetSpaceParams{WorkspaceID: arg.WorkspaceID, ID: arg.ID})
}

// DeleteSpace removes a non-default space and returns the rows.
func (r *Repo) DeleteSpace(ctx context.Context, arg db.DeleteSpaceParams) (int64, error) {
	res := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND id = ? AND is_default = ?", UUIDString(arg.WorkspaceID), UUIDString(arg.ID), false).
		Delete(&model.Space{})
	return res.RowsAffected, res.Error
}

// SetSpaceMember grants a user or team a role on a space, replacing an
// existing grant.
func (r *Repo) SetSpaceMember(ctx context.Context, arg db.SetSpaceMemberParams) (db.SpaceMember, error) {
	now := time.Now().UTC()
	row := model.SpaceMember{
		SpaceID:       model.UUID(UUIDString(arg.SpaceID)),
		PrincipalType: model.Text(arg.PrincipalType),
		PrincipalID:   model.UUID(UUIDString(arg.PrincipalID)),
		Role:          arg.Role,
		GrantedBy:     modelUUIDPtr(arg.GrantedBy),
		GrantedAt:     now,
	}
	if err := r.Users.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "space_id"}, {Name: "principal_type"}, {Name: "principal_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"role":       arg.Role,
			"granted_by": modelUUIDPtr(arg.GrantedBy),
			"granted_at": now,
		}),
	}).Create(&row).Error; err != nil {
		return db.SpaceMember{}, err
	}
	var stored model.SpaceMember
	if err := r.Users.db.WithContext(ctx).
		Where("space_id = ? AND principal_type = ? AND principal_id = ?",
			UUIDString(arg.SpaceID), arg.PrincipalType, UUIDString(arg.PrincipalID)).
		First(&stored).Error; err != nil {
		return db.SpaceMember{}, err
	}
	return toDBSpaceMember(stored), nil
}

// DeleteSpaceMember takes a principal off a space's access list and returns the
// rows.
func (r *Repo) DeleteSpaceMember(ctx context.Context, arg db.DeleteSpaceMemberParams) (int64, error) {
	res := r.Users.db.WithContext(ctx).
		Where("space_id = ? AND principal_type = ? AND principal_id = ?",
			UUIDString(arg.SpaceID), arg.PrincipalType, UUIDString(arg.PrincipalID)).
		Delete(&model.SpaceMember{})
	return res.RowsAffected, res.Error
}

func toDBSpace(s model.Space) db.Space {
	return db.Space{
		ID:          PGUUID(string(s.ID)),
		WorkspaceID: PGUUID(string(s.WorkspaceID)),
		Name:        string(s.Name),
		Description: s.Description,
		Visibility:  s.Visibility,
		DefaultRole: string(s.DefaultRole),
		IsDefault:   s.IsDefault,
		CreatedBy:   uuidPtr(s.CreatedBy),
		CreatedAt:   PGTime(s.CreatedAt),
		UpdatedAt:   PGTime(s.UpdatedAt),
		Slug:        string(s.Slug),
	}
}

func toDBSpaceMember(m model.SpaceMember) db.SpaceMember {
	return db.SpaceMember{
		SpaceID:       PGUUID(string(m.SpaceID)),
		PrincipalType: string(m.PrincipalType),
		PrincipalID:   PGUUID(string(m.PrincipalID)),
		Role:          m.Role,
		GrantedBy:     uuidPtr(m.GrantedBy),
		GrantedAt:     PGTime(m.GrantedAt),
	}
}

func toDBListSpacesForPrincipalRow(s model.Space, role string) db.ListSpacesForPrincipalRow {
	return db.ListSpacesForPrincipalRow{
		ID:          PGUUID(string(s.ID)),
		WorkspaceID: PGUUID(string(s.WorkspaceID)),
		Name:        string(s.Name),
		Description: s.Description,
		Visibility:  s.Visibility,
		DefaultRole: string(s.DefaultRole),
		IsDefault:   s.IsDefault,
		CreatedBy:   uuidPtr(s.CreatedBy),
		CreatedAt:   PGTime(s.CreatedAt),
		UpdatedAt:   PGTime(s.UpdatedAt),
		Slug:        string(s.Slug),
		MyRole:      role,
	}
}
