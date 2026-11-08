package repo

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ content.Store = (*Repo)(nil)

// TxContent runs fn in one transaction over a Repo bound to it, for the
// content service to write an entity and its version atomically (ADR 0020).
func (r *Repo) TxContent(ctx context.Context, fn func(content.Store) error) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}

// ── saved queries and folders ──────────────────────────────────────────────────

// CreateQueryFolder inserts a folder; the id is generated in the application
// so the insert works on every backend.
func (r *Repo) CreateQueryFolder(ctx context.Context, arg db.CreateQueryFolderParams) (db.QueryFolder, error) {
	row := model.QueryFolder{
		ID:          model.UUID(NewUUID()),
		WorkspaceID: model.UUID(UUIDString(arg.WorkspaceID)),
		ParentID:    modelUUIDPtr(arg.ParentID),
		Name:        arg.Name,
		CreatedAt:   time.Now().UTC(),
		SpaceID:     model.UUID(UUIDString(arg.SpaceID)),
		Slug:        model.Text(arg.Slug),
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.QueryFolder{}, contentDuplicate(err)
	}
	return toDBQueryFolder(row), nil
}

// GetQueryFolderByID returns one folder or pgx.ErrNoRows.
func (r *Repo) GetQueryFolderByID(ctx context.Context, arg db.GetQueryFolderByIDParams) (db.QueryFolder, error) {
	return r.getQueryFolder(ctx, arg.ID, arg.WorkspaceID)
}

// GetFolderBySlug returns the folder of a space by slug or pgx.ErrNoRows.
func (r *Repo) GetFolderBySlug(ctx context.Context, arg db.GetFolderBySlugParams) (db.QueryFolder, error) {
	if !arg.SpaceID.Valid {
		return db.QueryFolder{}, pgx.ErrNoRows
	}
	var row model.QueryFolder
	err := r.Users.db.WithContext(ctx).
		Where("space_id = ? AND slug = ?", UUIDString(arg.SpaceID), arg.Slug).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.QueryFolder{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.QueryFolder{}, err
	}
	return toDBQueryFolder(row), nil
}

// UpdateQueryFolder edits a folder; a nil slug or an invalid space keeps the
// stored value, as the sqlc COALESCE did. A space change detaches and
// reattaches the folder's saved queries around the move, because the
// (space_id, folder_id) foreign key is immediate.
func (r *Repo) UpdateQueryFolder(ctx context.Context, arg db.UpdateQueryFolderParams) (db.QueryFolder, error) {
	var out db.QueryFolder
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current model.QueryFolder
		err := tx.Where("id = ? AND workspace_id = ?", UUIDString(arg.ID), UUIDString(arg.WorkspaceID)).
			First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pgx.ErrNoRows
		}
		if err != nil {
			return err
		}
		moving := arg.SpaceID.Valid && string(current.SpaceID) != UUIDString(arg.SpaceID)
		var saved []model.SavedQuery
		if moving {
			if err := tx.Where("folder_id = ?", string(current.ID)).Find(&saved).Error; err != nil {
				return err
			}
			if len(saved) > 0 {
				ids := make([]string, len(saved))
				for i, query := range saved {
					ids[i] = string(query.ID)
				}
				if err := tx.Model(&model.SavedQuery{}).Where("id IN ?", ids).
					Update("folder_id", nil).Error; err != nil {
					return err
				}
			}
		}
		updates := map[string]any{
			"name":      arg.Name,
			"parent_id": modelUUIDPtr(arg.ParentID),
		}
		if arg.Slug != nil {
			updates["slug"] = *arg.Slug
		}
		if arg.SpaceID.Valid {
			updates["space_id"] = model.UUID(UUIDString(arg.SpaceID))
		}
		if err := tx.Model(&model.QueryFolder{}).
			Where("id = ? AND workspace_id = ?", UUIDString(arg.ID), UUIDString(arg.WorkspaceID)).
			Updates(updates).Error; err != nil {
			return contentDuplicate(err)
		}
		if moving {
			spaceID := model.UUID(UUIDString(arg.SpaceID))
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
		}
		var stored model.QueryFolder
		if err := tx.Where("id = ? AND workspace_id = ?", UUIDString(arg.ID), UUIDString(arg.WorkspaceID)).
			First(&stored).Error; err != nil {
			return err
		}
		out = toDBQueryFolder(stored)
		return nil
	})
	return out, err
}

// CreateSavedQuery inserts a saved query with an application-generated id.
func (r *Repo) CreateSavedQuery(ctx context.Context, arg db.CreateSavedQueryParams) (db.SavedQuery, error) {
	now := time.Now().UTC()
	row := model.SavedQuery{
		ID:              model.UUID(NewUUID()),
		WorkspaceID:     model.UUID(UUIDString(arg.WorkspaceID)),
		FolderID:        modelUUIDPtr(arg.FolderID),
		CreatedBy:       model.UUID(UUIDString(arg.CreatedBy)),
		Name:            model.Text(arg.Name),
		SQL:             arg.Sql,
		CreatedAt:       now,
		UpdatedAt:       now,
		SpaceID:         model.UUID(UUIDString(arg.SpaceID)),
		UpdatedBy:       modelUUIDPtr(arg.UpdatedBy),
		TargetBindingID: modelUUIDPtr(arg.TargetBindingID),
		TargetSchema:    arg.TargetSchema,
		Slug:            model.Text(arg.Slug),
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.SavedQuery{}, contentDuplicate(err)
	}
	return toDBSavedQuery(row), nil
}

