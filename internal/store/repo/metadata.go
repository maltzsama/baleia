package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/metadata/browse"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The browse service reads through this narrow interface; *Repo implements it
// (ADR 0020, #772).
var _ browse.Store = (*Repo)(nil)

// ── catalogs, namespaces and tables ───────────────────────────────────────────

// ListMetadataCatalogs lists the granted, enabled catalogs of a workspace
// with their freshness and enabled bindings, by name.
func (r *Repo) ListMetadataCatalogs(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListMetadataCatalogsRow, error) {
	var out []db.ListMetadataCatalogsRow
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		visible, err := grantedCatalogIDs(tx, workspaceID)
		if err != nil || len(visible) == 0 {
			return err
		}
		var catalogs []model.Catalog
		if err := tx.Where("id IN ?", visible).Order("name, id").Find(&catalogs).Error; err != nil {
			return err
		}
		connectionIDs := make([]string, 0, len(catalogs))
		for _, catalog := range catalogs {
			connectionIDs = append(connectionIDs, string(catalog.ConnectionID))
		}
		var connections []model.CatalogConnection
		if err := tx.Where("id IN ?", connectionIDs).Find(&connections).Error; err != nil {
			return err
		}
		kinds := make(map[string]string, len(connections))
		for _, connection := range connections {
			kinds[string(connection.ID)] = connection.Type
		}
		out = make([]db.ListMetadataCatalogsRow, 0, len(catalogs))
		for _, catalog := range catalogs {
			syncedAt, err := finishedSyncAt(tx, string(catalog.ID))
			if err != nil {
				return err
			}
			status, err := latestSyncStatus(tx, string(catalog.ID))
			if err != nil {
				return err
			}
			bindings, err := r.metadataBindings(ctx, tx, string(catalog.ID))
			if err != nil {
				return err
			}
			encoded, err := json.Marshal(bindings)
			if err != nil {
				return err
			}
			out = append(out, db.ListMetadataCatalogsRow{
				ID:                 PGUUID(string(catalog.ID)),
				Name:               string(catalog.Name),
				Origin:             string(catalog.Origin),
				ProviderKind:       kinds[string(catalog.ConnectionID)],
				MetadataSyncedAt:   syncedAt,
				MetadataSyncStatus: status,
				Bindings:           encoded,
			})
		}
		return nil
	}, nil)
	return out, err
}

// GetGrantedMetadataCatalog returns a granted, enabled catalog with its
// freshness, or pgx.ErrNoRows.
func (r *Repo) GetGrantedMetadataCatalog(ctx context.Context, arg db.GetGrantedMetadataCatalogParams) (db.GetGrantedMetadataCatalogRow, error) {
	if !arg.WorkspaceID.Valid || !arg.CatalogID.Valid {
		return db.GetGrantedMetadataCatalogRow{}, pgx.ErrNoRows
	}
	var out db.GetGrantedMetadataCatalogRow
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		visible, err := grantedCatalogIDs(tx, arg.WorkspaceID)
		if err != nil {
			return err
		}
		if !containsString(visible, UUIDString(arg.CatalogID)) {
			return pgx.ErrNoRows
		}
		syncedAt, err := finishedSyncAt(tx, UUIDString(arg.CatalogID))
		if err != nil {
			return err
		}
		out = db.GetGrantedMetadataCatalogRow{ID: arg.CatalogID, MetadataSyncedAt: syncedAt}
		return nil
	}, nil)
	return out, err
}

// ListChildNamespaces lists the namespaces directly under parent in a catalog.
func (r *Repo) ListChildNamespaces(ctx context.Context, arg db.ListChildNamespacesParams) ([][]string, error) {
	if !arg.CatalogID.Valid {
		return nil, nil
	}
	var rows []model.MetadataNamespace
	if err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND removed_at IS NULL", UUIDString(arg.CatalogID)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	parent := arg.Parent
	if parent == nil {
		parent = []string{}
	}
	out := make([][]string, 0, len(rows))
	for _, row := range rows {
		ns := []string(row.Namespace)
		if len(ns) != len(parent)+1 || !hasPrefix(ns, parent) {
			continue
		}
		out = append(out, ns)
	}
	sort.Slice(out, func(i, j int) bool { return compareNamespace(out[i], out[j]) < 0 })
	return out, nil
}

