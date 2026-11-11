package repo

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/content/search"
	"github.com/galileostudio/baleia/internal/db"
	textsearch "github.com/galileostudio/baleia/internal/search"
	"github.com/galileostudio/baleia/internal/semantic/docs"
	"github.com/galileostudio/baleia/internal/semantic/metrics"
	"github.com/galileostudio/baleia/internal/semantic/models"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// The semantic services read through these narrow interfaces; *Repo
// implements each of them (ADR 0020, #772).
var (
	_ docs.Store    = (*Repo)(nil)
	_ metrics.Store = (*Repo)(nil)
	_ models.Store  = (*Repo)(nil)
	_ search.Store  = (*Repo)(nil)
)

// ── docs ──────────────────────────────────────────────────────────────────────

// GetSemanticDoc returns the doc of a table (ColumnName nil) or one of its
// columns, or db.ErrNoRows.
func (r *Repo) GetSemanticDoc(ctx context.Context, arg db.GetSemanticDocParams) (db.SemanticDoc, error) {
	if !arg.WorkspaceID.Valid || !arg.CatalogID.Valid {
		return db.SemanticDoc{}, db.ErrNoRows
	}
	return r.getSemanticDoc(ctx, r.Users.db.WithContext(ctx), arg)
}

// ListSemanticDocsForTable returns every doc of one table, by column with the
// table-level doc first. A nil ColumnName lists the table and all columns; a
// non-nil one narrows the result to that column.
func (r *Repo) ListSemanticDocsForTable(ctx context.Context, arg db.ListSemanticDocsForTableParams) ([]db.SemanticDoc, error) {
	if !arg.WorkspaceID.Valid || !arg.CatalogID.Valid {
		return nil, nil
	}
	q := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND catalog_id = ? AND namespace = ? AND table_name = ?",
			UUIDString(arg.WorkspaceID), UUIDString(arg.CatalogID), model.StringArray(arg.Namespace), arg.TableName)
	if arg.ColumnName != nil {
		q = q.Where("column_name = ?", *arg.ColumnName)
	}
	var rows []model.SemanticDoc
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	// ORDER BY column_name NULLS FIRST is dialect-specific; the same order is
	// produced in Go (ADR 0020).
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].ColumnName, rows[j].ColumnName
		switch {
		case a == nil && b == nil:
			return false
		case a == nil:
			return true
		case b == nil:
			return false
		default:
			return string(*a) < string(*b)
		}
	})
	out := make([]db.SemanticDoc, len(rows))
	for i, row := range rows {
		out[i] = toDBSemanticDoc(row)
	}
	return out, nil
}

// UpsertSemanticDoc inserts or replaces the doc at its natural key. A
// certification keeps its first certified_by/certified_at, as the sqlc
// ON CONFLICT clause did.
func (r *Repo) UpsertSemanticDoc(ctx context.Context, arg db.UpsertSemanticDocParams) (db.SemanticDoc, error) {
	if !arg.WorkspaceID.Valid || !arg.CatalogID.Valid {
		return db.SemanticDoc{}, db.ErrNoRows
	}
	var out db.SemanticDoc
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		getParams := db.GetSemanticDocParams{
			WorkspaceID: arg.WorkspaceID, CatalogID: arg.CatalogID, Namespace: arg.Namespace,
			TableName: arg.TableName, ColumnName: arg.ColumnName,
		}
		current, err := r.getSemanticDoc(ctx, tx, getParams)
		if err != nil && !errors.Is(err, db.ErrNoRows) {
			return err
		}
		exists := err == nil
		tags := arg.Tags
		if tags == nil {
			tags = []string{}
		}
		if exists {
			certifiedBy := current.CertifiedBy
			certifiedAt := current.CertifiedAt
			if arg.Certified {
				if !certifiedBy.Valid {
					certifiedBy = arg.UpdatedBy
				}
				if !certifiedAt.Valid {
					certifiedAt = PGTime(now)
				}
			} else {
				certifiedBy = db.UUID{}
				certifiedAt = db.Timestamptz{}
			}
			updates := map[string]any{
				"description":   arg.Description,
				"tags":          model.StringArray(tags),
				"owner_user_id": modelUUIDPtr(arg.OwnerUserID),
				"updated_by":    model.UUID(UUIDString(arg.UpdatedBy)),
				"certified":     arg.Certified,
				"certified_by":  modelUUIDPtr(certifiedBy),
				"certified_at":  TimePtr(certifiedAt),
				"updated_at":    now,
			}
			if err := tx.Model(&model.SemanticDoc{}).
				Where("id = ?", UUIDString(current.ID)).
				Updates(updates).Error; err != nil {
				return err
			}
			out, err = r.getSemanticDoc(ctx, tx, getParams)
			return err
		}
		row := model.SemanticDoc{
			ID:             model.UUID(NewUUID()),
			WorkspaceID:    model.UUID(UUIDString(arg.WorkspaceID)),
			CatalogID:      model.UUID(UUIDString(arg.CatalogID)),
			Namespace:      model.StringArray(arg.Namespace),
			TableNameValue: model.Text(arg.TableName),
			ColumnName:     modelTextPtr(arg.ColumnName),
			Description:    model.Text(arg.Description),
			Tags:           model.StringArray(tags),
			OwnerUserID:    modelUUIDPtr(arg.OwnerUserID),
			UpdatedBy:      model.UUID(UUIDString(arg.UpdatedBy)),
			UpdatedAt:      now,
			Certified:      arg.Certified,
		}
		if arg.Certified {
			row.CertifiedBy = modelUUIDPtr(arg.UpdatedBy)
			row.CertifiedAt = &now
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		out = toDBSemanticDoc(row)
		return nil
	})
	return out, err
}