// GetSavedQueryByID returns one query or pgx.ErrNoRows.
func (r *Repo) GetSavedQueryByID(ctx context.Context, arg db.GetSavedQueryByIDParams) (db.SavedQuery, error) {
	return r.getSavedQuery(ctx, arg.ID, arg.WorkspaceID)
}

// GetSavedQueryBySlug returns the workspace's query by slug or pgx.ErrNoRows.
func (r *Repo) GetSavedQueryBySlug(ctx context.Context, arg db.GetSavedQueryBySlugParams) (db.SavedQuery, error) {
	if !arg.WorkspaceID.Valid {
		return db.SavedQuery{}, pgx.ErrNoRows
	}
	var row model.SavedQuery
	err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND slug = ?", UUIDString(arg.WorkspaceID), arg.Slug).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.SavedQuery{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.SavedQuery{}, err
	}
	return toDBSavedQuery(row), nil
}

// GetSavedQueryForUpdate returns one query with its row locked until the
// surrounding transaction ends, or pgx.ErrNoRows.
func (r *Repo) GetSavedQueryForUpdate(ctx context.Context, arg db.GetSavedQueryForUpdateParams) (db.SavedQuery, error) {
	return r.lockSavedQuery(ctx, arg.ID, arg.WorkspaceID)
}

// UpdateSavedQuery edits a query and returns the row.
func (r *Repo) UpdateSavedQuery(ctx context.Context, arg db.UpdateSavedQueryParams) (db.SavedQuery, error) {
	updates := map[string]any{
		"name":              arg.Name,
		"sql":               arg.Sql,
		"folder_id":         modelUUIDPtr(arg.FolderID),
		"space_id":          model.UUID(UUIDString(arg.SpaceID)),
		"slug":              arg.Slug,
		"target_binding_id": modelUUIDPtr(arg.TargetBindingID),
		"target_schema":     arg.TargetSchema,
		"updated_by":        modelUUIDPtr(arg.UpdatedBy),
		"updated_at":        time.Now().UTC(),
	}
	if err := r.Users.db.WithContext(ctx).Model(&model.SavedQuery{}).
		Where("id = ? AND workspace_id = ?", UUIDString(arg.ID), UUIDString(arg.WorkspaceID)).
		Updates(updates).Error; err != nil {
		return db.SavedQuery{}, contentDuplicate(err)
	}
	return r.getSavedQuery(ctx, arg.ID, arg.WorkspaceID)
}

// DeleteSavedQuery removes a query and returns the rows.
func (r *Repo) DeleteSavedQuery(ctx context.Context, arg db.DeleteSavedQueryParams) (int64, error) {
	res := r.Users.db.WithContext(ctx).
		Where("id = ? AND workspace_id = ?", UUIDString(arg.ID), UUIDString(arg.WorkspaceID)).
		Delete(&model.SavedQuery{})
	return res.RowsAffected, res.Error
}

// RestoreSavedQuery recreates a deleted query with its original id and now's
// timestamps.
func (r *Repo) RestoreSavedQuery(ctx context.Context, arg db.RestoreSavedQueryParams) (db.SavedQuery, error) {
	now := time.Now().UTC()
	row := model.SavedQuery{
		ID:              model.UUID(UUIDString(arg.ID)),
		WorkspaceID:     model.UUID(UUIDString(arg.WorkspaceID)),
		FolderID:        modelUUIDPtr(arg.FolderID),
		CreatedBy:       model.UUID(UUIDString(arg.CreatedBy)),
		Name:            model.Text(arg.Name),
		SQL:             arg.Sql,
		CreatedAt:       now,
		UpdatedAt:       now,
		SpaceID:         model.UUID(UUIDString(arg.SpaceID)),
		UpdatedBy:       modelUUIDPtr(arg.UpdatedBy),
		TargetBindingID: modelUUIDPtr(arg.TargetBindingID),
		TargetSchema:    arg.TargetSchema,
		Slug:            model.Text(arg.Slug),
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.SavedQuery{}, contentDuplicate(err)
	}
	return toDBSavedQuery(row), nil
}

// ListSpaceSavedQueries returns a space's queries by slug.
func (r *Repo) ListSpaceSavedQueries(ctx context.Context, spaceID pgtype.UUID) ([]db.SavedQuery, error) {
	var rows []model.SavedQuery
	if err := r.Users.db.WithContext(ctx).
		Where("space_id = ?", UUIDString(spaceID)).
		Order("slug").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.SavedQuery, len(rows))
	for i, row := range rows {
		out[i] = toDBSavedQuery(row)
	}
	return out, nil
}

// ListSpaceFolders returns a space's folders by slug.
func (r *Repo) ListSpaceFolders(ctx context.Context, spaceID pgtype.UUID) ([]db.QueryFolder, error) {
	var rows []model.QueryFolder
	if err := r.Users.db.WithContext(ctx).
		Where("space_id = ?", UUIDString(spaceID)).
		Order("slug").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.QueryFolder, len(rows))
	for i, row := range rows {
		out[i] = toDBQueryFolder(row)
	}
	return out, nil
}