// ListMetadataTablesPage returns one page of a namespace's live tables, by
// name, after the cursor.
func (r *Repo) ListMetadataTablesPage(ctx context.Context, arg db.ListMetadataTablesPageParams) ([]db.ListMetadataTablesPageRow, error) {
	if !arg.CatalogID.Valid || arg.Lim <= 0 {
		return nil, nil
	}
	var rows []model.MetadataTable
	if err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND namespace = ? AND removed_at IS NULL AND name > ?",
			UUIDString(arg.CatalogID), model.StringArray(arg.Namespace), arg.After).
		Order("name").
		Limit(int(arg.Lim)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ListMetadataTablesPageRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListMetadataTablesPageRow{
			ID:            PGUUID(string(row.ID)),
			Name:          string(row.Name),
			Kind:          row.Kind,
			Comment:       row.Comment,
			LastChangedAt: PGTime(row.LastChangedAt),
		}
	}
	return out, nil
}

// GetCatalogBindingForDisplay returns an enabled binding of the catalog (the
// given one, else the first) and its engine kind.
func (r *Repo) GetCatalogBindingForDisplay(ctx context.Context, arg db.GetCatalogBindingForDisplayParams) (db.GetCatalogBindingForDisplayRow, error) {
	if !arg.CatalogID.Valid {
		return db.GetCatalogBindingForDisplayRow{}, pgx.ErrNoRows
	}
	q := r.Users.db.WithContext(ctx).
		Table("catalog_bindings AS b").
		Select("b.id AS id, b.engine_catalog_name AS engine_catalog_name, e.kind AS engine_kind").
		Joins("JOIN engines e ON e.id = b.engine_id").
		Where("b.catalog_id = ? AND b.enabled AND e.enabled", UUIDString(arg.CatalogID))
	if arg.BindingID.Valid {
		q = q.Where("b.id = ?", UUIDString(arg.BindingID))
	}
	var row struct {
		ID                model.UUID
		EngineCatalogName string
		EngineKind        string
	}
	err := q.Order("b.created_at, b.id").Limit(1).Scan(&row).Error
	if err != nil {
		return db.GetCatalogBindingForDisplayRow{}, err
	}
	if row.ID == "" {
		return db.GetCatalogBindingForDisplayRow{}, pgx.ErrNoRows
	}
	return db.GetCatalogBindingForDisplayRow{
		ID:                PGUUID(string(row.ID)),
		EngineCatalogName: row.EngineCatalogName,
		EngineKind:        row.EngineKind,
	}, nil
}

// ListMetadataSyncRuns lists a catalog's latest metadata passes.
func (r *Repo) ListMetadataSyncRuns(ctx context.Context, arg db.ListMetadataSyncRunsParams) ([]db.ListMetadataSyncRunsRow, error) {
	if !arg.CatalogID.Valid || arg.Limit <= 0 {
		return nil, nil
	}
	var rows []model.MetadataSyncRun
	if err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ?", UUIDString(arg.CatalogID)).
		Order("started_at DESC").
		Limit(int(arg.Limit)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ListMetadataSyncRunsRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListMetadataSyncRunsRow{
			ID:            PGUUID(string(row.ID)),
			CatalogID:     PGUUID(string(row.CatalogID)),
			Status:        row.Status,
			SourcePath:    row.SourcePath,
			StartedAt:     PGTime(row.StartedAt),
			FinishedAt:    PGTimePtr(row.FinishedAt),
			TablesSeen:    row.TablesSeen,
			TablesChanged: row.TablesChanged,
			TablesRemoved: row.TablesRemoved,
			ErrorCode:     row.ErrorCode,
		}
	}
	return out, nil
}

