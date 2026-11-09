package repo

import (
	"context"
	"database/sql"
	"errors"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// ── lineage reads ─────────────────────────────────────────────────────────────

// GetMetadataTableByRef returns a live table by its natural key, or
// db.ErrNoRows. A reference whose table no longer exists contributes no edge.
func (r *Repo) GetMetadataTableByRef(ctx context.Context, arg db.GetMetadataTableByRefParams) (db.GetMetadataTableByRefRow, error) {
	if !arg.CatalogID.Valid {
		return db.GetMetadataTableByRefRow{}, db.ErrNoRows
	}
	var row model.MetadataTable
	err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND namespace = ? AND name = ? AND removed_at IS NULL",
			UUIDString(arg.CatalogID), model.StringArray(arg.Namespace), arg.Name).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return db.GetMetadataTableByRefRow{}, db.ErrNoRows
		}
		return db.GetMetadataTableByRefRow{}, err
	}
	return db.GetMetadataTableByRefRow{
		ID:        PGUUID(string(row.ID)),
		CatalogID: PGUUID(string(row.CatalogID)),
		Namespace: []string(row.Namespace),
		Name:      string(row.Name),
		Kind:      row.Kind,
	}, nil
}

// ListLineageModels returns the models the caller can read in one workspace.
func (r *Repo) ListLineageModels(ctx context.Context, arg db.ListLineageModelsParams) ([]db.ListLineageModelsRow, error) {
	var out []db.ListLineageModelsRow
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		spaces, err := visibleSpaces(tx, arg.WorkspaceID, arg.UserID)
		if err != nil || len(spaces) == 0 {
			return err
		}
		var rows []model.SemanticModel
		if err := tx.Where("workspace_id = ? AND space_id IN ?", UUIDString(arg.WorkspaceID), spaces).
			Find(&rows).Error; err != nil {
			return err
		}
		out = make([]db.ListLineageModelsRow, len(rows))
		for i, row := range rows {
			out[i] = db.ListLineageModelsRow{
				ID:          PGUUID(string(row.ID)),
				WorkspaceID: PGUUID(string(row.WorkspaceID)),
				SpaceID:     PGUUID(string(row.SpaceID)),
				Slug:        string(row.Slug),
				Label:       row.Label,
				CatalogID:   PGUUID(string(row.CatalogID)),
				Definition:  []byte(row.Definition),
				UpdatedAt:   PGTime(row.UpdatedAt),
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return out, err
}

// ListLineageMetrics returns the metrics the caller can read in one workspace.
func (r *Repo) ListLineageMetrics(ctx context.Context, arg db.ListLineageMetricsParams) ([]db.ListLineageMetricsRow, error) {
	var out []db.ListLineageMetricsRow
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		spaces, err := visibleSpaces(tx, arg.WorkspaceID, arg.UserID)
		if err != nil || len(spaces) == 0 {
			return err
		}
		var rows []model.SemanticMetric
		if err := tx.Where("workspace_id = ? AND space_id IN ?", UUIDString(arg.WorkspaceID), spaces).
			Find(&rows).Error; err != nil {
			return err
		}
		out = make([]db.ListLineageMetricsRow, len(rows))
		for i, row := range rows {
			out[i] = db.ListLineageMetricsRow{
				ID:          PGUUID(string(row.ID)),
				WorkspaceID: PGUUID(string(row.WorkspaceID)),
				SpaceID:     PGUUID(string(row.SpaceID)),
				Slug:        string(row.Slug),
				Label:       row.Label,
				CatalogID:   PGUUID(string(row.CatalogID)),
				Namespace:   []string(row.Namespace),
				TableName:   string(row.TableNameValue),
				UpdatedAt:   PGTime(row.UpdatedAt),
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return out, err
}

// ListLineageSavedQueries returns the saved queries the caller can read in one
// workspace, with the catalog their target binding points at.
func (r *Repo) ListLineageSavedQueries(ctx context.Context, arg db.ListLineageSavedQueriesParams) ([]db.ListLineageSavedQueriesRow, error) {
	var out []db.ListLineageSavedQueriesRow
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		spaces, err := visibleSpaces(tx, arg.WorkspaceID, arg.UserID)
		if err != nil || len(spaces) == 0 {
			return err
		}
		var rows []model.SavedQuery
		if err := tx.Where("workspace_id = ? AND space_id IN ?", UUIDString(arg.WorkspaceID), spaces).
			Find(&rows).Error; err != nil {
			return err
		}
		bindingIDs := make([]string, 0, len(rows))
		for _, row := range rows {
			if row.TargetBindingID != nil {
				bindingIDs = append(bindingIDs, string(*row.TargetBindingID))
			}
		}
		catalogByBinding := map[string]string{}
		if len(bindingIDs) > 0 {
			var bindings []model.CatalogBinding
			if err := tx.Select("id", "catalog_id").Where("id IN ?", bindingIDs).Find(&bindings).Error; err != nil {
				return err
			}
			for _, binding := range bindings {
				catalogByBinding[string(binding.ID)] = string(binding.CatalogID)
			}
		}
		out = make([]db.ListLineageSavedQueriesRow, len(rows))
		for i, row := range rows {
			var target db.UUID
			if row.TargetBindingID != nil {
				target = PGUUID(catalogByBinding[string(*row.TargetBindingID)])
			}
			out[i] = db.ListLineageSavedQueriesRow{
				ID:              PGUUID(string(row.ID)),
				WorkspaceID:     PGUUID(string(row.WorkspaceID)),
				SpaceID:         PGUUID(string(row.SpaceID)),
				Slug:            string(row.Slug),
				Name:            string(row.Name),
				Sql:             row.SQL,
				TargetSchema:    row.TargetSchema,
				TargetCatalogID: target,
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return out, err
}

// ListLineageBrokenModels returns every model, to detect a base table or
// column that no longer exists.
func (r *Repo) ListLineageBrokenModels(ctx context.Context) ([]db.ListLineageBrokenModelsRow, error) {
	var rows []model.SemanticModel
	if err := r.Users.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ListLineageBrokenModelsRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListLineageBrokenModelsRow{
			ID:          PGUUID(string(row.ID)),
			WorkspaceID: PGUUID(string(row.WorkspaceID)),
			SpaceID:     PGUUID(string(row.SpaceID)),
			Slug:        string(row.Slug),
			Label:       row.Label,
			CatalogID:   PGUUID(string(row.CatalogID)),
			Definition:  []byte(row.Definition),
			UpdatedAt:   PGTime(row.UpdatedAt),
		}
	}
	return out, nil
}

// ListLineageBrokenMetrics returns every metric, to detect a base table that
// no longer exists.
func (r *Repo) ListLineageBrokenMetrics(ctx context.Context) ([]db.ListLineageBrokenMetricsRow, error) {
	var rows []model.SemanticMetric
	if err := r.Users.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ListLineageBrokenMetricsRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListLineageBrokenMetricsRow{
			ID:          PGUUID(string(row.ID)),
			WorkspaceID: PGUUID(string(row.WorkspaceID)),
			SpaceID:     PGUUID(string(row.SpaceID)),
			Slug:        string(row.Slug),
			Label:       row.Label,
			CatalogID:   PGUUID(string(row.CatalogID)),
			Namespace:   []string(row.Namespace),
			TableName:   string(row.TableNameValue),
			UpdatedAt:   PGTime(row.UpdatedAt),
		}
	}
	return out, nil
}