// ── semantic metrics ──────────────────────────────────────────────────────────

// CreateSemanticMetric inserts a metric with an application-generated id.
func (r *Repo) CreateSemanticMetric(ctx context.Context, arg db.CreateSemanticMetricParams) (db.SemanticMetric, error) {
	row := model.SemanticMetric{
		ID:             model.UUID(NewUUID()),
		WorkspaceID:    model.UUID(UUIDString(arg.WorkspaceID)),
		Slug:           model.Text(arg.Slug),
		Label:          arg.Label,
		Description:    model.Text(arg.Description),
		Certified:      arg.Certified,
		CatalogID:      model.UUID(UUIDString(arg.CatalogID)),
		Namespace:      model.StringArray(arg.Namespace),
		TableNameValue: model.Text(arg.TableName),
		BindingID:      modelUUIDPtr(arg.BindingID),
		Definition:     model.JSON(arg.Definition),
		FormatVersion:  arg.FormatVersion,
		OwnerUserID:    modelUUIDPtr(arg.OwnerUserID),
		UpdatedBy:      model.UUID(UUIDString(arg.UpdatedBy)),
		UpdatedAt:      time.Now().UTC(),
		SpaceID:        model.UUID(UUIDString(arg.SpaceID)),
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.SemanticMetric{}, contentDuplicate(err)
	}
	// Read the row back: jsonb comes back canonicalized, and the service
	// compares definitions byte-for-byte to detect a no-op update.
	return r.getSemanticMetric(ctx, PGUUID(string(row.ID)), arg.WorkspaceID)
}

// GetSemanticMetric returns one metric or pgx.ErrNoRows.
func (r *Repo) GetSemanticMetric(ctx context.Context, arg db.GetSemanticMetricParams) (db.SemanticMetric, error) {
	return r.getSemanticMetric(ctx, arg.ID, arg.WorkspaceID)
}

// GetSemanticMetricBySlug returns the workspace's metric by slug.
func (r *Repo) GetSemanticMetricBySlug(ctx context.Context, arg db.GetSemanticMetricBySlugParams) (db.SemanticMetric, error) {
	if !arg.WorkspaceID.Valid {
		return db.SemanticMetric{}, pgx.ErrNoRows
	}
	var row model.SemanticMetric
	err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND slug = ?", UUIDString(arg.WorkspaceID), arg.Slug).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.SemanticMetric{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.SemanticMetric{}, err
	}
	return toDBSemanticMetric(row), nil
}

// GetSemanticMetricForUpdate returns one metric with its row locked until the
// surrounding transaction ends.
func (r *Repo) GetSemanticMetricForUpdate(ctx context.Context, arg db.GetSemanticMetricForUpdateParams) (db.SemanticMetric, error) {
	return r.lockSemanticMetric(ctx, arg.ID, arg.WorkspaceID)
}

// UpdateSemanticMetric edits a metric and returns the row.
func (r *Repo) UpdateSemanticMetric(ctx context.Context, arg db.UpdateSemanticMetricParams) (db.SemanticMetric, error) {
	updates := map[string]any{
		"space_id":       model.UUID(UUIDString(arg.SpaceID)),
		"slug":           arg.Slug,
		"label":          arg.Label,
		"description":    arg.Description,
		"certified":      arg.Certified,
		"catalog_id":     model.UUID(UUIDString(arg.CatalogID)),
		"namespace":      model.StringArray(arg.Namespace),
		"table_name":     arg.TableName,
		"binding_id":     modelUUIDPtr(arg.BindingID),
		"definition":     model.JSON(arg.Definition),
		"format_version": arg.FormatVersion,
		"owner_user_id":  modelUUIDPtr(arg.OwnerUserID),
		"updated_by":     model.UUID(UUIDString(arg.UpdatedBy)),
		"updated_at":     time.Now().UTC(),
	}
	if err := r.Users.db.WithContext(ctx).Model(&model.SemanticMetric{}).
		Where("workspace_id = ? AND id = ?", UUIDString(arg.WorkspaceID), UUIDString(arg.ID)).
		Updates(updates).Error; err != nil {
		return db.SemanticMetric{}, contentDuplicate(err)
	}
	return r.getSemanticMetric(ctx, arg.ID, arg.WorkspaceID)
}

// DeleteSemanticMetric removes a metric and returns the rows.
func (r *Repo) DeleteSemanticMetric(ctx context.Context, arg db.DeleteSemanticMetricParams) (int64, error) {
	res := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND id = ?", UUIDString(arg.WorkspaceID), UUIDString(arg.ID)).
		Delete(&model.SemanticMetric{})
	return res.RowsAffected, res.Error
}