// SearchMetadata finds tables and columns of the catalogs the workspace may
// use whose names are similar to q. Similarity, popularity and ordering are
// computed in Go so the query is portable (ADR 0020).
func (r *Repo) SearchMetadata(ctx context.Context, arg db.SearchMetadataParams) ([]db.SearchMetadataRow, error) {
	if !arg.WorkspaceID.Valid || arg.Lim <= 0 {
		return nil, nil
	}
	var out []db.SearchMetadataRow
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		visible, err := grantedCatalogIDs(tx, arg.WorkspaceID)
		if err != nil || len(visible) == 0 {
			return err
		}
		if arg.CatalogID.Valid {
			if !containsString(visible, UUIDString(arg.CatalogID)) {
				return nil
			}
			visible = []string{UUIDString(arg.CatalogID)}
		}
		var tables []model.MetadataTable
		if err := tx.Where("catalog_id IN ? AND removed_at IS NULL", visible).
			Order("name").
			Find(&tables).Error; err != nil {
			return err
		}
		if len(tables) == 0 {
			return nil
		}
		tableIDs := make([]string, len(tables))
		for i, table := range tables {
			tableIDs[i] = string(table.ID)
		}
		var columns []model.MetadataColumn
		if arg.Kind != browse.KindTable {
			if err := tx.Where("table_id IN ?", tableIDs).Order("ordinal").Find(&columns).Error; err != nil {
				return err
			}
		}
		docs, err := r.tableDocs(tx, arg.WorkspaceID, visible)
		if err != nil {
			return err
		}
		popularity, err := r.popularityByTable(tx, arg.WorkspaceID)
		if err != nil {
			return err
		}
		synced, err := r.syncTimes(tx, visible)
		if err != nil {
			return err
		}
		columnsByTable := make(map[string][]model.MetadataColumn, len(tables))
		for _, column := range columns {
			columnsByTable[string(column.TableID)] = append(columnsByTable[string(column.TableID)], column)
		}
		q := arg.Q
		hits := make([]db.SearchMetadataRow, 0, len(tables))
		for _, table := range tables {
			namespace := []string(table.Namespace)
			doc, docOK := docs[tableDocKey(string(table.CatalogID), namespace, string(table.Name))]
			if !matchesDocFilters(doc, docOK, arg.Tags, arg.Certified) {
				continue
			}
			if arg.Namespace != nil && !equalNamespace(namespace, arg.Namespace) {
				continue
			}
			catalogID := string(table.CatalogID)
			lastNamespace := ""
			if len(namespace) > 0 {
				lastNamespace = namespace[len(namespace)-1]
			}
			pop := popularity[tablePopularityKey(catalogID, lastNamespace, string(table.Name))]
			syncedAt := synced[catalogID]
			if arg.Kind == browse.KindTable || arg.Kind == browse.KindAll {
				if nameMatches(string(table.Name), q) {
					hits = append(hits, db.SearchMetadataRow{
						Kind:             browse.KindTable,
						TableID:          PGUUID(string(table.ID)),
						CatalogID:        PGUUID(catalogID),
						Namespace:        namespace,
						TableName:        string(table.Name),
						Score:            similarity(string(table.Name), q),
						Popularity:       pop,
						MetadataSyncedAt: syncedAt,
					})
				}
			}
			if arg.Kind == browse.KindColumn || arg.Kind == browse.KindAll {
				for _, column := range columnsByTable[string(table.ID)] {
					if !nameMatches(string(column.Name), q) {
						continue
					}
					hits = append(hits, db.SearchMetadataRow{
						Kind:             browse.KindColumn,
						TableID:          PGUUID(string(table.ID)),
						CatalogID:        PGUUID(catalogID),
						Namespace:        namespace,
						TableName:        string(table.Name),
						ColumnName:       string(column.Name),
						NativeType:       column.NativeType,
						Score:            similarity(string(column.Name), q),
						Popularity:       pop,
						MetadataSyncedAt: syncedAt,
					})
				}
			}
		}
		sort.SliceStable(hits, func(i, j int) bool {
			a, b := hits[i], hits[j]
			if a.Score != b.Score {
				return a.Score > b.Score
			}
			if a.Popularity != b.Popularity {
				return a.Popularity > b.Popularity
			}
			aLen, bLen := len(a.TableName), len(b.TableName)
			if a.ColumnName != "" {
				aLen = len(a.ColumnName)
			}
			if b.ColumnName != "" {
				bLen = len(b.ColumnName)
			}
			if aLen != bLen {
				return aLen < bLen
			}
			if a.TableName != b.TableName {
				return a.TableName < b.TableName
			}
			return a.ColumnName < b.ColumnName
		})
		if len(hits) > int(arg.Lim) {
			hits = hits[:arg.Lim]
		}
		out = hits
		return nil
	}, nil)
	return out, err
}

// ── metadata sync writes ──────────────────────────────────────────────────────

// GetCatalogForSync returns a catalog with what the sync needs to pick its
// source.
func (r *Repo) GetCatalogForSync(ctx context.Context, id pgtype.UUID) (db.GetCatalogForSyncRow, error) {
	if !id.Valid {
		return db.GetCatalogForSyncRow{}, pgx.ErrNoRows
	}
	var row struct {
		ID             model.UUID
		Name           string
		Origin         string
		SourceRef      *string
		Warehouse      *string
		Enabled        bool
		ConnectionID   model.UUID
		ConnectionType string
	}
	err := r.Users.db.WithContext(ctx).
		Table("catalogs AS c").
		Select("c.id, c.name, c.origin, c.source_ref, c.warehouse, c.enabled, cc.id AS connection_id, cc.type AS connection_type").
		Joins("JOIN catalog_connections cc ON cc.id = c.connection_id").
		Where("c.id = ?", UUIDString(id)).
		Scan(&row).Error
	if err != nil {
		return db.GetCatalogForSyncRow{}, err
	}
	if row.ID == "" {
		return db.GetCatalogForSyncRow{}, pgx.ErrNoRows
	}
	return db.GetCatalogForSyncRow{
		ID:             PGUUID(string(row.ID)),
		Name:           row.Name,
		Origin:         row.Origin,
		SourceRef:      row.SourceRef,
		Warehouse:      row.Warehouse,
		Enabled:        row.Enabled,
		ConnectionID:   PGUUID(string(row.ConnectionID)),
		ConnectionType: row.ConnectionType,
	}, nil
}

