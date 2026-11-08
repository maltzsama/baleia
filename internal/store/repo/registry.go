package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ── trino catalog registry ───────────────────────────────────────────────────

// UpsertTrinoCatalogRegistry writes the plugin's registry row for a catalog,
// entering the pending state Baleia asks the plugin to confirm.
func (r *Repo) UpsertTrinoCatalogRegistry(ctx context.Context, arg db.UpsertTrinoCatalogRegistryParams) error {
	now := time.Now().UTC()
	row := model.TrinoCatalogRegistry{
		ClusterID:      model.UUID(UUIDString(arg.ClusterID)),
		CatalogName:    model.Text(arg.CatalogName),
		ConnectorName:  arg.ConnectorName,
		Properties:     jsonOrDefault(arg.Column4),
		CatalogVersion: arg.CatalogVersion,
		Enabled:        true,
		SyncStatus:     model.Text("pending"),
		UpdatedBy:      model.Text("baleia"),
		UpdatedAt:      now,
	}
	return r.Users.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "cluster_id"}, {Name: "catalog_name"}},
		DoUpdates: clause.Assignments(map[string]any{
			"connector_name":  arg.ConnectorName,
			"properties":      jsonOrDefault(arg.Column4),
			"catalog_version": arg.CatalogVersion,
			"enabled":         true,
			"sync_status":     "pending",
			"sync_error":      nil,
			"updated_by":      "baleia",
			"updated_at":      now,
		}),
	}).Create(&row).Error
}

// SoftDeleteTrinoCatalogRegistry disables a permanent registry row.
func (r *Repo) SoftDeleteTrinoCatalogRegistry(ctx context.Context, arg db.SoftDeleteTrinoCatalogRegistryParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.TrinoCatalogRegistry{}).
		Where("cluster_id = ? AND catalog_name = ?", UUIDString(arg.ClusterID), arg.CatalogName).
		Updates(map[string]any{
			"enabled":    false,
			"updated_by": "baleia",
			"updated_at": time.Now().UTC(),
		}).Error
}

// GetTrinoCatalogRegistryState returns the registry state of one catalog.
func (r *Repo) GetTrinoCatalogRegistryState(ctx context.Context, arg db.GetTrinoCatalogRegistryStateParams) (db.GetTrinoCatalogRegistryStateRow, error) {
	var row model.TrinoCatalogRegistry
	err := r.Users.db.WithContext(ctx).
		Where("cluster_id = ? AND catalog_name = ?", UUIDString(arg.ClusterID), arg.CatalogName).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetTrinoCatalogRegistryStateRow{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.GetTrinoCatalogRegistryStateRow{}, err
	}
	return db.GetTrinoCatalogRegistryStateRow{
		CatalogVersion: row.CatalogVersion,
		Enabled:        row.Enabled,
		SyncStatus:     string(row.SyncStatus),
		UpdatedBy:      string(row.UpdatedBy),
	}, nil
}

// InsertEphemeralRegistryRow writes a connection test's expiring catalog row.
func (r *Repo) InsertEphemeralRegistryRow(ctx context.Context, arg db.InsertEphemeralRegistryRowParams) error {
	now := time.Now().UTC()
	expires := now.Add(time.Duration(arg.Ttl.Microseconds) * time.Microsecond)
	row := model.TrinoCatalogRegistry{
		ClusterID:      model.UUID(UUIDString(arg.ClusterID)),
		CatalogName:    model.Text(arg.CatalogName),
		ConnectorName:  arg.ConnectorName,
		Properties:     jsonOrDefault(arg.Column4),
		CatalogVersion: arg.CatalogVersion,
		Enabled:        true,
		SyncStatus:     model.Text("pending"),
		UpdatedBy:      model.Text("baleia"),
		UpdatedAt:      now,
		ExpiresAt:      &expires,
	}
	return r.Users.db.WithContext(ctx).Create(&row).Error
}

// DeleteEphemeralRegistryRow hard-deletes a test catalog's registry row.
func (r *Repo) DeleteEphemeralRegistryRow(ctx context.Context, arg db.DeleteEphemeralRegistryRowParams) error {
	return r.Users.db.WithContext(ctx).
		Where("cluster_id = ? AND catalog_name = ? AND expires_at IS NOT NULL",
			UUIDString(arg.ClusterID), arg.CatalogName).
		Delete(&model.TrinoCatalogRegistry{}).Error
}

// ListExpiredEphemeralRegistryRows lists the test catalogs past their expiry.
func (r *Repo) ListExpiredEphemeralRegistryRows(ctx context.Context) ([]db.ListExpiredEphemeralRegistryRowsRow, error) {
	var rows []model.TrinoCatalogRegistry
	if err := r.Users.db.WithContext(ctx).
		Where("expires_at IS NOT NULL AND expires_at < ?", time.Now().UTC()).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ListExpiredEphemeralRegistryRowsRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListExpiredEphemeralRegistryRowsRow{
			ClusterID:   PGUUID(string(row.ClusterID)),
			CatalogName: string(row.CatalogName),
		}
	}
	return out, nil
}

// registryState loads the registry row behind a binding, if its engine has a
// cluster and the plugin wrote a row.
func (r *Repo) registryState(ctx context.Context, engineID model.UUID, catalogName string) (model.TrinoCatalogRegistry, bool, error) {
	var cluster model.TrinoCluster
	err := r.Users.db.WithContext(ctx).Where("engine_id = ?", string(engineID)).First(&cluster).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.TrinoCatalogRegistry{}, false, nil
	}
	if err != nil {
		return model.TrinoCatalogRegistry{}, false, err
	}
	var row model.TrinoCatalogRegistry
	err = r.Users.db.WithContext(ctx).
		Where("cluster_id = ? AND catalog_name = ?", string(cluster.ID), catalogName).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.TrinoCatalogRegistry{}, false, nil
	}
	if err != nil {
		return model.TrinoCatalogRegistry{}, false, err
	}
	return row, true, nil
}

// observationState loads the observation behind a binding, if any.
func (r *Repo) observationState(ctx context.Context, engineID model.UUID, catalogName string) (model.EngineCatalogObservation, bool, error) {
	var row model.EngineCatalogObservation
	err := r.Users.db.WithContext(ctx).
		Where("engine_id = ? AND catalog_name = ?", string(engineID), catalogName).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.EngineCatalogObservation{}, false, nil
	}
	if err != nil {
		return model.EngineCatalogObservation{}, false, err
	}
	return row, true, nil
}
