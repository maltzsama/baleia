package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ── catalogs ──────────────────────────────────────────────────────────────────

// CreateCatalog inserts a source catalog. Its source_ref is the warehouse.
func (r *Repo) CreateCatalog(ctx context.Context, arg db.CreateCatalogParams) (db.Catalog, error) {
	now := time.Now().UTC()
	row := model.Catalog{
		ID:               model.UUID(NewUUID()),
		ConnectionID:     model.UUID(UUIDString(arg.ConnectionID)),
		Name:             model.Text(arg.Name),
		Description:      arg.Description,
		TrinoCatalogName: modelTextPtr(arg.TrinoCatalogName),
		ClusterID:        modelUUIDPtr(arg.ClusterID),
		Warehouse:        arg.Warehouse,
		Connector:        modelTextPtr(arg.Connector),
		ExtraProps:       jsonOrDefault(arg.ExtraProps),
		Enabled:          true,
		CreatedAt:        now,
		UpdatedAt:        now,
		Origin:           model.Text("source"),
		SourceRef:        arg.Warehouse,
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.Catalog{}, err
	}
	return toDBCatalog(row), nil
}

// ListCatalogsByConnection lists a connection's catalogs by name.
func (r *Repo) ListCatalogsByConnection(ctx context.Context, connectionID db.UUID) ([]db.Catalog, error) {
	var rows []model.Catalog
	if err := r.Users.db.WithContext(ctx).
		Where("connection_id = ?", UUIDString(connectionID)).
		Order("name").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return toDBCatalogs(rows), nil
}

// ListVisibleCatalogs lists every catalog to an admin, and to anyone else the
// catalogs of the connections they own.
func (r *Repo) ListVisibleCatalogs(ctx context.Context, arg db.ListVisibleCatalogsParams) ([]db.Catalog, error) {
	q := r.Users.db.WithContext(ctx).Table("catalogs AS c").Select("c.*")
	if !arg.IsAdmin {
		q = q.Joins("JOIN catalog_connections cc ON cc.id = c.connection_id").
			Where("cc.created_by = ?", uuidText(arg.UserID))
	}
	var rows []model.Catalog
	if err := q.Order("c.name").Find(&rows).Error; err != nil {
		return nil, err
	}
	return toDBCatalogs(rows), nil
}

// GetCatalogByTrinoName finds a catalog by its Trino name on a cluster.
func (r *Repo) GetCatalogByTrinoName(ctx context.Context, arg db.GetCatalogByTrinoNameParams) (db.Catalog, error) {
	var row model.Catalog
	err := r.Users.db.WithContext(ctx).
		Where("cluster_id = ? AND trino_catalog_name = ?", uuidText(arg.ClusterID), arg.TrinoCatalogName).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Catalog{}, db.ErrNoRows
	}
	if err != nil {
		return db.Catalog{}, err
	}
	return toDBCatalog(row), nil
}

// UpdateCatalog stores a catalog's new definition. A source catalog's
// source_ref follows its warehouse.
func (r *Repo) UpdateCatalog(ctx context.Context, arg db.UpdateCatalogParams) (db.Catalog, error) {
	current, err := r.GetCatalogByID(ctx, arg.ID)
	if err != nil {
		return db.Catalog{}, err
	}
	changes := map[string]any{
		"name":               arg.Name,
		"description":        arg.Description,
		"warehouse":          arg.Warehouse,
		"connector":          arg.Connector,
		"extra_props":        jsonOrDefault(arg.ExtraProps),
		"trino_catalog_name": arg.TrinoCatalogName,
		"updated_at":         time.Now().UTC(),
	}
	if current.Origin == "source" {
		changes["source_ref"] = arg.Warehouse
	}
	if err := r.Users.db.WithContext(ctx).Model(&model.Catalog{}).
		Where("id = ?", UUIDString(arg.ID)).Updates(changes).Error; err != nil {
		return db.Catalog{}, err
	}
	return r.GetCatalogByID(ctx, arg.ID)
}

// UpdateCatalogMeta renames a catalog and edits its description.
func (r *Repo) UpdateCatalogMeta(ctx context.Context, arg db.UpdateCatalogMetaParams) (db.Catalog, error) {
	if err := r.Users.db.WithContext(ctx).Model(&model.Catalog{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"name":        arg.Name,
			"description": arg.Description,
			"updated_at":  time.Now().UTC(),
		}).Error; err != nil {
		return db.Catalog{}, err
	}
	return r.GetCatalogByID(ctx, arg.ID)
}

// SetCatalogEnabled enables or disables a catalog.
func (r *Repo) SetCatalogEnabled(ctx context.Context, arg db.SetCatalogEnabledParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.Catalog{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{"enabled": arg.Enabled, "updated_at": time.Now().UTC()}).Error
}

// DeleteCatalog removes a catalog; its bindings cascade.
func (r *Repo) DeleteCatalog(ctx context.Context, id db.UUID) error {
	return r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).Delete(&model.Catalog{}).Error
}

// SetCatalogDiscoverable marks a catalog findable by workspace admins.
func (r *Repo) SetCatalogDiscoverable(ctx context.Context, arg db.SetCatalogDiscoverableParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.Catalog{}).
		Where("id = ?", UUIDString(arg.ID)).
		Update("discoverable", arg.Discoverable).Error
}