// RestoreSemanticMetric recreates a deleted metric with its original id.
func (r *Repo) RestoreSemanticMetric(ctx context.Context, arg db.RestoreSemanticMetricParams) (db.SemanticMetric, error) {
	row := model.SemanticMetric{
		ID:             model.UUID(UUIDString(arg.ID)),
		WorkspaceID:    model.UUID(UUIDString(arg.WorkspaceID)),
		Slug:           model.Text(arg.Slug),
		Label:          arg.Label,
		Description:    model.Text(arg.Description),
		Certified:      arg.Certified,
		CatalogID:      model.UUID(UUIDString(arg.CatalogID)),
		Namespace:      model.StringArray(arg.Namespace),
		TableNameValue: model.Text(arg.TableName),
		BindingID:      modelUUIDPtr(arg.BindingID),
		Definition:     model.JSON(arg.Definition),
		FormatVersion:  arg.FormatVersion,
		OwnerUserID:    modelUUIDPtr(arg.OwnerUserID),
		UpdatedBy:      model.UUID(UUIDString(arg.UpdatedBy)),
		UpdatedAt:      time.Now().UTC(),
		SpaceID:        model.UUID(UUIDString(arg.SpaceID)),
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.SemanticMetric{}, contentDuplicate(err)
	}
	return r.getSemanticMetric(ctx, PGUUID(string(row.ID)), arg.WorkspaceID)
}

// ListSpaceMetrics returns a space's metrics by slug.
func (r *Repo) ListSpaceMetrics(ctx context.Context, spaceID pgtype.UUID) ([]db.SemanticMetric, error) {
	var rows []model.SemanticMetric
	if err := r.Users.db.WithContext(ctx).
		Where("space_id = ?", UUIDString(spaceID)).
		Order("slug").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.SemanticMetric, len(rows))
	for i, row := range rows {
		out[i] = toDBSemanticMetric(row)
	}
	return out, nil
}

// ── semantic models ───────────────────────────────────────────────────────────

// CreateSemanticModel inserts a model with an application-generated id.
func (r *Repo) CreateSemanticModel(ctx context.Context, arg db.CreateSemanticModelParams) (db.SemanticModel, error) {
	row := model.SemanticModel{
		ID:            model.UUID(NewUUID()),
		WorkspaceID:   model.UUID(UUIDString(arg.WorkspaceID)),
		SpaceID:       model.UUID(UUIDString(arg.SpaceID)),
		Slug:          model.Text(arg.Slug),
		Label:         arg.Label,
		Description:   model.Text(arg.Description),
		Certified:     arg.Certified,
		CatalogID:     model.UUID(UUIDString(arg.CatalogID)),
		BindingID:     modelUUIDPtr(arg.BindingID),
		Definition:    model.JSON(arg.Definition),
		FormatVersion: arg.FormatVersion,
		OwnerUserID:   modelUUIDPtr(arg.OwnerUserID),
		UpdatedBy:     model.UUID(UUIDString(arg.UpdatedBy)),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.SemanticModel{}, contentDuplicate(err)
	}
	// Read the row back so the definition is the stored (canonical) JSON.
	return r.getSemanticModel(ctx, PGUUID(string(row.ID)), arg.WorkspaceID)
}

// GetSemanticModel returns one model or pgx.ErrNoRows.
func (r *Repo) GetSemanticModel(ctx context.Context, arg db.GetSemanticModelParams) (db.SemanticModel, error) {
	return r.getSemanticModel(ctx, arg.ID, arg.WorkspaceID)
}

// GetSemanticModelBySlug returns the workspace's model by slug.
func (r *Repo) GetSemanticModelBySlug(ctx context.Context, arg db.GetSemanticModelBySlugParams) (db.SemanticModel, error) {
	if !arg.WorkspaceID.Valid {
		return db.SemanticModel{}, pgx.ErrNoRows
	}
	var row model.SemanticModel
	err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND slug = ?", UUIDString(arg.WorkspaceID), arg.Slug).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.SemanticModel{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.SemanticModel{}, err
	}
	return toDBSemanticModel(row), nil
}

// GetSemanticModelForUpdate returns one model with its row locked until the
// surrounding transaction ends.
func (r *Repo) GetSemanticModelForUpdate(ctx context.Context, arg db.GetSemanticModelForUpdateParams) (db.SemanticModel, error) {
	return r.lockSemanticModel(ctx, arg.ID, arg.WorkspaceID)
}

// UpdateSemanticModel edits a model and returns the row.
func (r *Repo) UpdateSemanticModel(ctx context.Context, arg db.UpdateSemanticModelParams) (db.SemanticModel, error) {
	updates := map[string]any{
		"space_id":       model.UUID(UUIDString(arg.SpaceID)),
		"slug":           arg.Slug,
		"label":          arg.Label,
		"description":    arg.Description,
		"certified":      arg.Certified,
		"catalog_id":     model.UUID(UUIDString(arg.CatalogID)),
		"binding_id":     modelUUIDPtr(arg.BindingID),
		"definition":     model.JSON(arg.Definition),
		"format_version": arg.FormatVersion,
		"owner_user_id":  modelUUIDPtr(arg.OwnerUserID),
		"updated_by":     model.UUID(UUIDString(arg.UpdatedBy)),
		"updated_at":     time.Now().UTC(),
	}
	if err := r.Users.db.WithContext(ctx).Model(&model.SemanticModel{}).
		Where("workspace_id = ? AND id = ?", UUIDString(arg.WorkspaceID), UUIDString(arg.ID)).
		Updates(updates).Error; err != nil {
		return db.SemanticModel{}, contentDuplicate(err)
	}
	return r.getSemanticModel(ctx, arg.ID, arg.WorkspaceID)
}

