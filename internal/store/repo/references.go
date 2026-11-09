package repo

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// ── statement references and joins ────────────────────────────────────────────

// GetStatementReferenceTarget returns the statement and the workspace of its
// execution, for the live persistence path.
func (r *Repo) GetStatementReferenceTarget(ctx context.Context, arg db.GetStatementReferenceTargetParams) (db.GetStatementReferenceTargetRow, error) {
	if !arg.ExecutionID.Valid {
		return db.GetStatementReferenceTargetRow{}, db.ErrNoRows
	}
	var stmt model.QueryStatement
	err := r.Users.db.WithContext(ctx).
		Where("execution_id = ? AND position = ?", UUIDString(arg.ExecutionID), arg.Position).
		First(&stmt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetStatementReferenceTargetRow{}, db.ErrNoRows
	}
	if err != nil {
		return db.GetStatementReferenceTargetRow{}, err
	}
	var exec model.QueryExecution
	err = r.Users.db.WithContext(ctx).Where("id = ?", string(stmt.ExecutionID)).First(&exec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetStatementReferenceTargetRow{}, db.ErrNoRows
	}
	if err != nil {
		return db.GetStatementReferenceTargetRow{}, err
	}
	return db.GetStatementReferenceTargetRow{
		StatementID: PGUUID(string(stmt.ID)),
		WorkspaceID: PGUUID(string(exec.WorkspaceID)),
	}, nil
}

// DeleteStatementReferences removes a statement's table references.
func (r *Repo) DeleteStatementReferences(ctx context.Context, statementID db.UUID) error {
	if !statementID.Valid {
		return nil
	}
	return r.Users.db.WithContext(ctx).
		Where("statement_id = ?", UUIDString(statementID)).
		Delete(&model.StatementReference{}).Error
}

// DeleteStatementJoins removes a statement's equi-joins.
func (r *Repo) DeleteStatementJoins(ctx context.Context, statementID db.UUID) error {
	if !statementID.Valid {
		return nil
	}
	return r.Users.db.WithContext(ctx).
		Where("statement_id = ?", UUIDString(statementID)).
		Delete(&model.StatementJoin{}).Error
}

// InsertStatementReference inserts one table reference.
func (r *Repo) InsertStatementReference(ctx context.Context, arg db.InsertStatementReferenceParams) error {
	row := model.StatementReference{
		StatementID:    model.UUID(UUIDString(arg.StatementID)),
		WorkspaceID:    model.UUID(UUIDString(arg.WorkspaceID)),
		CatalogID:      modelUUIDPtr(arg.CatalogID),
		Namespace:      model.Text(arg.Namespace),
		TableNameValue: model.Text(arg.TableName),
		Alias:          model.Text(arg.Alias),
		Kind:           model.Text(arg.Kind),
	}
	return r.Users.db.WithContext(ctx).Create(&row).Error
}

// InsertStatementJoin inserts one equi-join.
func (r *Repo) InsertStatementJoin(ctx context.Context, arg db.InsertStatementJoinParams) error {
	row := model.StatementJoin{
		StatementID: model.UUID(UUIDString(arg.StatementID)),
		WorkspaceID: model.UUID(UUIDString(arg.WorkspaceID)),
		LeftAlias:   model.Text(arg.LeftAlias),
		LeftColumn:  model.Text(arg.LeftColumn),
		RightAlias:  model.Text(arg.RightAlias),
		RightColumn: model.Text(arg.RightColumn),
	}
	return r.Users.db.WithContext(ctx).Create(&row).Error
}