// ListDiscoverableCatalogs lists the enabled catalogs their owner published.
func (r *Repo) ListDiscoverableCatalogs(ctx context.Context) ([]db.ListDiscoverableCatalogsRow, error) {
	var rows []struct {
		ID                model.UUID
		Name              string
		EngineCatalogName string
		Description       *string
		ConnectionName    string
		CreatedBy         *model.UUID
	}
	err := r.Users.db.WithContext(ctx).
		Table("catalogs AS c").
		Select("c.id, c.name, COALESCE(c.trino_catalog_name, c.name) AS engine_catalog_name, c.description, cc.name AS connection_name, cc.created_by").
		Joins("JOIN catalog_connections cc ON cc.id = c.connection_id").
		Where("c.discoverable AND c.enabled").
		Order("c.name").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]db.ListDiscoverableCatalogsRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListDiscoverableCatalogsRow{
			ID:                PGUUID(string(row.ID)),
			Name:              row.Name,
			EngineCatalogName: row.EngineCatalogName,
			Description:       row.Description,
			ConnectionName:    row.ConnectionName,
			CreatedBy:         uuidPtr(row.CreatedBy),
		}
	}
	return out, nil
}

// GetCatalogSyncState returns the freshness of a catalog snapshot.
func (r *Repo) GetCatalogSyncState(ctx context.Context, catalogID db.UUID) (db.GetCatalogSyncStateRow, error) {
	var out db.GetCatalogSyncStateRow
	var latest model.MetadataSyncRun
	err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ?", UUIDString(catalogID)).
		Order("started_at DESC").
		First(&latest).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
	case err != nil:
		return out, err
	default:
		out.LastStatus = latest.Status
		if latest.ErrorCode != nil {
			out.LastErrorCode = *latest.ErrorCode
		}
	}
	var finished *time.Time
	if err := r.Users.db.WithContext(ctx).Model(&model.MetadataSyncRun{}).
		Where("catalog_id = ? AND status = ?", UUIDString(catalogID), "finished").
		Select("MAX(finished_at)").
		Scan(&finished).Error; err != nil {
		return db.GetCatalogSyncStateRow{}, err
	}
	out.SyncedAt = PGTimePtr(finished)
	return out, nil
}

// GetMetadataTableByName finds a live table by its natural key.
func (r *Repo) GetMetadataTableByName(ctx context.Context, arg db.GetMetadataTableByNameParams) (db.UUID, error) {
	var row model.MetadataTable
	err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND namespace = ? AND name = ? AND removed_at IS NULL",
			uuidText(arg.CatalogID), model.StringArray(arg.Namespace), arg.Name).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.UUID{}, db.ErrNoRows
	}
	if err != nil {
		return db.UUID{}, err
	}
	return PGUUID(string(row.ID)), nil
}

// ListMetadataColumns lists a table's stored columns by ordinal.
func (r *Repo) ListMetadataColumns(ctx context.Context, tableID db.UUID) ([]db.ListMetadataColumnsRow, error) {
	var rows []model.MetadataColumn
	if err := r.Users.db.WithContext(ctx).
		Where("table_id = ?", UUIDString(tableID)).
		Order("ordinal").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ListMetadataColumnsRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListMetadataColumnsRow{
			Ordinal:    row.Ordinal,
			Name:       string(row.Name),
			NativeType: row.NativeType,
			TypeSystem: row.TypeSystem,
			Nullable:   row.Nullable,
			Comment:    row.Comment,
		}
	}
	return out, nil
}

// ── bindings ──────────────────────────────────────────────────────────────────

// GetBinding returns one binding or db.ErrNoRows.
func (r *Repo) GetBinding(ctx context.Context, id db.UUID) (db.CatalogBinding, error) {
	row, err := r.getBinding(ctx, id)
	if err != nil {
		return db.CatalogBinding{}, err
	}
	return toDBCatalogBinding(row), nil
}

// ListBindingsByEngine lists an engine's bindings by engine catalog name.
func (r *Repo) ListBindingsByEngine(ctx context.Context, engineID db.UUID) ([]db.CatalogBinding, error) {
	var rows []model.CatalogBinding
	if err := r.Users.db.WithContext(ctx).
		Where("engine_id = ?", UUIDString(engineID)).
		Order("engine_catalog_name").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return toDBCatalogBindings(rows), nil
}

// ListHiveBindingsToResync lists the active hive bindings of a connection on an
// engine. Hive table redirection (#86) names only siblings active when the hive
// binding is projected, so a catalog change must re-apply these.
func (r *Repo) ListHiveBindingsToResync(ctx context.Context, connID, engineID db.UUID) ([]db.UUID, error) {
	var ids []string
	if err := r.Users.db.WithContext(ctx).
		Table("catalog_bindings AS b").
		Joins("JOIN catalogs AS c ON c.id = b.catalog_id").
		Where("b.engine_id = ? AND b.connector = ? AND b.enabled = ? AND b.lifecycle_state = ? AND c.connection_id = ?",
			UUIDString(engineID), "hive", true, "active", UUIDString(connID)).
		Pluck("b.id", &ids).Error; err != nil {
		return nil, err
	}
	out := make([]db.UUID, len(ids))
	for i, id := range ids {
		out[i] = PGUUID(id)
	}
	return out, nil
}

// GetManagedBindingOfCatalog returns a catalog's managed binding on an engine.
func (r *Repo) GetManagedBindingOfCatalog(ctx context.Context, arg db.GetManagedBindingOfCatalogParams) (db.CatalogBinding, error) {
	var row model.CatalogBinding
	err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND engine_id = ? AND mode = ?",
			UUIDString(arg.CatalogID), UUIDString(arg.EngineID), "managed").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.CatalogBinding{}, db.ErrNoRows
	}
	if err != nil {
		return db.CatalogBinding{}, err
	}
	return toDBCatalogBinding(row), nil
}