// ListSyncBindings lists a catalog's enabled bindings on enabled engines,
// oldest first.
func (r *Repo) ListSyncBindings(ctx context.Context, catalogID pgtype.UUID) ([]db.ListSyncBindingsRow, error) {
	if !catalogID.Valid {
		return nil, nil
	}
	var rows []struct {
		ID                model.UUID
		EngineID          model.UUID
		EngineCatalogName string
		Mode              string
		EngineKind        string
	}
	err := r.Users.db.WithContext(ctx).
		Table("catalog_bindings AS b").
		Select("b.id, b.engine_id, b.engine_catalog_name, b.mode, e.kind AS engine_kind").
		Joins("JOIN engines e ON e.id = b.engine_id").
		Where("b.catalog_id = ? AND b.enabled AND e.enabled AND b.lifecycle_state IN ?",
			UUIDString(catalogID), []string{"active", "updating"}).
		Order("b.created_at, b.id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]db.ListSyncBindingsRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListSyncBindingsRow{
			ID:                PGUUID(string(row.ID)),
			EngineID:          PGUUID(string(row.EngineID)),
			EngineCatalogName: row.EngineCatalogName,
			Mode:              row.Mode,
			EngineKind:        row.EngineKind,
		}
	}
	return out, nil
}

// BeginMetadataSyncRun starts a pass under a transactional row lock on the
// catalog, replacing the session advisory lock of the pgx path (ADR 0020).
// It fails a running pass older than staleAfter (a crashed worker) and
// reports false when a live pass already owns the catalog, so the caller
// returns ErrSyncInProgress.
func (r *Repo) BeginMetadataSyncRun(ctx context.Context, catalogID pgtype.UUID, sourcePath string, staleAfter time.Duration) (db.StartMetadataSyncRunRow, bool, error) {
	var (
		out     db.StartMetadataSyncRunRow
		started bool
	)
	if !catalogID.Valid {
		return out, false, pgx.ErrNoRows
	}
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The row lock serializes two starters: the second waits until the
		// first has committed its running row.
		var catalog model.Catalog
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", UUIDString(catalogID)).
			First(&catalog).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pgx.ErrNoRows
		}
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if staleAfter > 0 {
			code, message := "interrupted", "the sync stopped before it finished"
			if err := tx.Model(&model.MetadataSyncRun{}).
				Where("catalog_id = ? AND status = ? AND COALESCE(heartbeat_at, started_at) < ?",
					UUIDString(catalogID), "running", now.Add(-staleAfter)).
				Updates(map[string]any{
					"status":        "failed",
					"finished_at":   now,
					"error_code":    &code,
					"error_message": &message,
				}).Error; err != nil {
				return err
			}
		}
		var running int64
		if err := tx.Model(&model.MetadataSyncRun{}).
			Where("catalog_id = ? AND status = ?", UUIDString(catalogID), "running").
			Count(&running).Error; err != nil {
			return err
		}
		if running > 0 {
			return nil
		}
		row := model.MetadataSyncRun{
			ID:          model.UUID(NewUUID()),
			CatalogID:   model.UUID(UUIDString(catalogID)),
			Status:      "running",
			SourcePath:  sourcePath,
			StartedAt:   now,
			HeartbeatAt: &now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		out = db.StartMetadataSyncRunRow{ID: PGUUID(string(row.ID)), StartedAt: PGTime(row.StartedAt)}
		started = true
		return nil
	})
	return out, started, err
}

// TouchMetadataSyncRun records that a running pass is still alive, so a
// retry can tell a crashed worker from a slow one.
func (r *Repo) TouchMetadataSyncRun(ctx context.Context, id pgtype.UUID) error {
	if !id.Valid {
		return nil
	}
	return r.Users.db.WithContext(ctx).Model(&model.MetadataSyncRun{}).
		Where("id = ? AND status = ?", UUIDString(id), "running").
		Update("heartbeat_at", time.Now().UTC()).Error
}