// DeleteSemanticDoc removes the doc at its natural key and returns the rows.
func (r *Repo) DeleteSemanticDoc(ctx context.Context, arg db.DeleteSemanticDocParams) (int64, error) {
	if !arg.WorkspaceID.Valid || !arg.CatalogID.Valid {
		return 0, nil
	}
	q := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND catalog_id = ? AND namespace = ? AND table_name = ?",
			UUIDString(arg.WorkspaceID), UUIDString(arg.CatalogID), model.StringArray(arg.Namespace), arg.TableName)
	if arg.ColumnName == nil {
		q = q.Where("column_name IS NULL")
	} else {
		q = q.Where("column_name = ?", *arg.ColumnName)
	}
	res := q.Delete(&model.SemanticDoc{})
	return res.RowsAffected, res.Error
}

// SearchSemanticDocs finds docs of catalogs the workspace may use whose
// description, tags, table or column match q (#941). The docs index proposes
// candidates (ADR 0023); they are read live, filtered by the catalogs granted
// to the workspace, scored and ordered in Go so search stays portable
// (ADR 0020).
func (r *Repo) SearchSemanticDocs(ctx context.Context, arg db.SearchSemanticDocsParams) ([]db.SearchSemanticDocsRow, error) {
	if !arg.WorkspaceID.Valid || arg.Lim <= 0 {
		return nil, nil
	}
	workspace := UUIDString(arg.WorkspaceID)
	owner := model.UUID(workspace)
	if err := r.refreshScope(ctx, scopeDocs+workspace, scopeOwner{workspace: &owner}, docsBuilder(workspace)); err != nil {
		return nil, err
	}
	pattern := textsearch.Compile(arg.Q)
	var out []db.SearchSemanticDocsRow
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		visible, err := grantedCatalogIDs(tx, arg.WorkspaceID)
		if err != nil || len(visible) == 0 {
			return err
		}
		found, err := r.candidates(tx, []string{scopeDocs + workspace}, []string{kindDoc}, pattern, int(arg.Lim))
		if err != nil || len(found[kindDoc]) == 0 {
			return err
		}
		var rows []model.SemanticDoc
		err = inChunks(found[kindDoc], func(chunk []string) error {
			var part []model.SemanticDoc
			if err := tx.Where("workspace_id = ? AND catalog_id IN ? AND id IN ?", workspace, visible, chunk).
				Find(&part).Error; err != nil {
				return err
			}
			rows = append(rows, part...)
			return nil
		})
		if err != nil {
			return err
		}
		hits := make([]db.SearchSemanticDocsRow, 0, len(rows))
		for _, row := range rows {
			if arg.CatalogID.Valid && string(row.CatalogID) != UUIDString(arg.CatalogID) {
				continue
			}
			if arg.Tag != nil && !containsString(row.Tags, *arg.Tag) {
				continue
			}
			if arg.Certified != nil && row.Certified != *arg.Certified {
				continue
			}
			owner := uuidPtr(row.OwnerUserID)
			if arg.OwnerID.Valid && (!owner.Valid || owner != arg.OwnerID) {
				continue
			}
			fields := []textsearch.Field{
				{Text: string(row.TableNameValue), Weight: 1},
				{Text: string(row.Description), Weight: 1},
				{Text: strings.Join(row.Tags, " "), Weight: tagWeight},
			}
			if row.ColumnName != nil {
				fields = append(fields, textsearch.Field{Text: string(*row.ColumnName), Weight: 1})
			}
			score := pattern.Best(fields...)
			if score == 0 {
				continue
			}
			hits = append(hits, toDBSearchSemanticDocsRow(row, score))
		}
		sort.SliceStable(hits, func(i, j int) bool {
			if hits[i].Score == hits[j].Score {
				return hits[i].UpdatedAt.Time.After(hits[j].UpdatedAt.Time)
			}
			return hits[i].Score > hits[j].Score
		})
		if len(hits) > int(arg.Lim) {
			hits = hits[:arg.Lim]
		}
		out = hits
		return nil
	}, nil)
	return out, err
}