// GetBindingStatus derives the sync status the API shows for a binding.
func (r *Repo) GetBindingStatus(ctx context.Context, bindingID db.UUID) (db.BindingStatusV, error) {
	binding, err := r.getBinding(ctx, bindingID)
	if err != nil {
		return db.BindingStatusV{}, err
	}
	return r.bindingStatus(ctx, binding)
}

// CountManagedBindingsByState counts managed bindings by lifecycle state.
func (r *Repo) CountManagedBindingsByState(ctx context.Context) ([]db.CountManagedBindingsByStateRow, error) {
	var states []string
	if err := r.Users.db.WithContext(ctx).Model(&model.CatalogBinding{}).
		Where("mode = ?", "managed").
		Pluck("lifecycle_state", &states).Error; err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	for _, state := range states {
		counts[state]++
	}
	out := make([]db.CountManagedBindingsByStateRow, 0, len(counts))
	for state, count := range counts {
		out = append(out, db.CountManagedBindingsByStateRow{LifecycleState: state, Count: count})
	}
	return out, nil
}

// DeleteBinding removes a binding row.
func (r *Repo) DeleteBinding(ctx context.Context, id db.UUID) error {
	return r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).Delete(&model.CatalogBinding{}).Error
}

// DisableManagedBindingsOfCatalog disables a catalog's managed bindings.
func (r *Repo) DisableManagedBindingsOfCatalog(ctx context.Context, catalogID db.UUID) error {
	return r.Users.db.WithContext(ctx).Model(&model.CatalogBinding{}).
		Where("catalog_id = ? AND mode = ?", UUIDString(catalogID), "managed").
		Updates(map[string]any{"enabled": false, "updated_at": time.Now().UTC()}).Error
}

// ListManagedBindingsOfEngine lists the managed bindings the reconciler checks.
func (r *Repo) ListManagedBindingsOfEngine(ctx context.Context, engineID db.UUID) ([]db.CatalogBinding, error) {
	var rows []model.CatalogBinding
	if err := r.Users.db.WithContext(ctx).
		Where("engine_id = ? AND mode = ?", UUIDString(engineID), "managed").
		Order("engine_catalog_name").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return toDBCatalogBindings(rows), nil
}

// ManagedBindingNamesOfEngine lists the names of an engine's managed bindings.
func (r *Repo) ManagedBindingNamesOfEngine(ctx context.Context, engineID db.UUID) ([]string, error) {
	var names []string
	err := r.Users.db.WithContext(ctx).Model(&model.CatalogBinding{}).
		Where("engine_id = ? AND mode = ?", UUIDString(engineID), "managed").
		Pluck("engine_catalog_name", &names).Error
	return names, err
}

// UpsertManagedBinding stores the managed binding of a catalog on an engine,
// entering lifecycle_state with the projection version it wants.
func (r *Repo) UpsertManagedBinding(ctx context.Context, arg db.UpsertManagedBindingParams) (db.CatalogBinding, error) {
	now := time.Now().UTC()
	row := model.CatalogBinding{
		ID:                model.UUID(NewUUID()),
		CatalogID:         model.UUID(UUIDString(arg.CatalogID)),
		EngineID:          model.UUID(UUIDString(arg.EngineID)),
		EngineCatalogName: model.Text(arg.EngineCatalogName),
		Connector:         arg.Connector,
		ExtraProps:        jsonOrDefault(arg.ExtraProps),
		Mode:              "managed",
		Enabled:           arg.Enabled,
		CreatedAt:         now,
		UpdatedAt:         now,
		LifecycleState:    model.Text(arg.LifecycleState),
		DesiredVersion:    arg.DesiredVersion,
		StateChangedAt:    now,
	}
	err := r.Users.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "catalog_id"}, {Name: "engine_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"engine_catalog_name": arg.EngineCatalogName,
			"connector":           arg.Connector,
			"extra_props":         jsonOrDefault(arg.ExtraProps),
			"enabled":             arg.Enabled,
			"lifecycle_state":     arg.LifecycleState,
			"desired_version":     arg.DesiredVersion,
			"last_error_code":     nil,
			"last_error_message":  nil,
			"state_changed_at":    now,
			"updated_at":          now,
		}),
	}).Create(&row).Error
	if err != nil {
		return db.CatalogBinding{}, err
	}
	return r.GetManagedBindingOfCatalog(ctx, db.GetManagedBindingOfCatalogParams{
		CatalogID: arg.CatalogID, EngineID: arg.EngineID,
	})
}

// CreateManagedBinding inserts a provisioning managed binding.
func (r *Repo) CreateManagedBinding(ctx context.Context, arg db.CreateManagedBindingParams) (db.CatalogBinding, error) {
	now := time.Now().UTC()
	row := model.CatalogBinding{
		ID:                model.UUID(NewUUID()),
		CatalogID:         model.UUID(UUIDString(arg.CatalogID)),
		EngineID:          model.UUID(UUIDString(arg.EngineID)),
		EngineCatalogName: model.Text(arg.EngineCatalogName),
		Connector:         arg.Connector,
		ExtraProps:        jsonOrDefault(arg.ExtraProps),
		Mode:              "managed",
		Enabled:           true,
		CreatedAt:         now,
		UpdatedAt:         now,
		LifecycleState:    model.Text("provisioning"),
		DesiredVersion:    arg.DesiredVersion,
		StateChangedAt:    now,
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.CatalogBinding{}, err
	}
	return toDBCatalogBinding(row), nil
}