// ErrMetadataSyncTakenOver means the sync run is no longer the owner of the
// catalog: a newer pass took it over, so this pass must stop writing.
var ErrMetadataSyncTakenOver = errors.New("metadata sync run is no longer the owner")

// TxMetadata runs fn in one transaction over a Repo bound to it, so a table
// and its columns and foreign keys are replaced atomically (ADR 0020). It
// first locks the run row and verifies it is still running: a pass that lost
// its lease cannot mutate the catalog any more.
func (r *Repo) TxMetadata(ctx context.Context, runID pgtype.UUID, fn func(*Repo) error) error {
	if !runID.Valid {
		return ErrMetadataSyncTakenOver
	}
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run model.MetadataSyncRun
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", UUIDString(runID)).
			First(&run).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrMetadataSyncTakenOver
		}
		if err != nil {
			return err
		}
		if run.Status != "running" {
			return ErrMetadataSyncTakenOver
		}
		return fn(New(tx))
	})
}

// StartMetadataSyncRun opens a running pass.
func (r *Repo) StartMetadataSyncRun(ctx context.Context, arg db.StartMetadataSyncRunParams) (db.StartMetadataSyncRunRow, error) {
	now := time.Now().UTC()
	row := model.MetadataSyncRun{
		ID:         model.UUID(NewUUID()),
		CatalogID:  model.UUID(UUIDString(arg.CatalogID)),
		Status:     "running",
		SourcePath: arg.SourcePath,
		StartedAt:  now,
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.StartMetadataSyncRunRow{}, err
	}
	return db.StartMetadataSyncRunRow{ID: PGUUID(string(row.ID)), StartedAt: PGTime(row.StartedAt)}, nil
}

// FinishMetadataSyncRun closes a running pass.
func (r *Repo) FinishMetadataSyncRun(ctx context.Context, arg db.FinishMetadataSyncRunParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.MetadataSyncRun{}).
		Where("id = ? AND status = ?", UUIDString(arg.ID), "running").
		Updates(map[string]any{
			"status":         arg.Status,
			"finished_at":    time.Now().UTC(),
			"tables_seen":    arg.TablesSeen,
			"tables_changed": arg.TablesChanged,
			"tables_removed": arg.TablesRemoved,
			"error_code":     arg.ErrorCode,
			"error_message":  arg.ErrorMessage,
		}).Error
}

// FailStaleMetadataSyncRuns fails a run a crashed worker left running.
func (r *Repo) FailStaleMetadataSyncRuns(ctx context.Context, catalogID pgtype.UUID) error {
	code, message := "interrupted", "the sync stopped before it finished"
	return r.Users.db.WithContext(ctx).Model(&model.MetadataSyncRun{}).
		Where("catalog_id = ? AND status = ?", UUIDString(catalogID), "running").
		Updates(map[string]any{
			"status":        "failed",
			"finished_at":   time.Now().UTC(),
			"error_code":    &code,
			"error_message": &message,
		}).Error
}

// GetTableFingerprints returns what the last passes stored about live tables.
func (r *Repo) GetTableFingerprints(ctx context.Context, catalogID pgtype.UUID) ([]db.GetTableFingerprintsRow, error) {
	if !catalogID.Valid {
		return nil, nil
	}
	var rows []model.MetadataTable
	if err := r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND removed_at IS NULL", UUIDString(catalogID)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.GetTableFingerprintsRow, len(rows))
	for i, row := range rows {
		out[i] = db.GetTableFingerprintsRow{
			Namespace:   []string(row.Namespace),
			Name:        string(row.Name),
			Fingerprint: row.Fingerprint,
			Properties:  []byte(row.Properties),
		}
	}
	return out, nil
}

