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

// ── engines ───────────────────────────────────────────────────────────────────

// CreateEngine inserts an engine.
func (r *Repo) CreateEngine(ctx context.Context, arg db.CreateEngineParams) (db.Engine, error) {
	now := time.Now().UTC()
	row := model.Engine{
		ID:           model.UUID(NewUUID()),
		Name:         model.Text(arg.Name),
		Kind:         arg.Kind,
		Class:        arg.Class,
		ConnectionID: model.UUID(UUIDString(arg.ConnectionID)),
		IdentityMode: model.Text(arg.IdentityMode),
		Enabled:      true,
		CreatedBy:    modelUUIDPtr(arg.CreatedBy),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.Engine{}, err
	}
	return toDBEngine(row), nil
}

// ListEngines lists every engine by name.
func (r *Repo) ListEngines(ctx context.Context) ([]db.Engine, error) {
	var rows []model.Engine
	if err := r.Users.db.WithContext(ctx).Order("name").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.Engine, len(rows))
	for i, row := range rows {
		out[i] = toDBEngine(row)
	}
	return out, nil
}

// ListEnginesVisibleToUser lists the engines on connections the user owns.
func (r *Repo) ListEnginesVisibleToUser(ctx context.Context, userID db.UUID) ([]db.Engine, error) {
	var rows []model.Engine
	if err := r.Users.db.WithContext(ctx).
		Table("engines AS e").
		Select("e.*").
		Joins("JOIN catalog_connections c ON c.id = e.connection_id").
		Where("c.created_by = ?", uuidText(userID)).
		Order("e.name").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.Engine, len(rows))
	for i, row := range rows {
		out[i] = toDBEngine(row)
	}
	return out, nil
}

// UpdateEngine applies a partial update and returns the row.
func (r *Repo) UpdateEngine(ctx context.Context, arg db.UpdateEngineParams) (db.Engine, error) {
	changes := map[string]any{"updated_at": time.Now().UTC()}
	if arg.Enabled != nil {
		changes["enabled"] = *arg.Enabled
	}
	if arg.IdentityMode != nil {
		changes["identity_mode"] = *arg.IdentityMode
	}
	if arg.CostRoutingEnabled != nil {
		changes["cost_routing_enabled"] = *arg.CostRoutingEnabled
	}
	switch {
	case arg.ClearCostThreshold != nil && *arg.ClearCostThreshold:
		changes["cost_threshold_bytes"] = nil
	case arg.CostThresholdBytes != nil:
		changes["cost_threshold_bytes"] = *arg.CostThresholdBytes
	}
	switch {
	case arg.ClearExternalUrl != nil && *arg.ClearExternalUrl:
		changes["external_url"] = nil
	case arg.ExternalUrl != nil:
		changes["external_url"] = *arg.ExternalUrl
	}
	res := r.Users.db.WithContext(ctx).Model(&model.Engine{}).
		Where("id = ?", uuidText(arg.ID)).
		Updates(changes)
	if res.Error != nil {
		return db.Engine{}, res.Error
	}
	if res.RowsAffected == 0 {
		return db.Engine{}, db.ErrNoRows
	}
	return r.GetEngineByID(ctx, arg.ID)
}

// DeleteEngine removes an engine; its bindings cascade.
func (r *Repo) DeleteEngine(ctx context.Context, id db.UUID) error {
	return r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).Delete(&model.Engine{}).Error
}

// SetEngineMaxRunningTotal stores the engine's ceiling.
func (r *Repo) SetEngineMaxRunningTotal(ctx context.Context, arg db.SetEngineMaxRunningTotalParams) (db.Engine, error) {
	if err := r.Users.db.WithContext(ctx).Model(&model.Engine{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{"max_running_total": arg.MaxRunningTotal, "updated_at": time.Now().UTC()}).Error; err != nil {
		return db.Engine{}, err
	}
	return r.GetEngineByID(ctx, arg.ID)
}

// SetEngineTrinoResourceGroups stores the resource-group handover switch.
func (r *Repo) SetEngineTrinoResourceGroups(ctx context.Context, arg db.SetEngineTrinoResourceGroupsParams) (db.Engine, error) {
	if err := r.Users.db.WithContext(ctx).Model(&model.Engine{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"trino_resource_groups_enabled": arg.TrinoResourceGroupsEnabled,
			"updated_at":                    time.Now().UTC(),
		}).Error; err != nil {
		return db.Engine{}, err
	}
	return r.GetEngineByID(ctx, arg.ID)
}

// ── Trino clusters ────────────────────────────────────────────────────────────

// GetTrinoClusterByID returns one cluster.
func (r *Repo) GetTrinoClusterByID(ctx context.Context, id db.UUID) (db.TrinoCluster, error) {
	if !id.Valid {
		return db.TrinoCluster{}, db.ErrNoRows
	}
	var row model.TrinoCluster
	err := r.Users.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.TrinoCluster{}, db.ErrNoRows
	}
	if err != nil {
		return db.TrinoCluster{}, err
	}
	return toDBTrinoCluster(row), nil
}