// DeleteSemanticModel removes a model and returns the rows.
func (r *Repo) DeleteSemanticModel(ctx context.Context, arg db.DeleteSemanticModelParams) (int64, error) {
	res := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND id = ?", UUIDString(arg.WorkspaceID), UUIDString(arg.ID)).
		Delete(&model.SemanticModel{})
	return res.RowsAffected, res.Error
}

// RestoreSemanticModel recreates a deleted model with its original id.
func (r *Repo) RestoreSemanticModel(ctx context.Context, arg db.RestoreSemanticModelParams) (db.SemanticModel, error) {
	row := model.SemanticModel{
		ID:            model.UUID(UUIDString(arg.ID)),
		WorkspaceID:   model.UUID(UUIDString(arg.WorkspaceID)),
		SpaceID:       model.UUID(UUIDString(arg.SpaceID)),
		Slug:          model.Text(arg.Slug),
		Label:         arg.Label,
		Description:   model.Text(arg.Description),
		Certified:     arg.Certified,
		CatalogID:     model.UUID(UUIDString(arg.CatalogID)),
		BindingID:     modelUUIDPtr(arg.BindingID),
		Definition:    model.JSON(arg.Definition),
		FormatVersion: arg.FormatVersion,
		OwnerUserID:   modelUUIDPtr(arg.OwnerUserID),
		UpdatedBy:     model.UUID(UUIDString(arg.UpdatedBy)),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.SemanticModel{}, contentDuplicate(err)
	}
	return r.getSemanticModel(ctx, PGUUID(string(row.ID)), arg.WorkspaceID)
}

// ListSpaceModels returns a space's models by label.
func (r *Repo) ListSpaceModels(ctx context.Context, spaceID pgtype.UUID) ([]db.SemanticModel, error) {
	var rows []model.SemanticModel
	if err := r.Users.db.WithContext(ctx).
		Where("space_id = ?", UUIDString(spaceID)).
		Order("label").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.SemanticModel, len(rows))
	for i, row := range rows {
		out[i] = toDBSemanticModel(row)
	}
	return out, nil
}

// ── content versions ──────────────────────────────────────────────────────────

// InsertContentVersion appends one version. The id is the table's sequence.
func (r *Repo) InsertContentVersion(ctx context.Context, arg db.InsertContentVersionParams) (db.ContentVersion, error) {
	row := model.ContentVersion{
		WorkspaceID:  model.UUID(UUIDString(arg.WorkspaceID)),
		SpaceID:      model.UUID(UUIDString(arg.SpaceID)),
		EntityType:   model.Text(arg.EntityType),
		EntityID:     model.UUID(UUIDString(arg.EntityID)),
		Version:      arg.Version,
		ChangeKind:   arg.ChangeKind,
		Snapshot:     model.JSON(arg.Snapshot),
		AuthorID:     modelUUIDPtr(arg.AuthorID),
		RestoredFrom: arg.RestoredFrom,
		CreatedAt:    time.Now().UTC(),
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.ContentVersion{}, err
	}
	return toDBContentVersion(row), nil
}

// NextContentVersion is one past the entity's highest version; one when the
// entity has no versions yet.
func (r *Repo) NextContentVersion(ctx context.Context, arg db.NextContentVersionParams) (int32, error) {
	var row model.ContentVersion
	err := r.Users.db.WithContext(ctx).
		Select("version").
		Where("entity_type = ? AND entity_id = ?", arg.EntityType, UUIDString(arg.EntityID)).
		Order("version DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	return row.Version + 1, nil
}

// LatestContentVersion returns the entity's newest version.
func (r *Repo) LatestContentVersion(ctx context.Context, arg db.LatestContentVersionParams) (db.ContentVersion, error) {
	var row model.ContentVersion
	err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND entity_type = ? AND entity_id = ?",
			UUIDString(arg.WorkspaceID), arg.EntityType, UUIDString(arg.EntityID)).
		Order("version DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.ContentVersion{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.ContentVersion{}, err
	}
	return toDBContentVersion(row), nil
}

// ListContentVersions returns a page of the entity's versions, newest first,
// with each version's author name.
func (r *Repo) ListContentVersions(ctx context.Context, arg db.ListContentVersionsParams) ([]db.ListContentVersionsRow, error) {
	var rows []model.ContentVersion
	if err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND entity_type = ? AND entity_id = ?",
			UUIDString(arg.WorkspaceID), arg.EntityType, UUIDString(arg.EntityID)).
		Order("version DESC").
		Offset(int(arg.Off)).
		Limit(int(arg.Lim)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	names, err := r.authorNames(ctx, rows)
	if err != nil {
		return nil, err
	}
	out := make([]db.ListContentVersionsRow, len(rows))
	for i, row := range rows {
		out[i] = toDBListContentVersionsRow(row, names[modelUUIDValue(row.AuthorID)])
	}
	return out, nil
}