// UpdateBinding writes the desired state, guarded by the states a change may
// start from; no row means a concurrent change.
func (r *Repo) UpdateBinding(ctx context.Context, arg db.UpdateBindingParams) (db.CatalogBinding, error) {
	if !arg.ID.Valid || len(arg.FromStates) == 0 {
		return db.CatalogBinding{}, db.ErrNoRows
	}
	now := time.Now().UTC()
	changes := map[string]any{
		"engine_catalog_name": arg.EngineCatalogName,
		"connector":           arg.Connector,
		"extra_props":         jsonOrDefault(arg.ExtraProps),
		"enabled":             arg.Enabled,
		"lifecycle_state":     arg.LifecycleState,
		"last_error_code":     nil,
		"last_error_message":  nil,
		"state_changed_at":    now,
		"updated_at":          now,
	}
	if arg.DesiredVersion != nil {
		changes["desired_version"] = *arg.DesiredVersion
	}
	res := r.Users.db.WithContext(ctx).Model(&model.CatalogBinding{}).
		Where("id = ? AND lifecycle_state IN ?", UUIDString(arg.ID), arg.FromStates).
		Updates(changes)
	if res.Error != nil {
		return db.CatalogBinding{}, res.Error
	}
	if res.RowsAffected == 0 {
		return db.CatalogBinding{}, db.ErrNoRows
	}
	return r.GetBinding(ctx, arg.ID)
}

// TransitionBinding moves a binding between lifecycle states, guarded by the
// expected ones; no row means a concurrent change.
func (r *Repo) TransitionBinding(ctx context.Context, arg db.TransitionBindingParams) (db.CatalogBinding, error) {
	if !arg.ID.Valid || len(arg.FromStates) == 0 {
		return db.CatalogBinding{}, db.ErrNoRows
	}
	now := time.Now().UTC()
	changes := map[string]any{
		"lifecycle_state":    arg.ToState,
		"last_error_code":    arg.ErrorCode,
		"last_error_message": arg.ErrorMessage,
		"state_changed_at":   now,
		"updated_at":         now,
	}
	if arg.EngineCatalogName != nil {
		changes["engine_catalog_name"] = *arg.EngineCatalogName
	}
	if arg.DesiredVersion != nil {
		changes["desired_version"] = *arg.DesiredVersion
	}
	res := r.Users.db.WithContext(ctx).Model(&model.CatalogBinding{}).
		Where("id = ? AND lifecycle_state IN ?", UUIDString(arg.ID), arg.FromStates).
		Updates(changes)
	if res.Error != nil {
		return db.CatalogBinding{}, res.Error
	}
	if res.RowsAffected == 0 {
		return db.CatalogBinding{}, db.ErrNoRows
	}
	return r.GetBinding(ctx, arg.ID)
}

// UpsertNativeCatalog stores a catalog found inside an engine.
func (r *Repo) UpsertNativeCatalog(ctx context.Context, arg db.UpsertNativeCatalogParams) (db.Catalog, error) {
	now := time.Now().UTC()
	row := model.Catalog{
		ID:           model.UUID(NewUUID()),
		ConnectionID: model.UUID(UUIDString(arg.ConnectionID)),
		Name:         model.Text(arg.Name),
		ExtraProps:   model.JSON("{}"),
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
		Origin:       model.Text("native"),
	}
	err := r.Users.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "connection_id"}, {Name: "name"}},
		DoUpdates: clause.Assignments(map[string]any{"updated_at": now}),
	}).Create(&row).Error
	if err != nil {
		return db.Catalog{}, err
	}
	var stored model.Catalog
	if err := r.Users.db.WithContext(ctx).
		Where("connection_id = ? AND name = ?", UUIDString(arg.ConnectionID), arg.Name).
		First(&stored).Error; err != nil {
		return db.Catalog{}, err
	}
	return toDBCatalog(stored), nil
}

// UpsertNativeBinding stores a native binding, enabled again when the engine
// reports the catalog again.
func (r *Repo) UpsertNativeBinding(ctx context.Context, arg db.UpsertNativeBindingParams) (db.CatalogBinding, error) {
	now := time.Now().UTC()
	row := model.CatalogBinding{
		ID:                model.UUID(NewUUID()),
		CatalogID:         model.UUID(UUIDString(arg.CatalogID)),
		EngineID:          model.UUID(UUIDString(arg.EngineID)),
		EngineCatalogName: model.Text(arg.EngineCatalogName),
		ExtraProps:        model.JSON("{}"),
		Mode:              "native",
		Enabled:           true,
		CreatedAt:         now,
		UpdatedAt:         now,
		LifecycleState:    model.Text("active"),
		StateChangedAt:    now,
	}
	err := r.Users.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "catalog_id"}, {Name: "engine_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"engine_catalog_name": arg.EngineCatalogName,
			"enabled":             true,
			"updated_at":          now,
		}),
	}).Create(&row).Error
	if err != nil {
		return db.CatalogBinding{}, err
	}
	var stored model.CatalogBinding
	if err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND engine_id = ?", UUIDString(arg.CatalogID), UUIDString(arg.EngineID)).
		First(&stored).Error; err != nil {
		return db.CatalogBinding{}, err
	}
	return toDBCatalogBinding(stored), nil
}

// DisableMissingNativeBindings disables the native catalogs an engine no
// longer reports.
func (r *Repo) DisableMissingNativeBindings(ctx context.Context, arg db.DisableMissingNativeBindingsParams) (int64, error) {
	q := r.Users.db.WithContext(ctx).Model(&model.CatalogBinding{}).
		Where("engine_id = ? AND mode = ? AND enabled", UUIDString(arg.EngineID), "native")
	if len(arg.Present) > 0 {
		q = q.Where("engine_catalog_name NOT IN ?", arg.Present)
	}
	res := q.Updates(map[string]any{"enabled": false, "updated_at": time.Now().UTC()})
	return res.RowsAffected, res.Error
}

