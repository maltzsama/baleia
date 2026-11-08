package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/workbench/folders"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
)

// TxFolders runs fn in one transaction over a Repo bound to it, so a folder
// and its subtree move together (ADR 0020).
func (r *Repo) TxFolders(ctx context.Context, fn func(folders.Mover) error) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}

// TxWorkbench runs fn in one transaction over a Repo bound to it, for the
// workbench writes that must be atomic (statement references and joins).
func (r *Repo) TxWorkbench(ctx context.Context, fn func(*Repo) error) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}

// ── folders ───────────────────────────────────────────────────────────────────

// ListVisibleQueryFolders returns the children (or, without a parent, the
// roots) of the folders in spaces the user may see.
func (r *Repo) ListVisibleQueryFolders(ctx context.Context, arg db.ListVisibleQueryFoldersParams) ([]db.QueryFolder, error) {
	return r.listVisibleFolders(ctx, arg.WorkspaceID, arg.UserID, arg.SpaceID, &arg.ParentID, true)
}

// ListAllVisibleQueryFolders returns every visible folder of the workspace (or
// one space), not only the roots.
func (r *Repo) ListAllVisibleQueryFolders(ctx context.Context, arg db.ListAllVisibleQueryFoldersParams) ([]db.QueryFolder, error) {
	return r.listVisibleFolders(ctx, arg.WorkspaceID, arg.UserID, arg.SpaceID, nil, false)
}