// UpsertMetadataTable records a table seen by a pass. Changed reports a new
// table, one back from a tombstone or one with a new fingerprint.
func (r *Repo) UpsertMetadataTable(ctx context.Context, arg db.UpsertMetadataTableParams) (db.UpsertMetadataTableRow, error) {
	var out db.UpsertMetadataTableRow
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		var current model.MetadataTable
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("catalog_id = ? AND namespace = ? AND name = ?",
				UUIDString(arg.CatalogID), model.StringArray(arg.Namespace), arg.Name).
			First(&current).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			row := model.MetadataTable{
				ID:            model.UUID(NewUUID()),
				CatalogID:     model.UUID(UUIDString(arg.CatalogID)),
				Namespace:     model.StringArray(arg.Namespace),
				Name:          model.Text(arg.Name),
				Kind:          arg.Kind,
				Fingerprint:   arg.Fingerprint,
				Properties:    jsonOrDefault(arg.Properties),
				Comment:       arg.Comment,
				LastSeenAt:    now,
				LastChangedAt: now,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			out = db.UpsertMetadataTableRow{ID: PGUUID(string(row.ID)), Changed: true}
			return nil
		case err != nil:
			return err
		}
		changed := current.RemovedAt != nil || current.Fingerprint != arg.Fingerprint
		lastChanged := current.LastChangedAt
		if changed {
			lastChanged = now
		}
		if err := tx.Model(&model.MetadataTable{}).Where("id = ?", string(current.ID)).
			Updates(map[string]any{
				"kind":            arg.Kind,
				"fingerprint":     arg.Fingerprint,
				"properties":      jsonOrDefault(arg.Properties),
				"comment":         arg.Comment,
				"last_seen_at":    now,
				"last_changed_at": lastChanged,
				"removed_at":      nil,
			}).Error; err != nil {
			return err
		}
		out = db.UpsertMetadataTableRow{ID: PGUUID(string(current.ID)), Changed: changed}
		return nil
	})
	return out, err
}

// TouchMetadataTable records that an unchanged table is still present.
func (r *Repo) TouchMetadataTable(ctx context.Context, arg db.TouchMetadataTableParams) (int64, error) {
	res := r.Users.db.WithContext(ctx).Model(&model.MetadataTable{}).
		Where("catalog_id = ? AND namespace = ? AND name = ? AND removed_at IS NULL",
			UUIDString(arg.CatalogID), model.StringArray(arg.Namespace), arg.Name).
		Update("last_seen_at", time.Now().UTC())
	return res.RowsAffected, res.Error
}

// DeleteMetadataColumns removes a table's columns.
func (r *Repo) DeleteMetadataColumns(ctx context.Context, tableID pgtype.UUID) error {
	return r.Users.db.WithContext(ctx).Where("table_id = ?", UUIDString(tableID)).
		Delete(&model.MetadataColumn{}).Error
}

// InsertMetadataColumns stores a table's columns.
func (r *Repo) InsertMetadataColumns(ctx context.Context, arg db.InsertMetadataColumnsParams) error {
	if len(arg.Names) == 0 {
		return nil
	}
	rows := make([]model.MetadataColumn, 0, len(arg.Names))
	for i, name := range arg.Names {
		var nullable *bool
		if i < len(arg.Nullables) {
			v := arg.Nullables[i]
			nullable = &v
		}
		var comment *string
		if i < len(arg.Comments) && arg.Comments[i] != "" {
			v := arg.Comments[i]
			comment = &v
		}
		var ordinal int32
		if i < len(arg.Ordinals) {
			ordinal = arg.Ordinals[i]
		}
		var nativeType string
		if i < len(arg.NativeTypes) {
			nativeType = arg.NativeTypes[i]
		}
		rows = append(rows, model.MetadataColumn{
			TableID:    model.UUID(UUIDString(arg.TableID)),
			Ordinal:    ordinal,
			Name:       model.Text(name),
			NativeType: nativeType,
			TypeSystem: arg.TypeSystem,
			Nullable:   nullable,
			Comment:    comment,
		})
	}
	return r.Users.db.WithContext(ctx).Create(&rows).Error
}

// DeleteMetadataForeignKeys removes a table's outgoing foreign keys.
func (r *Repo) DeleteMetadataForeignKeys(ctx context.Context, tableID pgtype.UUID) error {
	return r.Users.db.WithContext(ctx).Where("table_id = ?", UUIDString(tableID)).
		Delete(&model.MetadataForeignKey{}).Error
}

// InsertMetadataForeignKeys stores one table's outgoing foreign keys, which
// the pass reports as JSON so multi-level namespaces stay arrays.
func (r *Repo) InsertMetadataForeignKeys(ctx context.Context, arg db.InsertMetadataForeignKeysParams) error {
	var keys []struct {
		ColumnName   string   `json:"column_name"`
		RefNamespace []string `json:"ref_namespace"`
		RefTable     string   `json:"ref_table"`
		RefColumn    string   `json:"ref_column"`
	}
	if err := json.Unmarshal(arg.ForeignKeys, &keys); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	rows := make([]model.MetadataForeignKey, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, model.MetadataForeignKey{
			CatalogID:    model.UUID(UUIDString(arg.CatalogID)),
			TableID:      model.UUID(UUIDString(arg.TableID)),
			ColumnName:   model.Text(key.ColumnName),
			RefNamespace: model.StringArray(key.RefNamespace),
			RefTable:     model.Text(key.RefTable),
			RefColumn:    model.Text(key.RefColumn),
			LastSeenAt:   time.Now().UTC(),
		})
	}
	return r.Users.db.WithContext(ctx).Create(&rows).Error
}