// GetTrinoClusterByEngine returns the cluster an engine runs.
func (r *Repo) GetTrinoClusterByEngine(ctx context.Context, engineID db.UUID) (db.TrinoCluster, error) {
	var row model.TrinoCluster
	err := r.Users.db.WithContext(ctx).Where("engine_id = ?", uuidText(engineID)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.TrinoCluster{}, db.ErrNoRows
	}
	if err != nil {
		return db.TrinoCluster{}, err
	}
	return toDBTrinoCluster(row), nil
}

// LinkTrinoClusterEngine attaches a cluster to an engine.
func (r *Repo) LinkTrinoClusterEngine(ctx context.Context, arg db.LinkTrinoClusterEngineParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.TrinoCluster{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{"engine_id": model.UUID(UUIDString(arg.EngineID)), "updated_at": time.Now().UTC()}).Error
}

// ── queue capacities ──────────────────────────────────────────────────────────

// DeleteEngineQueueCapacities clears one engine's capacities.
func (r *Repo) DeleteEngineQueueCapacities(ctx context.Context, engineID db.UUID) error {
	return r.Users.db.WithContext(ctx).
		Where("engine_id = ?", UUIDString(engineID)).
		Delete(&model.EngineQueueCapacity{}).Error
}

// UpsertEngineQueueCapacity inserts or replaces one class's capacity.
func (r *Repo) UpsertEngineQueueCapacity(ctx context.Context, arg db.UpsertEngineQueueCapacityParams) error {
	now := time.Now().UTC()
	row := model.EngineQueueCapacity{
		EngineID:         model.UUID(UUIDString(arg.EngineID)),
		Class:            model.Text(arg.Class),
		MaxRunning:       arg.MaxRunning,
		MaxQueued:        arg.MaxQueued,
		QueueTimeoutMs:   arg.QueueTimeoutMs,
		RuntimeTimeoutMs: arg.RuntimeTimeoutMs,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	return r.Users.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "engine_id"}, {Name: "class"}},
		DoUpdates: clause.Assignments(map[string]any{
			"max_running":        arg.MaxRunning,
			"max_queued":         arg.MaxQueued,
			"queue_timeout_ms":   arg.QueueTimeoutMs,
			"runtime_timeout_ms": arg.RuntimeTimeoutMs,
			"updated_at":         now,
		}),
	}).Create(&row).Error
}

// ── observations ──────────────────────────────────────────────────────────────

// ListEngineObservations lists the catalogs an engine reported.
func (r *Repo) ListEngineObservations(ctx context.Context, engineID db.UUID) ([]db.EngineCatalogObservation, error) {
	var rows []model.EngineCatalogObservation
	if err := r.Users.db.WithContext(ctx).
		Where("engine_id = ?", UUIDString(engineID)).
		Order("catalog_name").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.EngineCatalogObservation, len(rows))
	for i, row := range rows {
		out[i] = toDBEngineCatalogObservation(row)
	}
	return out, nil
}

// UpsertEngineCatalogObservation records a catalog the engine reported.
func (r *Repo) UpsertEngineCatalogObservation(ctx context.Context, arg db.UpsertEngineCatalogObservationParams) error {
	now := time.Now().UTC()
	row := model.EngineCatalogObservation{
		EngineID:    model.UUID(UUIDString(arg.EngineID)),
		CatalogName: model.Text(arg.CatalogName),
		Present:     true,
		Mode:        arg.Mode,
		Connector:   arg.Connector,
		FirstSeenAt: now,
		ObservedAt:  now,
	}
	return r.Users.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "engine_id"}, {Name: "catalog_name"}},
		DoUpdates: clause.Assignments(map[string]any{
			"present":     true,
			"mode":        arg.Mode,
			"connector":   arg.Connector,
			"observed_at": now,
		}),
	}).Create(&row).Error
}