func (r *Repo) listVisibleFolders(ctx context.Context, workspaceID, userID, spaceID pgtype.UUID, parent *pgtype.UUID, filterParent bool) ([]db.QueryFolder, error) {
	var out []db.QueryFolder
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		spaces, err := visibleSpaces(tx, workspaceID, userID)
		if err != nil || len(spaces) == 0 {
			return err
		}
		q := tx.Where("workspace_id = ? AND space_id IN ?", UUIDString(workspaceID), spaces)
		if spaceID.Valid {
			q = q.Where("space_id = ?", UUIDString(spaceID))
		}
		if filterParent {
			if parent != nil && parent.Valid {
				q = q.Where("parent_id = ?", UUIDString(*parent))
			} else {
				q = q.Where("parent_id IS NULL")
			}
		}
		var rows []model.QueryFolder
		if err := q.Order("name").Find(&rows).Error; err != nil {
			return err
		}
		out = make([]db.QueryFolder, len(rows))
		for i, row := range rows {
			out[i] = toDBQueryFolder(row)
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return out, err
}

// FolderSlugExists reports whether the space already has the folder slug.
func (r *Repo) FolderSlugExists(ctx context.Context, arg db.FolderSlugExistsParams) (bool, error) {
	if !arg.SpaceID.Valid {
		return false, nil
	}
	var count int64
	err := r.Users.db.WithContext(ctx).Model(&model.QueryFolder{}).
		Where("space_id = ? AND slug = ?", UUIDString(arg.SpaceID), arg.Slug).
		Count(&count).Error
	return count > 0, err
}

// DeleteQueryFolder removes a folder of the workspace and returns the rows.
func (r *Repo) DeleteQueryFolder(ctx context.Context, arg db.DeleteQueryFolderParams) (int64, error) {
	res := r.Users.db.WithContext(ctx).
		Where("id = ? AND workspace_id = ?", UUIDString(arg.ID), UUIDString(arg.WorkspaceID)).
		Delete(&model.QueryFolder{})
	return res.RowsAffected, res.Error
}

// MoveQueryFolderSubtree moves a folder and its descendants to another space.
// The recursive CTE of the sqlc query becomes an iterative walk in Go, so the
// write stays model-oriented and portable (ADR 0020). The saved queries in the
// subtree are detached and reattached around the folder move: the
// (space_id, folder_id) foreign key is immediate, so moving the folder first
// would leave them pointing at an old key.
func (r *Repo) MoveQueryFolderSubtree(ctx context.Context, arg db.MoveQueryFolderSubtreeParams) error {
	ids, err := r.folderSubtree(ctx, arg.ID, arg.WorkspaceID)
	if err != nil || len(ids) == 0 {
		return err
	}
	spaceID := model.UUID(UUIDString(arg.SpaceID))
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var saved []model.SavedQuery
		if err := tx.Where("folder_id IN ?", ids).Find(&saved).Error; err != nil {
			return err
		}
		if len(saved) > 0 {
			queryIDs := make([]string, len(saved))
			for i, query := range saved {
				queryIDs[i] = string(query.ID)
			}
			if err := tx.Model(&model.SavedQuery{}).Where("id IN ?", queryIDs).
				Update("folder_id", nil).Error; err != nil {
				return err
			}
		}
		// The target's parent may stay in the source space: clear it before
		// the move, and the caller's folder update sets the final parent.
		if err := tx.Model(&model.QueryFolder{}).
			Where("id = ? AND workspace_id = ?", UUIDString(arg.ID), UUIDString(arg.WorkspaceID)).
			Update("parent_id", nil).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.QueryFolder{}).Where("id IN ?", ids).
			Update("space_id", spaceID).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		for _, query := range saved {
			if err := tx.Model(&model.SavedQuery{}).Where("id = ?", string(query.ID)).
				Updates(map[string]any{
					"folder_id":  model.UUID(string(*query.FolderID)),
					"space_id":   spaceID,
					"updated_at": now,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// MoveSavedQueriesInFolderSubtree moves the saved queries in a folder subtree
// to the destination space.
func (r *Repo) MoveSavedQueriesInFolderSubtree(ctx context.Context, arg db.MoveSavedQueriesInFolderSubtreeParams) error {
	ids, err := r.folderSubtree(ctx, arg.ID, arg.WorkspaceID)
	if err != nil || len(ids) == 0 {
		return err
	}
	return r.Users.db.WithContext(ctx).Model(&model.SavedQuery{}).
		Where("folder_id IN ?", ids).
		Updates(map[string]any{"space_id": model.UUID(UUIDString(arg.SpaceID)), "updated_at": time.Now().UTC()}).Error
}

// folderSubtree returns the ids of the folder and its descendants, bounded by
// the workspace.
func (r *Repo) folderSubtree(ctx context.Context, id, workspaceID pgtype.UUID) ([]string, error) {
	if !id.Valid || !workspaceID.Valid {
		return nil, nil
	}
	root := UUIDString(id)
	var rootRow model.QueryFolder
	err := r.Users.db.WithContext(ctx).
		Where("id = ? AND workspace_id = ?", root, UUIDString(workspaceID)).
		First(&rootRow).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{root: true}
	ids := []string{root}
	frontier := []string{root}
	for len(frontier) > 0 {
		var children []model.QueryFolder
		if err := r.Users.db.WithContext(ctx).
			Where("workspace_id = ? AND parent_id IN ?", UUIDString(workspaceID), frontier).
			Find(&children).Error; err != nil {
			return nil, err
		}
		frontier = frontier[:0]
		for _, child := range children {
			childID := string(child.ID)
			if seen[childID] {
				continue
			}
			seen[childID] = true
			ids = append(ids, childID)
			frontier = append(frontier, childID)
		}
	}
	return ids, nil
}

// ── saved-query reads ─────────────────────────────────────────────────────────

// ListVisibleSavedQueries returns the queries in one folder of spaces the user
// may see.
func (r *Repo) ListVisibleSavedQueries(ctx context.Context, arg db.ListVisibleSavedQueriesParams) ([]db.SavedQuery, error) {
	return r.listVisibleSavedQueries(ctx, arg.WorkspaceID, arg.UserID, arg.SpaceID, &arg.FolderID, arg.Q, arg.Lim)
}

// SearchVisibleSavedQueries returns every visible saved query, optionally
// filtered by space and text (#113).
func (r *Repo) SearchVisibleSavedQueries(ctx context.Context, arg db.SearchVisibleSavedQueriesParams) ([]db.SavedQuery, error) {
	return r.listVisibleSavedQueries(ctx, arg.WorkspaceID, arg.UserID, arg.SpaceID, nil, arg.Q, arg.Lim)
}

func (r *Repo) listVisibleSavedQueries(ctx context.Context, workspaceID, userID, spaceID pgtype.UUID, folderID *pgtype.UUID, search *string, limit *int32) ([]db.SavedQuery, error) {
	var out []db.SavedQuery
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		spaces, err := visibleSpaces(tx, workspaceID, userID)
		if err != nil || len(spaces) == 0 {
			return err
		}
		q := tx.Where("workspace_id = ? AND space_id IN ?", UUIDString(workspaceID), spaces)
		if spaceID.Valid {
			q = q.Where("space_id = ?", UUIDString(spaceID))
		}
		if folderID != nil {
			if folderID.Valid {
				q = q.Where("folder_id = ?", UUIDString(*folderID))
			} else {
				q = q.Where("folder_id IS NULL")
			}
		}
		if search != nil {
			pattern := "%" + strings.ToLower(*search) + "%"
			q = q.Where("LOWER(name) LIKE ? OR LOWER(slug) LIKE ?", pattern, pattern)
		}
		if limit != nil {
			q = q.Limit(int(*limit))
		}
		var rows []model.SavedQuery
		if err := q.Order("name").Find(&rows).Error; err != nil {
			return err
		}
		out = make([]db.SavedQuery, len(rows))
		for i, row := range rows {
			out[i] = toDBSavedQuery(row)
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return out, err
}

// ListSavedQueryVersions returns the latest content version of every saved
// query in the workspace. The per-entity maximum is reduced in Go, so the
// query needs no GROUP BY (ADR 0020).
func (r *Repo) ListSavedQueryVersions(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListSavedQueryVersionsRow, error) {
	var rows []model.ContentVersion
	if err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND entity_type = ?", UUIDString(workspaceID), "saved_query").
		Order("entity_id, version DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ListSavedQueryVersionsRow, 0)
	seen := map[string]bool{}
	for _, row := range rows {
		id := string(row.EntityID)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, db.ListSavedQueryVersionsRow{EntityID: PGUUID(id), Version: row.Version})
	}
	return out, nil
}

// SavedQuerySlugExists reports whether the workspace already has the slug.
func (r *Repo) SavedQuerySlugExists(ctx context.Context, arg db.SavedQuerySlugExistsParams) (bool, error) {
	if !arg.WorkspaceID.Valid {
		return false, nil
	}
	var count int64
	err := r.Users.db.WithContext(ctx).Model(&model.SavedQuery{}).
		Where("workspace_id = ? AND slug = ?", UUIDString(arg.WorkspaceID), arg.Slug).
		Count(&count).Error
	return count > 0, err
}

// visibleSpaces returns the ids of the workspace's spaces the user may see,
// reproducing baleia_effective_space_role(x) IS NOT NULL in Go (ADR 0020
// removes the PL/pgSQL function).
func visibleSpaces(tx *gorm.DB, workspaceID, userID pgtype.UUID) ([]string, error) {
	if !workspaceID.Valid || !userID.Valid {
		return nil, nil
	}
	var user model.User
	if err := tx.Where("id = ?", UUIDString(userID)).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	wsRole, err := workspaceRole(tx, UUIDString(workspaceID), UUIDString(userID))
	if err != nil {
		return nil, err
	}
	if wsRole == "" && user.Role != "admin" {
		return nil, nil
	}
	var spaces []model.Space
	if err := tx.Where("workspace_id = ?", UUIDString(workspaceID)).Find(&spaces).Error; err != nil {
		return nil, err
	}
	out := make([]string, 0, len(spaces))
	for _, space := range spaces {
		role, err := spaceRoleFor(tx, user, wsRole, space, UUIDString(userID))
		if err != nil {
			return nil, err
		}
		if role != "" {
			out = append(out, string(space.ID))
		}
	}
	return out, nil
}