// GetContentVersion returns one version with its author name.
func (r *Repo) GetContentVersion(ctx context.Context, arg db.GetContentVersionParams) (db.GetContentVersionRow, error) {
	var row model.ContentVersion
	err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND entity_type = ? AND entity_id = ? AND version = ?",
			UUIDString(arg.WorkspaceID), arg.EntityType, UUIDString(arg.EntityID), arg.Version).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetContentVersionRow{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.GetContentVersionRow{}, err
	}
	names, err := r.authorNames(ctx, []model.ContentVersion{row})
	if err != nil {
		return db.GetContentVersionRow{}, err
	}
	return db.GetContentVersionRow{
		ID:           row.ID,
		WorkspaceID:  PGUUID(string(row.WorkspaceID)),
		SpaceID:      PGUUID(string(row.SpaceID)),
		EntityType:   string(row.EntityType),
		EntityID:     PGUUID(string(row.EntityID)),
		Version:      row.Version,
		ChangeKind:   row.ChangeKind,
		Snapshot:     []byte(row.Snapshot),
		AuthorID:     uuidPtr(row.AuthorID),
		RestoredFrom: row.RestoredFrom,
		CreatedAt:    PGTime(row.CreatedAt),
		AuthorName:   names[modelUUIDValue(row.AuthorID)],
	}, nil
}

// ListDeletedContent returns the workspace's entities whose latest
// delete/restore version is a delete, newest first, with author and space
// names. The latest-event-per-entity reduction runs in Go so the query needs
// no DISTINCT ON or window function (ADR 0020).
func (r *Repo) ListDeletedContent(ctx context.Context, arg db.ListDeletedContentParams) ([]db.ListDeletedContentRow, error) {
	var rows []model.ContentVersion
	if err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND change_kind IN ?", UUIDString(arg.WorkspaceID), []string{"delete", "restore"}).
		Order("entity_type, entity_id, version DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	latest := make(map[string]model.ContentVersion, len(rows))
	var order []string
	for _, row := range rows {
		key := string(row.EntityType) + "\x00" + string(row.EntityID)
		if _, seen := latest[key]; seen {
			continue
		}
		latest[key] = row
		if row.ChangeKind == "delete" && (arg.EntityType == nil || *arg.EntityType == string(row.EntityType)) {
			order = append(order, key)
		}
	}
	deleted := make([]model.ContentVersion, 0, len(order))
	for _, key := range order {
		deleted = append(deleted, latest[key])
	}
	sort.SliceStable(deleted, func(i, j int) bool { return deleted[i].CreatedAt.After(deleted[j].CreatedAt) })

	start := int(arg.Off)
	if start > len(deleted) {
		start = len(deleted)
	}
	end := start + int(arg.Lim)
	if end > len(deleted) {
		end = len(deleted)
	}
	page := deleted[start:end]

	names, err := r.authorNames(ctx, page)
	if err != nil {
		return nil, err
	}
	spaceNames, err := r.spaceNames(ctx, page)
	if err != nil {
		return nil, err
	}
	out := make([]db.ListDeletedContentRow, len(page))
	for i, row := range page {
		out[i] = db.ListDeletedContentRow{
			ID:           row.ID,
			WorkspaceID:  PGUUID(string(row.WorkspaceID)),
			SpaceID:      PGUUID(string(row.SpaceID)),
			EntityType:   string(row.EntityType),
			EntityID:     PGUUID(string(row.EntityID)),
			Version:      row.Version,
			ChangeKind:   row.ChangeKind,
			Snapshot:     []byte(row.Snapshot),
			AuthorID:     uuidPtr(row.AuthorID),
			RestoredFrom: row.RestoredFrom,
			CreatedAt:    PGTime(row.CreatedAt),
			AuthorName:   names[modelUUIDValue(row.AuthorID)],
			SpaceName:    spaceNames[string(row.SpaceID)],
		}
	}
	return out, nil
}

// ── catalogs, bindings and spaces ─────────────────────────────────────────────

// GetBindingTargetNames returns the catalog and engine names of a binding.
func (r *Repo) GetBindingTargetNames(ctx context.Context, id pgtype.UUID) (db.GetBindingTargetNamesRow, error) {
	var binding model.CatalogBinding
	err := r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetBindingTargetNamesRow{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.GetBindingTargetNamesRow{}, err
	}
	var catalog model.Catalog
	err = r.Users.db.WithContext(ctx).Where("id = ?", string(binding.CatalogID)).First(&catalog).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetBindingTargetNamesRow{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.GetBindingTargetNamesRow{}, err
	}
	var engine model.Engine
	err = r.Users.db.WithContext(ctx).Where("id = ?", string(binding.EngineID)).First(&engine).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetBindingTargetNamesRow{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.GetBindingTargetNamesRow{}, err
	}
	return db.GetBindingTargetNamesRow{CatalogName: string(catalog.Name), EngineName: string(engine.Name)}, nil
}

// GetCatalogNameByID returns a catalog's logical name.
func (r *Repo) GetCatalogNameByID(ctx context.Context, id pgtype.UUID) (string, error) {
	var row model.Catalog
	err := r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", pgx.ErrNoRows
	}
	if err != nil {
		return "", err
	}
	return string(row.Name), nil
}