// DeleteNativeCatalogsOfEngine deletes the native catalogs of an engine (their
// bindings cascade).
func (r *Repo) DeleteNativeCatalogsOfEngine(ctx context.Context, engineID db.UUID) error {
	var ids []string
	if err := r.Users.db.WithContext(ctx).Model(&model.CatalogBinding{}).
		Where("engine_id = ? AND mode = ?", UUIDString(engineID), "native").
		Pluck("catalog_id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return r.Users.db.WithContext(ctx).
		Where("id IN ? AND origin = ?", ids, "native").
		Delete(&model.Catalog{}).Error
}

// UpsertAdoptedBinding records a catalog found in an engine that no managed
// binding created, enabling an adopted one the engine reports again.
func (r *Repo) UpsertAdoptedBinding(ctx context.Context, arg db.UpsertAdoptedBindingParams) error {
	now := time.Now().UTC()
	res := r.Users.db.WithContext(ctx).Model(&model.CatalogBinding{}).
		Where("engine_id = ? AND engine_catalog_name = ? AND mode = ? AND NOT enabled",
			UUIDString(arg.EngineID), arg.EngineCatalogName, "adopted").
		Updates(map[string]any{"enabled": true, "updated_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}
	var count int64
	if err := r.Users.db.WithContext(ctx).Model(&model.CatalogBinding{}).
		Where("engine_id = ? AND engine_catalog_name = ?", UUIDString(arg.EngineID), arg.EngineCatalogName).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	row := model.CatalogBinding{
		ID:                model.UUID(NewUUID()),
		CatalogID:         model.UUID(UUIDString(arg.CatalogID)),
		EngineID:          model.UUID(UUIDString(arg.EngineID)),
		EngineCatalogName: model.Text(arg.EngineCatalogName),
		Connector:         arg.Connector,
		ExtraProps:        model.JSON("{}"),
		Mode:              "adopted",
		Enabled:           true,
		CreatedAt:         now,
		UpdatedAt:         now,
		LifecycleState:    model.Text("active"),
		StateChangedAt:    now,
	}
	return r.Users.db.WithContext(ctx).Create(&row).Error
}

// DisableMissingAdoptedBindings disables adopted catalogs the engine no
// longer reports.
func (r *Repo) DisableMissingAdoptedBindings(ctx context.Context, engineID db.UUID) (int64, error) {
	var names []string
	if err := r.Users.db.WithContext(ctx).Model(&model.EngineCatalogObservation{}).
		Where("engine_id = ? AND NOT present", UUIDString(engineID)).
		Pluck("catalog_name", &names).Error; err != nil {
		return 0, err
	}
	if len(names) == 0 {
		return 0, nil
	}
	res := r.Users.db.WithContext(ctx).Model(&model.CatalogBinding{}).
		Where("engine_id = ? AND mode = ? AND enabled AND engine_catalog_name IN ?",
			UUIDString(engineID), "adopted", names).
		Updates(map[string]any{"enabled": false, "updated_at": time.Now().UTC()})
	return res.RowsAffected, res.Error
}

// GetManagedConnectionOfEngineCatalog returns the connection behind a catalog
// an engine exposes through a managed binding.
func (r *Repo) GetManagedConnectionOfEngineCatalog(ctx context.Context, arg db.GetManagedConnectionOfEngineCatalogParams) (db.CatalogConnection, error) {
	var row model.CatalogConnection
	err := r.Users.db.WithContext(ctx).
		Table("catalog_connections AS cc").
		Select("cc.*").
		Joins("JOIN catalogs c ON c.connection_id = cc.id").
		Joins("JOIN catalog_bindings b ON b.catalog_id = c.id").
		Where("b.engine_id = ? AND b.engine_catalog_name = ? AND b.mode = ? AND b.enabled",
			UUIDString(arg.EngineID), arg.EngineCatalogName, "managed").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.CatalogConnection{}, db.ErrNoRows
	}
	if err != nil {
		return db.CatalogConnection{}, err
	}
	return toDBCatalogConnection(row), nil
}

// ── count helpers ─────────────────────────────────────────────────────────────

// CountNamespacesByCatalog counts a catalog's live namespaces.
func (r *Repo) CountNamespacesByCatalog(ctx context.Context, catalogID db.UUID) (int64, error) {
	var count int64
	err := r.Users.db.WithContext(ctx).Model(&model.MetadataNamespace{}).
		Where("catalog_id = ? AND removed_at IS NULL", UUIDString(catalogID)).
		Count(&count).Error
	return count, err
}

// CountTablesByCatalog counts a catalog's live tables.
func (r *Repo) CountTablesByCatalog(ctx context.Context, catalogID db.UUID) (int64, error) {
	var count int64
	err := r.Users.db.WithContext(ctx).Model(&model.MetadataTable{}).
		Where("catalog_id = ? AND removed_at IS NULL", UUIDString(catalogID)).
		Count(&count).Error
	return count, err
}

// CountCatalogImpact counts what depends on a catalog before it is retired.
func (r *Repo) CountCatalogImpact(ctx context.Context, catalogID db.UUID) (db.CountCatalogImpactRow, error) {
	id := UUIDString(catalogID)
	var out db.CountCatalogImpactRow
	for _, c := range []struct {
		model any
		where string
		dst   *int64
	}{
		{&model.CatalogBinding{}, "catalog_id = ?", &out.Bindings},
		{&model.WorkspaceCatalogGrant{}, "catalog_id = ?", &out.Grants},
		{&model.SemanticMetric{}, "catalog_id = ?", &out.Metrics},
	} {
		if err := r.Users.db.WithContext(ctx).Model(c.model).Where(c.where, id).Count(c.dst).Error; err != nil {
			return db.CountCatalogImpactRow{}, err
		}
	}
	var targeting int64
	if err := r.Users.db.WithContext(ctx).Table("saved_queries AS s").
		Joins("JOIN catalog_bindings b ON b.id = s.target_binding_id").
		Where("b.catalog_id = ?", id).
		Count(&targeting).Error; err != nil {
		return db.CountCatalogImpactRow{}, err
	}
	out.SavedQueriesTargeting = targeting
	var running int64
	if err := r.Users.db.WithContext(ctx).Table("query_executions AS e").
		Joins("JOIN catalog_bindings b ON b.id = e.target_binding_id").
		Where("b.catalog_id = ? AND e.status = ?", id, "running").
		Count(&running).Error; err != nil {
		return db.CountCatalogImpactRow{}, err
	}
	out.RunningExecutions = running
	return out, nil
}

// CountConnectionImpact counts what depends on a connection.
func (r *Repo) CountConnectionImpact(ctx context.Context, connectionID db.UUID) (db.CountConnectionImpactRow, error) {
	id := UUIDString(connectionID)
	var out db.CountConnectionImpactRow
	if err := r.Users.db.WithContext(ctx).Model(&model.Catalog{}).
		Where("connection_id = ?", id).Count(&out.Catalogs).Error; err != nil {
		return db.CountConnectionImpactRow{}, err
	}
	if err := r.Users.db.WithContext(ctx).Table("catalog_bindings AS b").
		Joins("JOIN catalogs c ON c.id = b.catalog_id").
		Where("c.connection_id = ?", id).Count(&out.Bindings).Error; err != nil {
		return db.CountConnectionImpactRow{}, err
	}
	if err := r.Users.db.WithContext(ctx).Table("query_executions AS e").
		Joins("JOIN catalog_bindings b ON b.id = e.target_binding_id").
		Joins("JOIN catalogs c ON c.id = b.catalog_id").
		Where("c.connection_id = ? AND e.status = ?", id, "running").
		Count(&out.RunningExecutions).Error; err != nil {
		return db.CountConnectionImpactRow{}, err
	}
	return out, nil
}

// CountEngineBlockers counts what stops an engine from being deleted.
func (r *Repo) CountEngineBlockers(ctx context.Context, engineID db.UUID) (db.CountEngineBlockersRow, error) {
	id := UUIDString(engineID)
	var out db.CountEngineBlockersRow
	if err := r.Users.db.WithContext(ctx).Model(&model.CatalogBinding{}).
		Where("engine_id = ? AND mode = ?", id, "managed").Count(&out.ManagedBindings).Error; err != nil {
		return db.CountEngineBlockersRow{}, err
	}
	if err := r.Users.db.WithContext(ctx).Model(&model.QueryExecution{}).
		Where("engine_id = ? AND status = ?", id, "running").Count(&out.RunningExecutions).Error; err != nil {
		return db.CountEngineBlockersRow{}, err
	}
	return out, nil
}

// CountEngineImpact counts what depends on an engine before it is disabled or
// deleted.
func (r *Repo) CountEngineImpact(ctx context.Context, engineID db.UUID) (db.CountEngineImpactRow, error) {
	id := UUIDString(engineID)
	var out db.CountEngineImpactRow
	if err := r.Users.db.WithContext(ctx).Model(&model.CatalogBinding{}).
		Where("engine_id = ? AND mode = ?", id, "managed").Count(&out.ManagedBindings).Error; err != nil {
		return db.CountEngineImpactRow{}, err
	}
	if err := r.Users.db.WithContext(ctx).Table("catalog_bindings AS b").
		Joins("JOIN catalogs c ON c.id = b.catalog_id").
		Where("b.engine_id = ? AND b.mode = ? AND c.origin = ?", id, "native", "native").
		Count(&out.NativeCatalogs).Error; err != nil {
		return db.CountEngineImpactRow{}, err
	}
	if err := r.Users.db.WithContext(ctx).Table("workspace_catalog_grants AS g").
		Joins("JOIN catalog_bindings b ON b.catalog_id = g.catalog_id").
		Where("b.engine_id = ?", id).Count(&out.Grants).Error; err != nil {
		return db.CountEngineImpactRow{}, err
	}
	if err := r.Users.db.WithContext(ctx).Table("semantic_metrics AS m").
		Joins("JOIN catalog_bindings b ON b.catalog_id = m.catalog_id OR b.id = m.binding_id").
		Where("b.engine_id = ?", id).Count(&out.Metrics).Error; err != nil {
		return db.CountEngineImpactRow{}, err
	}
	if err := r.Users.db.WithContext(ctx).Table("saved_queries AS s").
		Joins("JOIN catalog_bindings b ON b.id = s.target_binding_id").
		Where("b.engine_id = ?", id).Count(&out.SavedQueriesTargeting).Error; err != nil {
		return db.CountEngineImpactRow{}, err
	}
	if err := r.Users.db.WithContext(ctx).Model(&model.QueryExecution{}).
		Where("engine_id = ? AND status = ?", id, "running").Count(&out.RunningExecutions).Error; err != nil {
		return db.CountEngineImpactRow{}, err
	}
	return out, nil
}

// ── reconcile runs ────────────────────────────────────────────────────────────

// InsertReconcileRun stores one reconcile run, keeping the last 100 per engine.
func (r *Repo) InsertReconcileRun(ctx context.Context, arg db.InsertReconcileRunParams) (db.InsertReconcileRunRow, error) {
	row := model.CatalogReconcileRun{
		ID:         model.UUID(NewUUID()),
		EngineID:   model.UUID(UUIDString(arg.EngineID)),
		StartedAt:  arg.StartedAt.Time,
		FinishedAt: arg.FinishedAt.Time,
		DryRun:     arg.DryRun,
		Actions:    jsonOrDefault(arg.Actions),
		Adopted:    model.StringArray(arg.Adopted),
		Error:      arg.Error,
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.InsertReconcileRunRow{}, err
	}
	var old []string
	if err := r.Users.db.WithContext(ctx).Model(&model.CatalogReconcileRun{}).
		Where("engine_id = ?", UUIDString(arg.EngineID)).
		Order("started_at DESC").
		Pluck("id", &old).Error; err != nil {
		return db.InsertReconcileRunRow{}, err
	}
	if len(old) > 99 {
		if err := r.Users.db.WithContext(ctx).Where("id IN ?", old[99:]).
			Delete(&model.CatalogReconcileRun{}).Error; err != nil {
			return db.InsertReconcileRunRow{}, err
		}
	}
	return toDBInsertReconcileRunRow(row), nil
}

// GetReconcileRun returns one run of an engine.
func (r *Repo) GetReconcileRun(ctx context.Context, arg db.GetReconcileRunParams) (db.CatalogReconcileRun, error) {
	var row model.CatalogReconcileRun
	err := r.Users.db.WithContext(ctx).
		Where("engine_id = ? AND id = ?", UUIDString(arg.EngineID), UUIDString(arg.ID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.CatalogReconcileRun{}, db.ErrNoRows
	}
	if err != nil {
		return db.CatalogReconcileRun{}, err
	}
	return toDBCatalogReconcileRun(row), nil
}

// ListReconcileRuns lists an engine's newest runs.
func (r *Repo) ListReconcileRuns(ctx context.Context, arg db.ListReconcileRunsParams) ([]db.CatalogReconcileRun, error) {
	var rows []model.CatalogReconcileRun
	if err := r.Users.db.WithContext(ctx).
		Where("engine_id = ?", UUIDString(arg.EngineID)).
		Order("started_at DESC").
		Limit(int(arg.Limit)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.CatalogReconcileRun, len(rows))
	for i, row := range rows {
		out[i] = toDBCatalogReconcileRun(row)
	}
	return out, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (r *Repo) getBinding(ctx context.Context, id db.UUID) (model.CatalogBinding, error) {
	var row model.CatalogBinding
	if !id.Valid {
		return row, db.ErrNoRows
	}
	err := r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, db.ErrNoRows
	}
	return row, err
}

// bindingStatus reproduces binding_status_v in Go (ADR 0020 removes the view).
func (r *Repo) bindingStatus(ctx context.Context, b model.CatalogBinding) (db.BindingStatusV, error) {
	registry, registryFound, err := r.registryState(ctx, b.EngineID, string(b.EngineCatalogName))
	if err != nil {
		return db.BindingStatusV{}, err
	}
	observation, observationFound, err := r.observationState(ctx, b.EngineID, string(b.EngineCatalogName))
	if err != nil {
		return db.BindingStatusV{}, err
	}
	return bindingStatusFrom(b, registry, registryFound, observation, observationFound), nil
}

// bindingStatusFrom derives the sync status the API shows for a binding from
// already-loaded registry and observation state. It is shared by the
// single-binding path and the batched list path so the logic has one source.
func bindingStatusFrom(b model.CatalogBinding, registry model.TrinoCatalogRegistry, registryFound bool, observation model.EngineCatalogObservation, observationFound bool) db.BindingStatusV {
	status := db.BindingStatusV{BindingID: PGUUID(string(b.ID)), LifecycleState: string(b.LifecycleState)}
	switch {
	case b.LifecycleState == "error":
		status.SyncStatus = "error"
	case b.LifecycleState == "deprovisioning" || b.LifecycleState == "disabled":
		status.SyncStatus = "disabled"
	case b.LifecycleState == "provisioning" || b.LifecycleState == "updating":
		status.SyncStatus = "pending"
	case b.Mode != "managed":
		status.SyncStatus = "synced"
	case registryFound && registry.Enabled && registry.SyncStatus == "error":
		status.SyncStatus = "error"
	case registryFound && registry.UpdatedBy == "trino" && registry.Enabled &&
		equalStringPtr(registry.CatalogVersion, b.DesiredVersion) && (!observationFound || observation.Present):
		status.SyncStatus = "synced"
	default:
		status.SyncStatus = "pending"
	}
	if b.LifecycleState == "active" && b.Mode == "managed" && registryFound && registry.Enabled && registry.SyncStatus == "error" {
		if registry.SyncError != nil && *registry.SyncError != "" {
			status.EngineError = *registry.SyncError
		} else {
			status.EngineError = "the engine could not load the catalog"
		}
	}
	return status
}

func equalStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// bindingStatusSet holds the rows needed to derive binding status for many
// bindings in a constant number of queries. See loadBindingStatusSet.
type bindingStatusSet struct {
	engines      map[model.UUID]model.Engine
	clusters     map[model.UUID]model.TrinoCluster
	registries   map[string]model.TrinoCatalogRegistry
	observations map[string]model.EngineCatalogObservation
}

func clusterRegistryKey(clusterID model.UUID, catalogName string) string {
	return string(clusterID) + "\x00" + catalogName
}

func observationKey(engineID model.UUID, catalogName string) string {
	return string(engineID) + "\x00" + catalogName
}

// loadBindingStatusSet loads, in four queries, the engines, Trino clusters,
// registries and observations behind the given bindings. It mirrors the
// per-binding registryState and observationState lookups, batched.
func (r *Repo) loadBindingStatusSet(scope *gorm.DB, bindings []model.CatalogBinding) (*bindingStatusSet, error) {
	s := &bindingStatusSet{
		engines:      make(map[model.UUID]model.Engine),
		clusters:     make(map[model.UUID]model.TrinoCluster),
		registries:   make(map[string]model.TrinoCatalogRegistry),
		observations: make(map[string]model.EngineCatalogObservation),
	}
	if len(bindings) == 0 {
		return s, nil
	}
	engineIDs := make([]model.UUID, 0, len(bindings))
	seenEngines := make(map[model.UUID]struct{}, len(bindings))
	catalogNames := make([]string, 0, len(bindings))
	seenNames := make(map[string]struct{}, len(bindings))
	for _, b := range bindings {
		if _, ok := seenEngines[b.EngineID]; !ok {
			seenEngines[b.EngineID] = struct{}{}
			engineIDs = append(engineIDs, b.EngineID)
		}
		name := string(b.EngineCatalogName)
		if _, ok := seenNames[name]; !ok {
			seenNames[name] = struct{}{}
			catalogNames = append(catalogNames, name)
		}
	}

	var engines []model.Engine
	if err := scope.Select("id", "name", "kind", "enabled").Where("id IN ?", engineIDs).Find(&engines).Error; err != nil {
		return nil, err
	}
	for _, e := range engines {
		s.engines[e.ID] = e
	}

	var clusters []model.TrinoCluster
	if err := scope.Where("engine_id IN ?", engineIDs).Find(&clusters).Error; err != nil {
		return nil, err
	}
	clusterIDs := make([]model.UUID, 0, len(clusters))
	for _, c := range clusters {
		if c.EngineID == nil {
			continue
		}
		s.clusters[*c.EngineID] = c
		clusterIDs = append(clusterIDs, c.ID)
	}

	if len(clusterIDs) > 0 {
		var registries []model.TrinoCatalogRegistry
		if err := scope.Where("cluster_id IN ? AND catalog_name IN ?", clusterIDs, catalogNames).Find(&registries).Error; err != nil {
			return nil, err
		}
		for _, reg := range registries {
			s.registries[clusterRegistryKey(reg.ClusterID, string(reg.CatalogName))] = reg
		}
	}

	var observations []model.EngineCatalogObservation
	if err := scope.Where("engine_id IN ? AND catalog_name IN ?", engineIDs, catalogNames).Find(&observations).Error; err != nil {
		return nil, err
	}
	for _, o := range observations {
		s.observations[observationKey(o.EngineID, string(o.CatalogName))] = o
	}
	return s, nil
}

// registryFor mirrors registryState for a binding against a preloaded set.
func (s *bindingStatusSet) registryFor(b model.CatalogBinding) (model.TrinoCatalogRegistry, bool) {
	cluster, ok := s.clusters[b.EngineID]
	if !ok {
		return model.TrinoCatalogRegistry{}, false
	}
	registry, ok := s.registries[clusterRegistryKey(cluster.ID, string(b.EngineCatalogName))]
	return registry, ok
}

// status derives a binding's status from a preloaded set, matching
// bindingStatusFrom without touching the database.
func (s *bindingStatusSet) status(b model.CatalogBinding) db.BindingStatusV {
	registry, registryFound := s.registryFor(b)
	observation, observationFound := s.observations[observationKey(b.EngineID, string(b.EngineCatalogName))]
	return bindingStatusFrom(b, registry, registryFound, observation, observationFound)
}

func toDBCatalogs(rows []model.Catalog) []db.Catalog {
	out := make([]db.Catalog, len(rows))
	for i, row := range rows {
		out[i] = toDBCatalog(row)
	}
	return out
}

func toDBCatalog(c model.Catalog) db.Catalog {
	return db.Catalog{
		ID:               PGUUID(string(c.ID)),
		ConnectionID:     PGUUID(string(c.ConnectionID)),
		Name:             string(c.Name),
		Description:      c.Description,
		TrinoCatalogName: textPtr(c.TrinoCatalogName),
		ClusterID:        uuidPtr(c.ClusterID),
		Warehouse:        c.Warehouse,
		Connector:        textPtr(c.Connector),
		ExtraProps:       []byte(c.ExtraProps),
		Enabled:          c.Enabled,
		CreatedAt:        PGTime(c.CreatedAt),
		UpdatedAt:        PGTime(c.UpdatedAt),
		Origin:           string(c.Origin),
		SourceRef:        c.SourceRef,
		Discoverable:     c.Discoverable,
	}
}

func toDBCatalogBindings(rows []model.CatalogBinding) []db.CatalogBinding {
	out := make([]db.CatalogBinding, len(rows))
	for i, row := range rows {
		out[i] = toDBCatalogBinding(row)
	}
	return out
}

func toDBCatalogReconcileRun(r model.CatalogReconcileRun) db.CatalogReconcileRun {
	return db.CatalogReconcileRun{
		ID:         PGUUID(string(r.ID)),
		EngineID:   PGUUID(string(r.EngineID)),
		StartedAt:  PGTime(r.StartedAt),
		FinishedAt: PGTime(r.FinishedAt),
		DryRun:     r.DryRun,
		Actions:    []byte(r.Actions),
		Adopted:    []string(r.Adopted),
		Error:      r.Error,
	}
}

func toDBInsertReconcileRunRow(r model.CatalogReconcileRun) db.InsertReconcileRunRow {
	return db.InsertReconcileRunRow{
		ID:         PGUUID(string(r.ID)),
		EngineID:   PGUUID(string(r.EngineID)),
		StartedAt:  PGTime(r.StartedAt),
		FinishedAt: PGTime(r.FinishedAt),
		DryRun:     r.DryRun,
		Actions:    []byte(r.Actions),
		Adopted:    []string(r.Adopted),
		Error:      r.Error,
	}
}