// UpsertMetadataNamespace records a namespace seen by a pass.
func (r *Repo) UpsertMetadataNamespace(ctx context.Context, arg db.UpsertMetadataNamespaceParams) error {
	now := time.Now().UTC()
	row := model.MetadataNamespace{
		CatalogID:  model.UUID(UUIDString(arg.CatalogID)),
		Namespace:  model.StringArray(nonNilStrings(arg.Namespace)),
		LastSeenAt: now,
	}
	return r.Users.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "catalog_id"}, {Name: "namespace"}},
		DoUpdates: clause.Assignments(map[string]any{
			"last_seen_at": now,
			"removed_at":   nil,
		}),
	}).Create(&row).Error
}

// TombstoneMissingTables marks the tables a pass did not see as removed.
func (r *Repo) TombstoneMissingTables(ctx context.Context, arg db.TombstoneMissingTablesParams) (int64, error) {
	res := r.Users.db.WithContext(ctx).Model(&model.MetadataTable{}).
		Where("catalog_id = ? AND last_seen_at < ? AND removed_at IS NULL",
			UUIDString(arg.CatalogID), arg.PassStartedAt.Time).
		Update("removed_at", time.Now().UTC())
	return res.RowsAffected, res.Error
}

// TombstoneMissingNamespaces marks the namespaces a pass did not see as
// removed.
func (r *Repo) TombstoneMissingNamespaces(ctx context.Context, arg db.TombstoneMissingNamespacesParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.MetadataNamespace{}).
		Where("catalog_id = ? AND last_seen_at < ? AND removed_at IS NULL",
			UUIDString(arg.CatalogID), arg.PassStartedAt.Time).
		Update("removed_at", time.Now().UTC()).Error
}

// PurgeMetadataTableTombstones deletes table tombstones older than 7 days.
func (r *Repo) PurgeMetadataTableTombstones(ctx context.Context, catalogID pgtype.UUID) error {
	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)
	return r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND removed_at < ?", UUIDString(catalogID), cutoff).
		Delete(&model.MetadataTable{}).Error
}

// PurgeMetadataNamespaceTombstones deletes namespace tombstones older than 7
// days.
func (r *Repo) PurgeMetadataNamespaceTombstones(ctx context.Context, catalogID pgtype.UUID) error {
	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)
	return r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND removed_at < ?", UUIDString(catalogID), cutoff).
		Delete(&model.MetadataNamespace{}).Error
}

// ── helpers ───────────────────────────────────────────────────────────────────

type metadataBinding struct {
	BindingID         string `json:"binding_id"`
	EngineID          string `json:"engine_id"`
	EngineName        string `json:"engine_name"`
	EngineKind        string `json:"engine_kind"`
	EngineCatalogName string `json:"engine_catalog_name"`
	Mode              string `json:"mode"`
	Status            string `json:"status"`
}

func (r *Repo) metadataBindings(ctx context.Context, scope *gorm.DB, catalogID string) ([]metadataBinding, error) {
	var rows []model.CatalogBinding
	if err := scope.Where("catalog_id = ? AND enabled", catalogID).
		Order("created_at, id").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]metadataBinding, 0, len(rows))
	for _, row := range rows {
		var engine model.Engine
		err := scope.Select("id", "name", "kind", "enabled").Where("id = ?", string(row.EngineID)).First(&engine).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && !engine.Enabled) {
			continue
		}
		if err != nil {
			return nil, err
		}
		status, err := r.bindingStatus(ctx, row)
		if err != nil {
			return nil, err
		}
		syncStatus := status.SyncStatus
		if syncStatus == "" {
			syncStatus = "pending"
		}
		out = append(out, metadataBinding{
			BindingID:         string(row.ID),
			EngineID:          string(engine.ID),
			EngineName:        string(engine.Name),
			EngineKind:        engine.Kind,
			EngineCatalogName: string(row.EngineCatalogName),
			Mode:              row.Mode,
			Status:            syncStatus,
		})
	}
	return out, nil
}

