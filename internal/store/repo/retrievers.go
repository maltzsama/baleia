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

// ── retrievers (#118) ─────────────────────────────────────────────────────────

// CreateRetriever inserts a retriever and returns it.
func (r *Repo) CreateRetriever(ctx context.Context, arg db.CreateRetrieverParams) (db.Retriever, error) {
	now := time.Now().UTC()
	row := model.Retriever{
		ID:            model.UUID(NewUUID()),
		WorkspaceID:   model.UUID(UUIDString(arg.WorkspaceID)),
		CreatedBy:     model.UUID(UUIDString(arg.CreatedBy)),
		Name:          model.Text(arg.Name),
		DefinitionSQL: arg.DefinitionSql,
		DestCatalog:   arg.DestCatalog,
		DestNamespace: arg.DestNamespace,
		DestTable:     arg.DestTable,
		CronSchedule:  arg.CronSchedule,
		RefreshType:   model.Text(arg.RefreshType),
		Status:        "active",
		NextRefreshAt: TimePtr(arg.NextRefreshAt),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if arg.DestBindingID.Valid {
		id := model.UUID(UUIDString(arg.DestBindingID))
		row.DestBindingID = &id
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.Retriever{}, err
	}
	return toDBRetriever(row), nil
}

// GetRetrieverByID returns one retriever of the workspace or pgx.ErrNoRows.
func (r *Repo) GetRetrieverByID(ctx context.Context, arg db.GetRetrieverByIDParams) (db.Retriever, error) {
	row, err := r.retriever(ctx, arg.WorkspaceID, arg.ID)
	if err != nil {
		return db.Retriever{}, err
	}
	return toDBRetriever(row), nil
}

// ListRetrieversByWorkspace lists a workspace's retrievers by name.
func (r *Repo) ListRetrieversByWorkspace(ctx context.Context, workspaceID pgtype.UUID) ([]db.Retriever, error) {
	if !workspaceID.Valid {
		return nil, nil
	}
	var rows []model.Retriever
	if err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ?", UUIDString(workspaceID)).
		Order("name").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.Retriever, len(rows))
	for i, row := range rows {
		out[i] = toDBRetriever(row)
	}
	return out, nil
}

// UpdateRetriever edits a retriever of the workspace and returns it. The row
// is locked for the update and the read so a concurrent delete cannot split
// them.
func (r *Repo) UpdateRetriever(ctx context.Context, arg db.UpdateRetrieverParams) (db.Retriever, error) {
	var out db.Retriever
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.Retriever
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND workspace_id = ?", UUIDString(arg.ID), UUIDString(arg.WorkspaceID)).
			First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pgx.ErrNoRows
		}
		if err != nil {
			return err
		}
		updates := map[string]any{
			"name":            arg.Name,
			"definition_sql":  arg.DefinitionSql,
			"dest_catalog":    arg.DestCatalog,
			"dest_namespace":  arg.DestNamespace,
			"dest_table":      arg.DestTable,
			"dest_binding_id": modelUUIDPtr(arg.DestBindingID),
			"cron_schedule":   arg.CronSchedule,
			"refresh_type":    arg.RefreshType,
			"status":          arg.Status,
			"error_code":      arg.ErrorCode,
			"next_refresh_at": TimePtr(arg.NextRefreshAt),
			"updated_at":      time.Now().UTC(),
		}
		if err := tx.Model(&model.Retriever{}).Where("id = ?", string(row.ID)).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", string(row.ID)).First(&row).Error; err != nil {
			return err
		}
		out = toDBRetriever(row)
		return nil
	})
	if err != nil {
		return db.Retriever{}, err
	}
	return out, nil
}

// DeleteRetriever removes a retriever of the workspace; its refresh history
// goes with it (ON DELETE CASCADE).
func (r *Repo) DeleteRetriever(ctx context.Context, arg db.DeleteRetrieverParams) (int64, error) {
	res := r.Users.db.WithContext(ctx).
		Where("id = ? AND workspace_id = ?", UUIDString(arg.ID), UUIDString(arg.WorkspaceID)).
		Delete(&model.Retriever{})
	return res.RowsAffected, res.Error
}

