package repo

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// ListPopularTables lists the workspace's most used tables, live and granted
// only, best first. The join and ordering run in Go so the query needs no
// PostgreSQL array indexing (ADR 0020).
func (r *Repo) ListPopularTables(ctx context.Context, arg db.ListPopularTablesParams) ([]db.ListPopularTablesRow, error) {
	var out []db.ListPopularTablesRow
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		visible, err := grantedCatalogIDs(tx, arg.WorkspaceID)
		if err != nil || len(visible) == 0 {
			return err
		}
		var popular []model.TablePopularity
		if err := tx.Where("workspace_id = ?", UUIDString(arg.WorkspaceID)).
			Find(&popular).Error; err != nil {
			return err
		}
		if len(popular) == 0 {
			return nil
		}
		var tables []model.MetadataTable
		if err := tx.Where("catalog_id IN ? AND removed_at IS NULL", visible).
			Find(&tables).Error; err != nil {
			return err
		}
		byKey := make(map[string]model.MetadataTable, len(tables))
		for _, table := range tables {
			namespace := []string(table.Namespace)
			last := ""
			if len(namespace) > 0 {
				last = namespace[len(namespace)-1]
			}
			byKey[tablePopularityKey(string(table.CatalogID), last, string(table.Name))] = table
		}
		synced, err := r.syncTimes(tx, visible)
		if err != nil {
			return err
		}
		out = make([]db.ListPopularTablesRow, 0, len(popular))
		for _, p := range popular {
			table, ok := byKey[tablePopularityKey(string(p.CatalogID), string(p.Namespace), string(p.TableNameValue))]
			if !ok {
				continue
			}
			out = append(out, db.ListPopularTablesRow{
				TableID:          PGUUID(string(table.ID)),
				CatalogID:        PGUUID(string(table.CatalogID)),
				Namespace:        []string(table.Namespace),
				Name:             string(table.Name),
				Kind:             table.Kind,
				Uses:             p.Uses,
				Score:            p.Score,
				LastUsedAt:       PGTime(p.LastUsedAt),
				MetadataSyncedAt: synced[string(p.CatalogID)],
			})
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Score != out[j].Score {
				return out[i].Score > out[j].Score
			}
			if !out[i].LastUsedAt.Time.Equal(out[j].LastUsedAt.Time) {
				return out[i].LastUsedAt.Time.After(out[j].LastUsedAt.Time)
			}
			return out[i].Name < out[j].Name
		})
		if arg.Lim >= 0 && len(out) > int(arg.Lim) {
			out = out[:arg.Lim]
		}
		return nil
	}, nil)
	return out, err
}

// DeleteTablePopularity clears the popularity snapshot.
func (r *Repo) DeleteTablePopularity(ctx context.Context) error {
	return r.Users.db.WithContext(ctx).
		Session(&gorm.Session{AllowGlobalUpdate: true}).
		Delete(&model.TablePopularity{}).Error
}

// InsertTablePopularity recomputes the popularity snapshot from the reads of
// the last 30 days, with a 7-day half-life decay. The aggregation runs in Go;
// the sqlc query's exp()/interval arithmetic is not portable (ADR 0020).
func (r *Repo) InsertTablePopularity(ctx context.Context) error {
	now := time.Now().UTC()
	cutoff := now.Add(-30 * 24 * time.Hour)
	var refs []model.StatementReference
	if err := r.Users.db.WithContext(ctx).
		Where("kind = ? AND catalog_id IS NOT NULL", "read").
		Find(&refs).Error; err != nil {
		return err
	}
	if len(refs) == 0 {
		return nil
	}
	statementIDs := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		id := string(ref.StatementID)
		if seen[id] {
			continue
		}
		seen[id] = true
		statementIDs = append(statementIDs, id)
	}
	var statements []model.QueryStatement
	if err := r.Users.db.WithContext(ctx).
		Where("id IN ? AND status = ?", statementIDs, "finished").
		Find(&statements).Error; err != nil {
		return err
	}
	byID := make(map[string]model.QueryStatement, len(statements))
	for _, statement := range statements {
		byID[string(statement.ID)] = statement
	}
	type aggregate struct {
		uses     int64
		score    float64
		lastUsed time.Time
	}
	groups := map[string]*aggregate{}
	for _, ref := range refs {
		statement, ok := byID[string(ref.StatementID)]
		if !ok || ref.CatalogID == nil {
			continue
		}
		usedAt := statement.StartedAt
		if statement.FinishedAt != nil {
			usedAt = *statement.FinishedAt
		}
		if usedAt.Before(cutoff) {
			continue
		}
		key := string(ref.WorkspaceID) + "\x00" + tablePopularityKey(string(*ref.CatalogID), string(ref.Namespace), string(ref.TableNameValue))
		agg := groups[key]
		if agg == nil {
			agg = &aggregate{}
			groups[key] = agg
		}
		agg.uses++
		days := now.Sub(usedAt).Hours() / 24
		agg.score += math.Exp(-math.Ln2 * days / 7)
		if usedAt.After(agg.lastUsed) {
			agg.lastUsed = usedAt
		}
	}
	rows := make([]model.TablePopularity, 0, len(groups))
	for _, ref := range refs {
		if ref.CatalogID == nil {
			continue
		}
		key := string(ref.WorkspaceID) + "\x00" + tablePopularityKey(string(*ref.CatalogID), string(ref.Namespace), string(ref.TableNameValue))
		agg := groups[key]
		if agg == nil {
			continue
		}
		delete(groups, key)
		rows = append(rows, model.TablePopularity{
			WorkspaceID:    ref.WorkspaceID,
			CatalogID:      *ref.CatalogID,
			Namespace:      ref.Namespace,
			TableNameValue: ref.TableNameValue,
			Uses:           agg.uses,
			Score:          agg.score,
			LastUsedAt:     agg.lastUsed,
			ComputedAt:     now,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	return r.Users.db.WithContext(ctx).Create(&rows).Error
}

// RefreshTablePopularity replaces the snapshot atomically, so a reader sees
// either the previous snapshot or the new one, never an empty table.
func (r *Repo) RefreshTablePopularity(ctx context.Context) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		store := New(tx)
		if err := store.DeleteTablePopularity(ctx); err != nil {
			return err
		}
		return store.InsertTablePopularity(ctx)
	})
}

// compile-time check that the popularity read keeps the sqlc signature.
var _ func(context.Context, db.ListPopularTablesParams) ([]db.ListPopularTablesRow, error) = (*Repo)(nil).ListPopularTables