// tagWeight scales a match on a doc's tags below its name and description.
const tagWeight = 0.7

// ListSemanticTags lists the tags of docs on catalogs the workspace may use,
// prefix matches first, with their counts.
func (r *Repo) ListSemanticTags(ctx context.Context, arg db.ListSemanticTagsParams) ([]db.ListSemanticTagsRow, error) {
	if !arg.WorkspaceID.Valid || arg.Lim <= 0 {
		return nil, nil
	}
	var out []db.ListSemanticTagsRow
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		visible, err := grantedCatalogIDs(tx, arg.WorkspaceID)
		if err != nil || len(visible) == 0 {
			return err
		}
		var rows []model.SemanticDoc
		if err := tx.Where("workspace_id = ? AND catalog_id IN ?", UUIDString(arg.WorkspaceID), visible).
			Find(&rows).Error; err != nil {
			return err
		}
		counts := map[string]int64{}
		for _, row := range rows {
			for _, tag := range row.Tags {
				counts[tag]++
			}
		}
		q := strings.ToLower(arg.Q)
		out = make([]db.ListSemanticTagsRow, 0, len(counts))
		for tag, count := range counts {
			lower := strings.ToLower(tag)
			if q != "" && !strings.HasPrefix(lower, q) && !strings.Contains(lower, q) {
				continue
			}
			out = append(out, db.ListSemanticTagsRow{Tag: tag, Count: count})
		}
		sort.SliceStable(out, func(i, j int) bool {
			pi := q != "" && strings.HasPrefix(strings.ToLower(out[i].Tag), q)
			pj := q != "" && strings.HasPrefix(strings.ToLower(out[j].Tag), q)
			if pi != pj {
				return pi
			}
			if out[i].Count != out[j].Count {
				return out[i].Count > out[j].Count
			}
			return out[i].Tag < out[j].Tag
		})
		if len(out) > int(arg.Lim) {
			out = out[:arg.Lim]
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return out, err
}

// ── metrics ───────────────────────────────────────────────────────────────────

// ListVisibleSemanticMetrics returns the metrics in spaces the user may see,
// optionally narrowed to one space, by label.
func (r *Repo) ListVisibleSemanticMetrics(ctx context.Context, arg db.ListVisibleSemanticMetricsParams) ([]db.SemanticMetric, error) {
	var out []db.SemanticMetric
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		spaces, err := visibleSpaces(tx, arg.WorkspaceID, arg.UserID)
		if err != nil || len(spaces) == 0 {
			return err
		}
		q := tx.Where("workspace_id = ? AND space_id IN ?", UUIDString(arg.WorkspaceID), spaces)
		if arg.SpaceID.Valid {
			q = q.Where("space_id = ?", UUIDString(arg.SpaceID))
		}
		if len(arg.IDs) > 0 {
			q = q.Where("id IN ?", uuidStrings(arg.IDs))
		}
		var rows []model.SemanticMetric
		if err := q.Order("label").Find(&rows).Error; err != nil {
			return err
		}
		out = make([]db.SemanticMetric, len(rows))
		for i, row := range rows {
			out[i] = toDBSemanticMetric(row)
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return out, err
}

// ListSemanticMetricVersions returns the latest content version of every
// metric in the workspace. The per-entity maximum is reduced in Go (ADR 0020).
func (r *Repo) ListSemanticMetricVersions(ctx context.Context, workspaceID db.UUID) ([]db.ListSemanticMetricVersionsRow, error) {
	return r.listContentVersions(ctx, workspaceID, "semantic_metric")
}

// SetSemanticMetricCertification records who certified a metric and when, or
// clears both when uncertifying.
func (r *Repo) SetSemanticMetricCertification(ctx context.Context, arg db.SetSemanticMetricCertificationParams) (db.SemanticMetric, error) {
	if !arg.WorkspaceID.Valid || !arg.ID.Valid {
		return db.SemanticMetric{}, db.ErrNoRows
	}
	updates := map[string]any{
		"certified":    arg.Certified,
		"certified_by": nil,
		"certified_at": nil,
	}
	if arg.Certified {
		updates["certified_by"] = model.UUID(UUIDString(arg.Actor))
		updates["certified_at"] = time.Now().UTC()
	}
	res := r.Users.db.WithContext(ctx).Model(&model.SemanticMetric{}).
		Where("workspace_id = ? AND id = ?", UUIDString(arg.WorkspaceID), UUIDString(arg.ID)).
		Updates(updates)
	if res.Error != nil {
		return db.SemanticMetric{}, res.Error
	}
	if res.RowsAffected == 0 {
		return db.SemanticMetric{}, db.ErrNoRows
	}
	return r.GetSemanticMetric(ctx, db.GetSemanticMetricParams{WorkspaceID: arg.WorkspaceID, ID: arg.ID})
}

// ── models ────────────────────────────────────────────────────────────────────

// ListVisibleSemanticModels returns the models in spaces the user may see on
// catalogs the workspace may use, optionally narrowed to one space, by label.
func (r *Repo) ListVisibleSemanticModels(ctx context.Context, arg db.ListVisibleSemanticModelsParams) ([]db.SemanticModel, error) {
	var out []db.SemanticModel
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		spaces, err := visibleSpaces(tx, arg.WorkspaceID, arg.UserID)
		if err != nil || len(spaces) == 0 {
			return err
		}
		catalogs, err := grantedCatalogIDs(tx, arg.WorkspaceID)
		if err != nil || len(catalogs) == 0 {
			return err
		}
		q := tx.Where("workspace_id = ? AND space_id IN ? AND catalog_id IN ?",
			UUIDString(arg.WorkspaceID), spaces, catalogs)
		if arg.SpaceID.Valid {
			q = q.Where("space_id = ?", UUIDString(arg.SpaceID))
		}
		if len(arg.IDs) > 0 {
			q = q.Where("id IN ?", uuidStrings(arg.IDs))
		}
		var rows []model.SemanticModel
		if err := q.Order("label").Find(&rows).Error; err != nil {
			return err
		}
		out = make([]db.SemanticModel, len(rows))
		for i, row := range rows {
			out[i] = toDBSemanticModel(row)
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return out, err
}

// ListSemanticModelVersions returns the latest content version of every model
// in the workspace.
func (r *Repo) ListSemanticModelVersions(ctx context.Context, workspaceID db.UUID) ([]db.ListSemanticModelVersionsRow, error) {
	versions, err := r.listContentVersions(ctx, workspaceID, "semantic_model")
	if err != nil {
		return nil, err
	}
	out := make([]db.ListSemanticModelVersionsRow, len(versions))
	for i, v := range versions {
		out[i] = db.ListSemanticModelVersionsRow(v)
	}
	return out, nil
}

// SetSemanticModelCertification records who certified a model and when, or
// clears both when uncertifying.
func (r *Repo) SetSemanticModelCertification(ctx context.Context, arg db.SetSemanticModelCertificationParams) (db.SemanticModel, error) {
	if !arg.WorkspaceID.Valid || !arg.ID.Valid {
		return db.SemanticModel{}, db.ErrNoRows
	}
	updates := map[string]any{
		"certified":    arg.Certified,
		"certified_by": nil,
		"certified_at": nil,
	}
	if arg.Certified {
		updates["certified_by"] = model.UUID(UUIDString(arg.Actor))
		updates["certified_at"] = time.Now().UTC()
	}
	res := r.Users.db.WithContext(ctx).Model(&model.SemanticModel{}).
		Where("workspace_id = ? AND id = ?", UUIDString(arg.WorkspaceID), UUIDString(arg.ID)).
		Updates(updates)
	if res.Error != nil {
		return db.SemanticModel{}, res.Error
	}
	if res.RowsAffected == 0 {
		return db.SemanticModel{}, db.ErrNoRows
	}
	return r.GetSemanticModel(ctx, db.GetSemanticModelParams{WorkspaceID: arg.WorkspaceID, ID: arg.ID})
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (r *Repo) getSemanticDoc(ctx context.Context, scope *gorm.DB, arg db.GetSemanticDocParams) (db.SemanticDoc, error) {
	q := scope.Where("workspace_id = ? AND catalog_id = ? AND namespace = ? AND table_name = ?",
		UUIDString(arg.WorkspaceID), UUIDString(arg.CatalogID), model.StringArray(arg.Namespace), arg.TableName)
	if arg.ColumnName == nil {
		q = q.Where("column_name IS NULL")
	} else {
		q = q.Where("column_name = ?", *arg.ColumnName)
	}
	var row model.SemanticDoc
	err := q.First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.SemanticDoc{}, db.ErrNoRows
	}
	if err != nil {
		return db.SemanticDoc{}, err
	}
	return toDBSemanticDoc(row), nil
}

// listContentVersions returns the latest version per entity of one kind. The
// highest version per entity is reduced in Go, so the query needs no GROUP BY.
func (r *Repo) listContentVersions(ctx context.Context, workspaceID db.UUID, entityType string) ([]db.ListSemanticMetricVersionsRow, error) {
	if !workspaceID.Valid {
		return nil, nil
	}
	var rows []model.ContentVersion
	if err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND entity_type = ?", UUIDString(workspaceID), entityType).
		Order("entity_id, version DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ListSemanticMetricVersionsRow, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		id := string(row.EntityID)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, db.ListSemanticMetricVersionsRow{EntityID: PGUUID(id), Version: row.Version})
	}
	return out, nil
}

// grantedCatalogIDs is the Go replacement for baleia_workspace_catalog_ids:
// the enabled catalogs granted to the workspace.
func grantedCatalogIDs(scope *gorm.DB, workspaceID db.UUID) ([]string, error) {
	if !workspaceID.Valid {
		return nil, nil
	}
	granted := scope.Model(&model.WorkspaceCatalogGrant{}).
		Select("catalog_id").
		Where("workspace_id = ?", UUIDString(workspaceID))
	var ids []string
	err := scope.Model(&model.Catalog{}).
		Where("enabled = ? AND id IN (?)", true, granted).
		Pluck("id", &ids).Error
	return ids, err
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func toDBSemanticDoc(d model.SemanticDoc) db.SemanticDoc {
	return db.SemanticDoc{
		ID:          PGUUID(string(d.ID)),
		WorkspaceID: PGUUID(string(d.WorkspaceID)),
		CatalogID:   PGUUID(string(d.CatalogID)),
		Namespace:   []string(d.Namespace),
		TableName:   string(d.TableNameValue),
		ColumnName:  textPtr(d.ColumnName),
		Description: string(d.Description),
		Tags:        []string(d.Tags),
		OwnerUserID: uuidPtr(d.OwnerUserID),
		UpdatedBy:   PGUUID(string(d.UpdatedBy)),
		UpdatedAt:   PGTime(d.UpdatedAt),
		Certified:   d.Certified,
		CertifiedBy: uuidPtr(d.CertifiedBy),
		CertifiedAt: PGTimePtr(d.CertifiedAt),
	}
}

func toDBSearchSemanticDocsRow(d model.SemanticDoc, score float64) db.SearchSemanticDocsRow {
	doc := toDBSemanticDoc(d)
	return db.SearchSemanticDocsRow{
		ID:          doc.ID,
		WorkspaceID: doc.WorkspaceID,
		CatalogID:   doc.CatalogID,
		Namespace:   doc.Namespace,
		TableName:   doc.TableName,
		ColumnName:  doc.ColumnName,
		Description: doc.Description,
		Tags:        doc.Tags,
		OwnerUserID: doc.OwnerUserID,
		UpdatedBy:   doc.UpdatedBy,
		UpdatedAt:   doc.UpdatedAt,
		Certified:   doc.Certified,
		CertifiedBy: doc.CertifiedBy,
		CertifiedAt: doc.CertifiedAt,
		Score:       score,
	}
}