// MarkEngineCatalogsAbsent flags the catalogs the engine no longer reports.
func (r *Repo) MarkEngineCatalogsAbsent(ctx context.Context, arg db.MarkEngineCatalogsAbsentParams) error {
	q := r.Users.db.WithContext(ctx).Model(&model.EngineCatalogObservation{}).
		Where("engine_id = ?", UUIDString(arg.EngineID))
	if len(arg.PresentNames) > 0 {
		q = q.Where("catalog_name NOT IN ?", arg.PresentNames)
	}
	return q.Updates(map[string]any{"present": false, "observed_at": time.Now().UTC()}).Error
}

// MarkEngineCatalogManaged records that Baleia still manages a reported name.
func (r *Repo) MarkEngineCatalogManaged(ctx context.Context, arg db.MarkEngineCatalogManagedParams) error {
	now := time.Now().UTC()
	row := model.EngineCatalogObservation{
		EngineID:    model.UUID(UUIDString(arg.EngineID)),
		CatalogName: model.Text(arg.CatalogName),
		Present:     true,
		Mode:        "managed",
		FirstSeenAt: now,
		ObservedAt:  now,
	}
	return r.Users.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "engine_id"}, {Name: "catalog_name"}},
		DoUpdates: clause.Assignments(map[string]any{
			"mode":        "managed",
			"present":     true,
			"observed_at": now,
		}),
	}).Create(&row).Error
}

// ObservedManagedNamesOfEngine lists managed names the engine still reports.
func (r *Repo) ObservedManagedNamesOfEngine(ctx context.Context, engineID db.UUID) ([]string, error) {
	var names []string
	err := r.Users.db.WithContext(ctx).Model(&model.EngineCatalogObservation{}).
		Where("engine_id = ? AND mode = ? AND present", UUIDString(engineID), "managed").
		Pluck("catalog_name", &names).Error
	return names, err
}

// ── conversions ───────────────────────────────────────────────────────────────

func toDBEngine(e model.Engine) db.Engine {
	return db.Engine{
		ID:                         PGUUID(string(e.ID)),
		Name:                       string(e.Name),
		Kind:                       e.Kind,
		Class:                      e.Class,
		ConnectionID:               PGUUID(string(e.ConnectionID)),
		IdentityMode:               string(e.IdentityMode),
		Enabled:                    e.Enabled,
		CreatedBy:                  uuidPtr(e.CreatedBy),
		CreatedAt:                  PGTime(e.CreatedAt),
		UpdatedAt:                  PGTime(e.UpdatedAt),
		MaxRunningTotal:            e.MaxRunningTotal,
		CostRoutingEnabled:         e.CostRoutingEnabled,
		CostThresholdBytes:         e.CostThresholdBytes,
		ExternalUrl:                e.ExternalURL,
		TrinoResourceGroupsEnabled: e.TrinoResourceGroupsEnabled,
	}
}

func toDBTrinoCluster(c model.TrinoCluster) db.TrinoCluster {
	return db.TrinoCluster{
		ID:             PGUUID(string(c.ID)),
		Name:           string(c.Name),
		CoordinatorUrl: c.CoordinatorURL,
		NodeVersion:    c.NodeVersion,
		CreatedAt:      PGTime(c.CreatedAt),
		UpdatedAt:      PGTimePtr(c.UpdatedAt),
		EngineID:       uuidPtr(c.EngineID),
	}
}

func toDBEngineCatalogObservation(o model.EngineCatalogObservation) db.EngineCatalogObservation {
	return db.EngineCatalogObservation{
		EngineID:    PGUUID(string(o.EngineID)),
		CatalogName: string(o.CatalogName),
		Present:     o.Present,
		Mode:        o.Mode,
		Connector:   o.Connector,
		FirstSeenAt: PGTime(o.FirstSeenAt),
		ObservedAt:  PGTime(o.ObservedAt),
	}
}