// ListJoinSuggestions returns the equi-joins the workspace used between two
// tables over the last 90 days, ranked by a 30-day half-life decay. The
// aggregate is reduced in Go: the query needs no window function or SQL
// scoring expression (ADR 0020).
func (r *Repo) ListJoinSuggestions(ctx context.Context, arg db.ListJoinSuggestionsParams) ([]db.ListJoinSuggestionsRow, error) {
	now := time.Now().UTC()
	threshold := now.Add(-90 * 24 * time.Hour)
	var joins []model.StatementJoin
	if err := r.Users.db.WithContext(ctx).
		Where("workspace_id = ?", UUIDString(arg.WorkspaceID)).
		Find(&joins).Error; err != nil {
		return nil, err
	}
	if len(joins) == 0 {
		return nil, nil
	}
	stmtIDs := make([]string, 0, len(joins))
	seenStmt := map[string]bool{}
	for _, j := range joins {
		id := string(j.StatementID)
		if seenStmt[id] {
			continue
		}
		seenStmt[id] = true
		stmtIDs = append(stmtIDs, id)
	}
	var statements []model.QueryStatement
	if err := r.Users.db.WithContext(ctx).
		Where("id IN ? AND status = ?", stmtIDs, "finished").
		Find(&statements).Error; err != nil {
		return nil, err
	}
	usedAt := map[string]time.Time{}
	finished := make([]string, 0, len(statements))
	for _, st := range statements {
		at := st.StartedAt
		if st.FinishedAt != nil {
			at = *st.FinishedAt
		}
		if at.Before(threshold) {
			continue
		}
		id := string(st.ID)
		usedAt[id] = at
		finished = append(finished, id)
	}
	if len(finished) == 0 {
		return nil, nil
	}
	var refs []model.StatementReference
	if err := r.Users.db.WithContext(ctx).
		Where("statement_id IN ?", finished).
		Find(&refs).Error; err != nil {
		return nil, err
	}
	byStatement := map[string]map[string][]model.StatementReference{}
	for _, ref := range refs {
		if !catalogAllowed(ref, arg.CatalogID) {
			continue
		}
		sid := string(ref.StatementID)
		if byStatement[sid] == nil {
			byStatement[sid] = map[string][]model.StatementReference{}
		}
		alias := strings.ToLower(string(ref.Alias))
		byStatement[sid][alias] = append(byStatement[sid][alias], ref)
	}

	type pair struct {
		left, right string
	}
	agg := map[pair]*db.ListJoinSuggestionsRow{}
	for _, join := range joins {
		sid := string(join.StatementID)
		at, ok := usedAt[sid]
		if !ok {
			continue
		}
		aliases := byStatement[sid]
		if aliases == nil {
			continue
		}
		for _, left := range aliases[strings.ToLower(string(join.LeftAlias))] {
			for _, right := range aliases[strings.ToLower(string(join.RightAlias))] {
				leftColumn, rightColumn, ok := orientJoin(join, left, right, arg)
				if !ok {
					continue
				}
				key := pair{leftColumn, rightColumn}
				row := agg[key]
				if row == nil {
					row = &db.ListJoinSuggestionsRow{LeftColumn: leftColumn, RightColumn: rightColumn}
					agg[key] = row
				}
				row.UseCount++
				if !row.LastUsedAt.Valid || at.After(row.LastUsedAt.Time) {
					row.LastUsedAt = PGTime(at)
				}
				age := now.Sub(at).Seconds()
				row.Score += math.Exp(-math.Ln2 * age / (30 * 86400))
			}
		}
	}
	out := make([]db.ListJoinSuggestionsRow, 0, len(agg))
	for _, row := range agg {
		out = append(out, *row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if !out[i].LastUsedAt.Time.Equal(out[j].LastUsedAt.Time) {
			return out[i].LastUsedAt.Time.After(out[j].LastUsedAt.Time)
		}
		if out[i].LeftColumn != out[j].LeftColumn {
			return out[i].LeftColumn < out[j].LeftColumn
		}
		return out[i].RightColumn < out[j].RightColumn
	})
	if arg.Lim > 0 && len(out) > int(arg.Lim) {
		out = out[:arg.Lim]
	}
	return out, nil
}