// GetGrantedCatalogByName returns the id of an enabled catalog the workspace
// was granted, by its logical name.
func (r *Repo) GetGrantedCatalogByName(ctx context.Context, arg db.GetGrantedCatalogByNameParams) (pgtype.UUID, error) {
	granted := r.Users.db.Model(&model.WorkspaceCatalogGrant{}).
		Select("catalog_id").
		Where("workspace_id = ?", UUIDString(arg.WorkspaceID))
	var row model.Catalog
	err := r.Users.db.WithContext(ctx).
		Where("name = ? AND enabled = ? AND id IN (?)", arg.Name, true, granted).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return pgtype.UUID{}, pgx.ErrNoRows
	}
	if err != nil {
		return pgtype.UUID{}, err
	}
	return PGUUID(string(row.ID)), nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (r *Repo) getQueryFolder(ctx context.Context, id, workspaceID pgtype.UUID) (db.QueryFolder, error) {
	if !id.Valid || !workspaceID.Valid {
		return db.QueryFolder{}, pgx.ErrNoRows
	}
	var row model.QueryFolder
	err := r.Users.db.WithContext(ctx).
		Where("id = ? AND workspace_id = ?", UUIDString(id), UUIDString(workspaceID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.QueryFolder{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.QueryFolder{}, err
	}
	return toDBQueryFolder(row), nil
}

func (r *Repo) getSavedQuery(ctx context.Context, id, workspaceID pgtype.UUID) (db.SavedQuery, error) {
	if !id.Valid || !workspaceID.Valid {
		return db.SavedQuery{}, pgx.ErrNoRows
	}
	var row model.SavedQuery
	err := r.Users.db.WithContext(ctx).
		Where("id = ? AND workspace_id = ?", UUIDString(id), UUIDString(workspaceID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.SavedQuery{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.SavedQuery{}, err
	}
	return toDBSavedQuery(row), nil
}

func (r *Repo) lockSavedQuery(ctx context.Context, id, workspaceID pgtype.UUID) (db.SavedQuery, error) {
	if !id.Valid || !workspaceID.Valid {
		return db.SavedQuery{}, pgx.ErrNoRows
	}
	var row model.SavedQuery
	err := r.Users.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND workspace_id = ?", UUIDString(id), UUIDString(workspaceID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.SavedQuery{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.SavedQuery{}, err
	}
	return toDBSavedQuery(row), nil
}

func (r *Repo) getSemanticMetric(ctx context.Context, id, workspaceID pgtype.UUID) (db.SemanticMetric, error) {
	if !id.Valid || !workspaceID.Valid {
		return db.SemanticMetric{}, pgx.ErrNoRows
	}
	var row model.SemanticMetric
	err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND id = ?", UUIDString(workspaceID), UUIDString(id)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.SemanticMetric{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.SemanticMetric{}, err
	}
	return toDBSemanticMetric(row), nil
}

func (r *Repo) lockSemanticMetric(ctx context.Context, id, workspaceID pgtype.UUID) (db.SemanticMetric, error) {
	if !id.Valid || !workspaceID.Valid {
		return db.SemanticMetric{}, pgx.ErrNoRows
	}
	var row model.SemanticMetric
	err := r.Users.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("workspace_id = ? AND id = ?", UUIDString(workspaceID), UUIDString(id)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.SemanticMetric{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.SemanticMetric{}, err
	}
	return toDBSemanticMetric(row), nil
}

func (r *Repo) getSemanticModel(ctx context.Context, id, workspaceID pgtype.UUID) (db.SemanticModel, error) {
	if !id.Valid || !workspaceID.Valid {
		return db.SemanticModel{}, pgx.ErrNoRows
	}
	var row model.SemanticModel
	err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND id = ?", UUIDString(workspaceID), UUIDString(id)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.SemanticModel{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.SemanticModel{}, err
	}
	return toDBSemanticModel(row), nil
}

func (r *Repo) lockSemanticModel(ctx context.Context, id, workspaceID pgtype.UUID) (db.SemanticModel, error) {
	if !id.Valid || !workspaceID.Valid {
		return db.SemanticModel{}, pgx.ErrNoRows
	}
	var row model.SemanticModel
	err := r.Users.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("workspace_id = ? AND id = ?", UUIDString(workspaceID), UUIDString(id)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.SemanticModel{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.SemanticModel{}, err
	}
	return toDBSemanticModel(row), nil
}

// authorNames resolves the author ids of the given versions to user names.
func (r *Repo) authorNames(ctx context.Context, rows []model.ContentVersion) (map[string]*string, error) {
	ids := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		id := modelUUIDValue(row.AuthorID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	out := map[string]*string{}
	if len(ids) == 0 {
		return out, nil
	}
	var users []model.User
	if err := r.Users.db.WithContext(ctx).Select("id", "name").Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	for _, user := range users {
		name := user.Name
		out[string(user.ID)] = &name
	}
	return out, nil
}

// spaceNames resolves the space ids of the given versions to space names.
func (r *Repo) spaceNames(ctx context.Context, rows []model.ContentVersion) (map[string]*string, error) {
	ids := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		id := string(row.SpaceID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	out := map[string]*string{}
	if len(ids) == 0 {
		return out, nil
	}
	var spaces []model.Space
	if err := r.Users.db.WithContext(ctx).Select("id", "name").Where("id IN ?", ids).Find(&spaces).Error; err != nil {
		return nil, err
	}
	for _, space := range spaces {
		name := string(space.Name)
		out[string(space.ID)] = &name
	}
	return out, nil
}

// contentDuplicate reports a unique-constraint violation with PostgreSQL's
// SQLSTATE so the content service can map it to ErrNameConflict on every
// backend.
func contentDuplicate(err error) error {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return &pgconn.PgError{Code: uniqueViolation, Message: "duplicate key value violates unique constraint"}
	}
	return err
}

func modelUUIDValue(u *model.UUID) string {
	if u == nil {
		return ""
	}
	return string(*u)
}

func toDBQueryFolder(f model.QueryFolder) db.QueryFolder {
	return db.QueryFolder{
		ID:          PGUUID(string(f.ID)),
		WorkspaceID: PGUUID(string(f.WorkspaceID)),
		ParentID:    uuidPtr(f.ParentID),
		Name:        f.Name,
		CreatedAt:   PGTime(f.CreatedAt),
		SpaceID:     PGUUID(string(f.SpaceID)),
		Slug:        string(f.Slug),
	}
}

func toDBSavedQuery(q model.SavedQuery) db.SavedQuery {
	return db.SavedQuery{
		ID:              PGUUID(string(q.ID)),
		WorkspaceID:     PGUUID(string(q.WorkspaceID)),
		FolderID:        uuidPtr(q.FolderID),
		CreatedBy:       PGUUID(string(q.CreatedBy)),
		Name:            string(q.Name),
		Sql:             q.SQL,
		CreatedAt:       PGTime(q.CreatedAt),
		UpdatedAt:       PGTime(q.UpdatedAt),
		SpaceID:         PGUUID(string(q.SpaceID)),
		UpdatedBy:       uuidPtr(q.UpdatedBy),
		TargetBindingID: uuidPtr(q.TargetBindingID),
		TargetSchema:    q.TargetSchema,
		Slug:            string(q.Slug),
	}
}

func toDBSemanticMetric(m model.SemanticMetric) db.SemanticMetric {
	return db.SemanticMetric{
		ID:            PGUUID(string(m.ID)),
		WorkspaceID:   PGUUID(string(m.WorkspaceID)),
		Slug:          string(m.Slug),
		Label:         m.Label,
		Description:   string(m.Description),
		Certified:     m.Certified,
		CatalogID:     PGUUID(string(m.CatalogID)),
		Namespace:     []string(m.Namespace),
		TableName:     string(m.TableNameValue),
		BindingID:     uuidPtr(m.BindingID),
		Definition:    []byte(m.Definition),
		FormatVersion: m.FormatVersion,
		OwnerUserID:   uuidPtr(m.OwnerUserID),
		UpdatedBy:     PGUUID(string(m.UpdatedBy)),
		UpdatedAt:     PGTime(m.UpdatedAt),
		SpaceID:       PGUUID(string(m.SpaceID)),
		CertifiedBy:   uuidPtr(m.CertifiedBy),
		CertifiedAt:   PGTimePtr(m.CertifiedAt),
	}
}

func toDBSemanticModel(m model.SemanticModel) db.SemanticModel {
	return db.SemanticModel{
		ID:            PGUUID(string(m.ID)),
		WorkspaceID:   PGUUID(string(m.WorkspaceID)),
		SpaceID:       PGUUID(string(m.SpaceID)),
		Slug:          string(m.Slug),
		Label:         m.Label,
		Description:   string(m.Description),
		Certified:     m.Certified,
		CertifiedBy:   uuidPtr(m.CertifiedBy),
		CertifiedAt:   PGTimePtr(m.CertifiedAt),
		CatalogID:     PGUUID(string(m.CatalogID)),
		BindingID:     uuidPtr(m.BindingID),
		Definition:    []byte(m.Definition),
		FormatVersion: m.FormatVersion,
		OwnerUserID:   uuidPtr(m.OwnerUserID),
		UpdatedBy:     PGUUID(string(m.UpdatedBy)),
		UpdatedAt:     PGTime(m.UpdatedAt),
	}
}

func toDBContentVersion(v model.ContentVersion) db.ContentVersion {
	return db.ContentVersion{
		ID:           v.ID,
		WorkspaceID:  PGUUID(string(v.WorkspaceID)),
		SpaceID:      PGUUID(string(v.SpaceID)),
		EntityType:   string(v.EntityType),
		EntityID:     PGUUID(string(v.EntityID)),
		Version:      v.Version,
		ChangeKind:   v.ChangeKind,
		Snapshot:     []byte(v.Snapshot),
		AuthorID:     uuidPtr(v.AuthorID),
		RestoredFrom: v.RestoredFrom,
		CreatedAt:    PGTime(v.CreatedAt),
	}
}

func toDBListContentVersionsRow(v model.ContentVersion, authorName *string) db.ListContentVersionsRow {
	return db.ListContentVersionsRow{
		ID:           v.ID,
		WorkspaceID:  PGUUID(string(v.WorkspaceID)),
		SpaceID:      PGUUID(string(v.SpaceID)),
		EntityType:   string(v.EntityType),
		EntityID:     PGUUID(string(v.EntityID)),
		Version:      v.Version,
		ChangeKind:   v.ChangeKind,
		Snapshot:     []byte(v.Snapshot),
		AuthorID:     uuidPtr(v.AuthorID),
		RestoredFrom: v.RestoredFrom,
		CreatedAt:    PGTime(v.CreatedAt),
		AuthorName:   authorName,
	}
}