func finishedSyncAt(scope *gorm.DB, catalogID string) (pgtype.Timestamptz, error) {
	var finished sql.NullTime
	if err := scope.Model(&model.MetadataSyncRun{}).
		Where("catalog_id = ? AND status = ?", catalogID, "finished").
		Select("MAX(finished_at)").
		Scan(&finished).Error; err != nil {
		return pgtype.Timestamptz{}, err
	}
	if !finished.Valid {
		return pgtype.Timestamptz{}, nil
	}
	return PGTime(finished.Time), nil
}

func latestSyncStatus(scope *gorm.DB, catalogID string) (string, error) {
	var run model.MetadataSyncRun
	err := scope.Where("catalog_id = ?", catalogID).Order("started_at DESC").First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "never", nil
	}
	if err != nil {
		return "", err
	}
	return run.Status, nil
}

func (r *Repo) tableDocs(scope *gorm.DB, workspaceID pgtype.UUID, catalogIDs []string) (map[string]model.SemanticDoc, error) {
	var rows []model.SemanticDoc
	if err := scope.Where("workspace_id = ? AND catalog_id IN ? AND column_name IS NULL",
		UUIDString(workspaceID), catalogIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]model.SemanticDoc, len(rows))
	for _, row := range rows {
		out[tableDocKey(string(row.CatalogID), []string(row.Namespace), string(row.TableNameValue))] = row
	}
	return out, nil
}

func (r *Repo) popularityByTable(scope *gorm.DB, workspaceID pgtype.UUID) (map[string]float64, error) {
	var rows []model.TablePopularity
	if err := scope.Where("workspace_id = ?", UUIDString(workspaceID)).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(rows))
	for _, row := range rows {
		out[tablePopularityKey(string(row.CatalogID), string(row.Namespace), string(row.TableNameValue))] = row.Score
	}
	return out, nil
}

func (r *Repo) syncTimes(scope *gorm.DB, catalogIDs []string) (map[string]pgtype.Timestamptz, error) {
	out := make(map[string]pgtype.Timestamptz, len(catalogIDs))
	for _, catalogID := range catalogIDs {
		at, err := finishedSyncAt(scope, catalogID)
		if err != nil {
			return nil, err
		}
		out[catalogID] = at
	}
	return out, nil
}

func tableDocKey(catalogID string, namespace []string, table string) string {
	return catalogID + "\x00" + strings.Join(namespace, "\x1f") + "\x00" + table
}

func tablePopularityKey(catalogID, namespace, table string) string {
	return catalogID + "\x00" + namespace + "\x00" + table
}

func matchesDocFilters(doc model.SemanticDoc, exists bool, tags []string, certified *bool) bool {
	if tags != nil {
		if !exists {
			return false
		}
		for _, tag := range tags {
			if !containsString(doc.Tags, tag) {
				return false
			}
		}
	}
	if certified != nil {
		isCertified := exists && doc.Certified
		if isCertified != *certified {
			return false
		}
	}
	return true
}

func equalNamespace(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasPrefix(values, prefix []string) bool {
	if len(prefix) > len(values) {
		return false
	}
	return equalNamespace(values[:len(prefix)], prefix)
}

func compareNamespace(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// nameMatches reproduces the trigram match (or word similarity) of the sqlc
// query without a database extension.
func nameMatches(name, q string) bool {
	lower := strings.ToLower(name)
	query := strings.ToLower(strings.TrimSpace(q))
	if query == "" {
		return true
	}
	if strings.Contains(lower, query) {
		return true
	}
	if similarity(name, q) >= 0.3 {
		return true
	}
	for _, word := range strings.FieldsFunc(lower, func(r rune) bool {
		return !(r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}) {
		if word != "" && similarity(word, q) >= 0.5 {
			return true
		}
	}
	return false
}

// similarity is a portable trigram similarity: the Jaccard index of the
// padded trigram sets, close enough to pg_trgm for ordering names.
func similarity(a, b string) float64 {
	left, right := trigrams(a), trigrams(b)
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	shared := 0
	for gram := range left {
		if _, ok := right[gram]; ok {
			shared++
		}
	}
	return float64(shared) / float64(len(left)+len(right)-shared)
}

func trigrams(value string) map[string]struct{} {
	value = strings.ToLower(strings.TrimSpace(value))
	out := map[string]struct{}{}
	if value == "" {
		return out
	}
	runes := []rune(value)
	for i := 0; i < len(runes); i++ {
		end := i + 3
		if end > len(runes) {
			end = len(runes)
		}
		gram := string(runes[i:end])
		if utf8.RuneCountInString(gram) < 2 {
			continue
		}
		out[gram] = struct{}{}
	}
	return out
}