// orientJoin normalizes a join so the left column belongs to the requested
// left table, whatever order the statement wrote it in.
func orientJoin(join model.StatementJoin, left, right model.StatementReference, arg db.ListJoinSuggestionsParams) (string, string, bool) {
	leftNs, leftTable := string(left.Namespace), string(left.TableNameValue)
	rightNs, rightTable := string(right.Namespace), string(right.TableNameValue)
	switch {
	case strings.EqualFold(leftNs, arg.LeftNs) && strings.EqualFold(leftTable, arg.LeftTable) &&
		strings.EqualFold(rightNs, arg.RightNs) && strings.EqualFold(rightTable, arg.RightTable):
		return string(join.LeftColumn), string(join.RightColumn), true
	case strings.EqualFold(rightNs, arg.LeftNs) && strings.EqualFold(rightTable, arg.LeftTable) &&
		strings.EqualFold(leftNs, arg.RightNs) && strings.EqualFold(leftTable, arg.RightTable):
		return string(join.RightColumn), string(join.LeftColumn), true
	}
	return "", "", false
}

// catalogAllowed reproduces the reference's catalog filter: a reference
// without a catalog always passes; one with a catalog needs an explicit match.
func catalogAllowed(ref model.StatementReference, catalogID db.UUID) bool {
	if ref.CatalogID == nil {
		return true
	}
	return catalogID.Valid && string(*ref.CatalogID) == UUIDString(catalogID)
}

// ListForeignKeyJoinSuggestions returns the foreign keys between two tables,
// oriented so the left column belongs to the left table.
func (r *Repo) ListForeignKeyJoinSuggestions(ctx context.Context, arg db.ListForeignKeyJoinSuggestionsParams) ([]db.ListForeignKeyJoinSuggestionsRow, error) {
	keys := map[string]db.ListForeignKeyJoinSuggestionsRow{}
	add := func(from, to string) {
		key := from + "\x00" + to
		keys[key] = db.ListForeignKeyJoinSuggestionsRow{LeftColumn: from, RightColumn: to}
	}
	fromLeft, err := r.foreignKeys(ctx, arg.LeftTableID, arg.LeftNamespace, arg.RightTable)
	if err != nil {
		return nil, err
	}
	for _, fk := range fromLeft {
		add(string(fk.ColumnName), string(fk.RefColumn))
	}
	fromRight, err := r.foreignKeys(ctx, arg.RightTableID, arg.RightNamespace, arg.LeftTable)
	if err != nil {
		return nil, err
	}
	for _, fk := range fromRight {
		add(string(fk.RefColumn), string(fk.ColumnName))
	}
	out := make([]db.ListForeignKeyJoinSuggestionsRow, 0, len(keys))
	for _, row := range keys {
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LeftColumn != out[j].LeftColumn {
			return out[i].LeftColumn < out[j].LeftColumn
		}
		return out[i].RightColumn < out[j].RightColumn
	})
	return out, nil
}