// ListRetrieverDestinations lists the bindings a retriever of the workspace
// may write to (#118): a granted catalog, an Iceberg connector, an active
// binding on an enabled federated engine.
func (r *Repo) ListRetrieverDestinations(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListRetrieverDestinationsRow, error) {
	var out []db.ListRetrieverDestinationsRow
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		visible, err := grantedCatalogIDs(tx, workspaceID)
		if err != nil || len(visible) == 0 {
			return err
		}
		var rows []struct {
			BindingID         model.UUID
			EngineCatalogName string
			Connector         string
			CatalogID         model.UUID
			CatalogName       string
			EngineID          model.UUID
			EngineName        string
			EngineKind        string
		}
		err = tx.Table("catalog_bindings AS b").
			Select(`b.id AS binding_id, b.engine_catalog_name, COALESCE(b.connector, '') AS connector,
				c.id AS catalog_id, c.name AS catalog_name,
				e.id AS engine_id, e.name AS engine_name, e.kind AS engine_kind`).
			Joins("JOIN catalogs c ON c.id = b.catalog_id").
			Joins("JOIN engines e ON e.id = b.engine_id").
			Where("b.catalog_id IN ? AND b.connector = ? AND b.lifecycle_state = ? AND b.enabled AND e.enabled AND e.class = ?",
				visible, "iceberg", "active", "federated").
			Order("c.name, b.engine_catalog_name").
			Scan(&rows).Error
		if err != nil {
			return err
		}
		out = make([]db.ListRetrieverDestinationsRow, len(rows))
		for i, row := range rows {
			out[i] = db.ListRetrieverDestinationsRow{
				BindingID:         PGUUID(string(row.BindingID)),
				EngineCatalogName: row.EngineCatalogName,
				Connector:         row.Connector,
				CatalogID:         PGUUID(string(row.CatalogID)),
				CatalogName:       row.CatalogName,
				EngineID:          PGUUID(string(row.EngineID)),
				EngineName:        row.EngineName,
				EngineKind:        row.EngineKind,
			}
		}
		return nil
	})
	return out, err
}

// ListRetrieverRefreshHistory lists a retriever's latest refresh runs.
func (r *Repo) ListRetrieverRefreshHistory(ctx context.Context, arg db.ListRetrieverRefreshHistoryParams) ([]db.RetrieverRefreshHistory, error) {
	if !arg.RetrieverID.Valid || arg.Limit <= 0 {
		return nil, nil
	}
	var rows []model.RetrieverRefreshHistory
	if err := r.Users.db.WithContext(ctx).
		Where("retriever_id = ?", UUIDString(arg.RetrieverID)).
		Order("started_at DESC").
		Limit(int(arg.Limit)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.RetrieverRefreshHistory, len(rows))
	for i, row := range rows {
		out[i] = toDBRetrieverRefreshHistory(row)
	}
	return out, nil
}

// LatestRetrieverRefresh returns the latest refresh run's status or
// pgx.ErrNoRows.
func (r *Repo) LatestRetrieverRefresh(ctx context.Context, retrieverID pgtype.UUID) (db.LatestRetrieverRefreshRow, error) {
	if !retrieverID.Valid {
		return db.LatestRetrieverRefreshRow{}, pgx.ErrNoRows
	}
	var row model.RetrieverRefreshHistory
	err := r.Users.db.WithContext(ctx).
		Select("status", "error_code").
		Where("retriever_id = ?", UUIDString(retrieverID)).
		Order("started_at DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.LatestRetrieverRefreshRow{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.LatestRetrieverRefreshRow{}, err
	}
	return db.LatestRetrieverRefreshRow{Status: string(row.Status), ErrorCode: row.ErrorCode}, nil
}

func (r *Repo) retriever(ctx context.Context, workspaceID, id pgtype.UUID) (model.Retriever, error) {
	if !workspaceID.Valid || !id.Valid {
		return model.Retriever{}, pgx.ErrNoRows
	}
	var row model.Retriever
	err := r.Users.db.WithContext(ctx).
		Where("id = ? AND workspace_id = ?", UUIDString(id), UUIDString(workspaceID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.Retriever{}, pgx.ErrNoRows
	}
	if err != nil {
		return model.Retriever{}, err
	}
	return row, nil
}

func toDBRetriever(row model.Retriever) db.Retriever {
	return db.Retriever{
		ID:            PGUUID(string(row.ID)),
		WorkspaceID:   PGUUID(string(row.WorkspaceID)),
		CreatedBy:     PGUUID(string(row.CreatedBy)),
		Name:          string(row.Name),
		DefinitionSql: row.DefinitionSQL,
		DestCatalog:   row.DestCatalog,
		DestNamespace: row.DestNamespace,
		DestTable:     row.DestTable,
		CronSchedule:  row.CronSchedule,
		RefreshType:   string(row.RefreshType),
		Status:        string(row.Status),
		LastRefreshAt: PGTimePtr(row.LastRefreshAt),
		NextRefreshAt: PGTimePtr(row.NextRefreshAt),
		CreatedAt:     PGTime(row.CreatedAt),
		UpdatedAt:     PGTime(row.UpdatedAt),
		DestBindingID: uuidPtr(row.DestBindingID),
		ErrorCode:     row.ErrorCode,
	}
}

func toDBRetrieverRefreshHistory(row model.RetrieverRefreshHistory) db.RetrieverRefreshHistory {
	return db.RetrieverRefreshHistory{
		ID:                 PGUUID(string(row.ID)),
		RetrieverID:        PGUUID(string(row.RetrieverID)),
		TemporalWorkflowID: row.TemporalWorkflowID,
		RefreshType:        row.RefreshType,
		Status:             string(row.Status),
		RowsProcessed:      row.RowsProcessed,
		DurationMs:         row.DurationMs,
		ErrorMessage:       row.ErrorMessage,
		StartedAt:          PGTime(row.StartedAt),
		FinishedAt:         PGTimePtr(row.FinishedAt),
		ErrorCode:          row.ErrorCode,
	}
}