// foreignKeys returns the keys declared on a table that reference another
// table; the reference table is matched case-insensitively, as the sqlc query
// did.
func (r *Repo) foreignKeys(ctx context.Context, tableID db.UUID, namespace []string, refTable string) ([]model.MetadataForeignKey, error) {
	if !tableID.Valid {
		return nil, nil
	}
	var rows []model.MetadataForeignKey
	if err := r.Users.db.WithContext(ctx).
		Where("table_id = ? AND ref_namespace = ?", UUIDString(tableID), model.StringArray(namespace)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, row := range rows {
		if strings.EqualFold(string(row.RefTable), refTable) {
			out = append(out, row)
		}
	}
	return out, nil
}

// ── metadata, for join suggestions ────────────────────────────────────────────

// GetGrantedMetadataTable returns a live table of a catalog granted to the
// workspace, with its catalog freshness.
func (r *Repo) GetGrantedMetadataTable(ctx context.Context, arg db.GetGrantedMetadataTableParams) (db.GetGrantedMetadataTableRow, error) {
	if !arg.TableID.Valid || !arg.WorkspaceID.Valid {
		return db.GetGrantedMetadataTableRow{}, db.ErrNoRows
	}
	granted := r.Users.db.WithContext(ctx).Model(&model.WorkspaceCatalogGrant{}).
		Select("catalog_id").
		Where("workspace_id = ?", UUIDString(arg.WorkspaceID))
	var catalogs []model.Catalog
	if err := r.Users.db.WithContext(ctx).
		Where("enabled = ? AND id IN (?)", true, granted).
		Find(&catalogs).Error; err != nil {
		return db.GetGrantedMetadataTableRow{}, err
	}
	if len(catalogs) == 0 {
		return db.GetGrantedMetadataTableRow{}, db.ErrNoRows
	}
	ids := make([]string, len(catalogs))
	for i, catalog := range catalogs {
		ids[i] = string(catalog.ID)
	}
	var table model.MetadataTable
	err := r.Users.db.WithContext(ctx).
		Where("id = ? AND catalog_id IN ?", UUIDString(arg.TableID), ids).
		First(&table).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetGrantedMetadataTableRow{}, db.ErrNoRows
	}
	if err != nil {
		return db.GetGrantedMetadataTableRow{}, err
	}
	var syncedAt db.Timestamptz
	var run model.MetadataSyncRun
	err = r.Users.db.WithContext(ctx).
		Where("catalog_id = ? AND status = ? AND finished_at IS NOT NULL", string(table.CatalogID), "finished").
		Order("finished_at DESC").
		First(&run).Error
	if err == nil && run.FinishedAt != nil {
		syncedAt = PGTime(*run.FinishedAt)
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetGrantedMetadataTableRow{}, err
	}
	return db.GetGrantedMetadataTableRow{
		ID:               PGUUID(string(table.ID)),
		CatalogID:        PGUUID(string(table.CatalogID)),
		Namespace:        []string(table.Namespace),
		Name:             string(table.Name),
		Kind:             table.Kind,
		Properties:       []byte(table.Properties),
		Comment:          table.Comment,
		LastChangedAt:    PGTime(table.LastChangedAt),
		RemovedAt:        PGTimePtr(table.RemovedAt),
		MetadataSyncedAt: syncedAt,
	}, nil
}

// ── Trino profile ─────────────────────────────────────────────────────────────

// GetTrinoQuerySnapshot returns the profile Lens recorded for one engine
// query, or db.ErrNoRows.
func (r *Repo) GetTrinoQuerySnapshot(ctx context.Context, trinoQueryID string) (db.TrinoQuerySnapshot, error) {
	var row model.TrinoQuerySnapshot
	err := r.Users.db.WithContext(ctx).
		Where("trino_query_id = ?", trinoQueryID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.TrinoQuerySnapshot{}, db.ErrNoRows
	}
	if err != nil {
		return db.TrinoQuerySnapshot{}, err
	}
	return db.TrinoQuerySnapshot{
		ID:                 PGUUID(string(row.ID)),
		TrinoQueryID:       string(row.TrinoQueryID),
		ExecutionID:        uuidPtr(row.ExecutionID),
		Origin:             string(row.Origin),
		User:               string(row.User),
		State:              string(row.State),
		Sql:                row.SQL,
		Catalog:            row.Catalog,
		Schema:             row.Schema,
		CreatedAt:          PGTime(row.CreatedAt),
		ElapsedMs:          row.ElapsedMs,
		CpuMs:              row.CPUMs,
		PhysicalInputBytes: row.PhysicalInputBytes,
		OutputRows:         row.OutputRows,
		ErrorMessage:       row.ErrorMessage,
		RawTrinoPayload:    []byte(modelJSONValue(row.RawTrinoPayload)),
		FirstSeenAt:        PGTime(row.FirstSeenAt),
		LastSeenAt:         PGTime(row.LastSeenAt),
		EngineID:           PGUUID(string(row.EngineID)),
	}, nil
}
