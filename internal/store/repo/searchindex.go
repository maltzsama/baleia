package repo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	contentsearch "github.com/galileostudio/baleia/internal/content/search"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/search"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The search index (ADR 0023) is a term index in two tables, rebuilt per
// scope when the scope's source changes. It only proposes candidate rows;
// every search reads the candidates from their live tables, applies its
// visibility predicates and scores them there.

// Scope key prefixes and the kinds of indexed rows.
const (
	scopeMetadata = "metadata:"
	scopeDocs     = "docs:"
	scopeContent  = "content:"

	kindTable        = "table"
	kindColumn       = "column"
	kindDoc          = "doc"
	kindSavedQuery   = "saved_query"
	kindMetric       = "semantic_metric"
	kindModel        = "semantic_model"
	termBatchSize    = 10000 // four columns each: under the 65535 parameters of one statement
	maxCandidates    = 5000  // per lookup: exact terms, prefixes, fuzzy terms
	maxFuzzyTerms    = 20000
	maxSQLTerms      = 300  // words of a saved query's SQL that are indexed
	indexPageSize    = 5000 // tables read per page while building a metadata scope
	columnPageTables = 200  // tables whose columns are read at once
	nilUUID          = "00000000-0000-0000-0000-000000000000"
)

// indexRow is one row of a scope with the texts its terms come from.
type indexRow struct {
	Kind  string
	Ref   string
	Texts []string
}

// scopeOwner is what a scope belongs to, for cascading deletes.
type scopeOwner struct {
	catalog   *model.UUID
	workspace *model.UUID
}

// scopeBuilder produces a scope's fingerprint and rows from its source.
type scopeBuilder struct {
	fingerprint func(tx *gorm.DB) (string, error)
	rows        func(tx *gorm.DB, emit func(indexRow) error) error
}

// refreshScope brings the terms of a scope up to date with its source.
//
// A fresh scope costs two plain reads and no lock, so concurrent searches do
// not queue on it. A stale one is rebuilt once per process (searches of the
// same scope wait on the rebuild without holding a database connection) and,
// across replicas, under a row lock on the scope that the others find fresh
// when they get it. The rebuild is detached from the request that started it:
// the searches waiting for it must not fail with that request's cancellation.
func (r *Repo) refreshScope(ctx context.Context, key string, owner scopeOwner, b scopeBuilder) error {
	fresh, err := r.scopeIsFresh(ctx, key, b)
	if err != nil || fresh {
		return err
	}
	_, err, _ = r.searchBuilds.Do(key, func() (any, error) {
		return nil, r.rebuildScope(context.WithoutCancel(ctx), key, owner, b)
	})
	return err
}

// scopeIsFresh reports whether the scope was built from its source's current
// state. It reads, and takes no lock.
func (r *Repo) scopeIsFresh(ctx context.Context, key string, b scopeBuilder) (bool, error) {
	conn := r.Users.db.WithContext(ctx)
	var scope model.SearchScope
	err := conn.Where("scope_key = ?", key).Take(&scope).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read search scope %s: %w", key, err)
	}
	if scope.IndexedAt == nil {
		return false, nil
	}
	fingerprint, err := b.fingerprint(conn)
	if err != nil {
		return false, err
	}
	return scope.Fingerprint == fingerprint, nil
}

// rebuildScope rebuilds a scope's terms in its own read-committed transaction
// under a row lock on the scope, after checking again that it is still stale.
func (r *Repo) rebuildScope(ctx context.Context, key string, owner scopeOwner, b scopeBuilder) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		seed := model.SearchScope{ScopeKey: key, CatalogID: owner.catalog, WorkspaceID: owner.workspace}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return fmt.Errorf("create search scope %s: %w", key, err)
		}
		var scope model.SearchScope
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("scope_key = ?", key).First(&scope).Error; err != nil {
			return fmt.Errorf("lock search scope %s: %w", key, err)
		}
		fingerprint, err := b.fingerprint(tx)
		if err != nil {
			return err
		}
		if scope.IndexedAt != nil && scope.Fingerprint == fingerprint {
			return nil
		}
		if err := tx.Where("scope_key = ?", key).Delete(&model.SearchTerm{}).Error; err != nil {
			return fmt.Errorf("clear search terms %s: %w", key, err)
		}
		batch := make([]model.SearchTerm, 0, termBatchSize)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&batch).Error
			batch = batch[:0]
			return err
		}
		emit := func(row indexRow) error {
			for _, term := range search.Terms(row.Texts...) {
				batch = append(batch, model.SearchTerm{ScopeKey: key, Term: model.Term(term), Kind: row.Kind, RefID: row.Ref})
				if len(batch) == termBatchSize {
					if err := flush(); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if err := b.rows(tx, emit); err != nil {
			return err
		}
		if err := flush(); err != nil {
			return fmt.Errorf("store search terms %s: %w", key, err)
		}
		now := time.Now().UTC()
		return tx.Model(&model.SearchScope{}).Where("scope_key = ?", key).
			Updates(map[string]any{"fingerprint": fingerprint, "indexed_at": now}).Error
	})
}

// candidates are the refs, by kind, of rows of scopes with a term that matches
// the pattern: exactly, by prefix and, when those find fewer than want rows,
// by a close spelling. The index proposes; the caller reads and scores them.
func (r *Repo) candidates(tx *gorm.DB, scopes, kinds []string, p search.Pattern, want int) (map[string][]string, error) {
	if len(scopes) == 0 || len(kinds) == 0 || p.IsEmpty() {
		return map[string][]string{}, nil
	}
	found := map[string]map[string]struct{}{}
	total := 0
	add := func(rows []model.SearchTerm) {
		for _, row := range rows {
			refs := found[row.Kind]
			if refs == nil {
				refs = map[string]struct{}{}
				found[row.Kind] = refs
			}
			if _, ok := refs[row.RefID]; !ok {
				refs[row.RefID] = struct{}{}
				total++
			}
		}
	}
	base := func() *gorm.DB {
		return tx.Model(&model.SearchTerm{}).Distinct("kind", "ref_id").
			Where("scope_key IN ? AND kind IN ?", scopes, kinds).Limit(maxCandidates)
	}
	exact, prefixes := p.Lookup()
	if len(exact) > 0 {
		var rows []model.SearchTerm
		if err := base().Where("term IN ?", exact).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("search exact terms: %w", err)
		}
		add(rows)
	}
	if len(prefixes) > 0 {
		var rows []model.SearchTerm
		if err := base().Where(prefixLike(prefixes)).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("search term prefixes: %w", err)
		}
		add(rows)
	}
	if total < want {
		near, err := r.nearTerms(tx, scopes, p)
		if err != nil {
			return nil, err
		}
		if len(near) > 0 {
			var rows []model.SearchTerm
			if err := base().Where("term IN ?", near).Scan(&rows).Error; err != nil {
				return nil, fmt.Errorf("search near terms: %w", err)
			}
			add(rows)
		}
	}
	out := make(map[string][]string, len(found))
	for kind, refs := range found {
		list := make([]string, 0, len(refs))
		for ref := range refs {
			list = append(list, ref)
		}
		out[kind] = list
	}
	return out, nil
}

// prefixLike is a condition that matches a term starting with any prefix.
// Terms hold only letters and digits, so a prefix needs no LIKE escaping.
func prefixLike(prefixes []string) clause.Expression {
	conds := make([]clause.Expression, len(prefixes))
	for i, prefix := range prefixes {
		conds[i] = clause.Like{Column: clause.Column{Name: "term"}, Value: prefix + "%"}
	}
	return clause.Or(conds...)
}

// ── metadata ───────────────────────────────────────────────────────────────

// metadataScopeKey is the scope of a catalog's tables and columns.
func metadataScopeKey(catalogID string) string { return scopeMetadata + catalogID }

// columnRef is the ref of a column row: its table and ordinal.
func columnRef(tableID string, ordinal int32) string {
	return tableID + ":" + strconv.Itoa(int(ordinal))
}

// splitColumnRef undoes columnRef.
func splitColumnRef(ref string) (tableID string, ordinal int32, ok bool) {
	id, rest, found := strings.Cut(ref, ":")
	n, err := strconv.ParseInt(rest, 10, 32)
	if !found || err != nil {
		return "", 0, false
	}
	return id, int32(n), true
}

// metadataBuilder builds a catalog's scope from its live tables and columns.
func metadataBuilder(catalogID string) scopeBuilder {
	return scopeBuilder{
		fingerprint: func(tx *gorm.DB) (string, error) {
			tables := func() *gorm.DB { return tx.Model(&model.MetadataTable{}).Where("catalog_id = ?", catalogID) }
			var live int64
			if err := tables().Where("removed_at IS NULL").Count(&live).Error; err != nil {
				return "", fmt.Errorf("fingerprint metadata %s: %w", catalogID, err)
			}
			changed, err := newest(tables(), "last_changed_at")
			if err != nil {
				return "", fmt.Errorf("fingerprint metadata %s: %w", catalogID, err)
			}
			removed, err := newest(tables().Where("removed_at IS NOT NULL"), "removed_at")
			if err != nil {
				return "", fmt.Errorf("fingerprint metadata %s: %w", catalogID, err)
			}
			return fmt.Sprintf("%d|%s|%s", live, stamp(changed), stamp(removed)), nil
		},
		rows: func(tx *gorm.DB, emit func(indexRow) error) error {
			// Read in pages and never keep a cursor open: emit writes on this
			// transaction's connection.
			tableIDs := []string{}
			last := nilUUID
			for {
				var page []model.MetadataTable
				if err := tx.Select("id", "name", "comment").
					Where("catalog_id = ? AND removed_at IS NULL AND id > ?", catalogID, last).
					Order("id").Limit(indexPageSize).Find(&page).Error; err != nil {
					return fmt.Errorf("read tables to index: %w", err)
				}
				for _, t := range page {
					tableIDs = append(tableIDs, string(t.ID))
					if err := emit(indexRow{Kind: kindTable, Ref: string(t.ID), Texts: []string{string(t.Name), deref(t.Comment)}}); err != nil {
						return err
					}
				}
				if len(page) < indexPageSize {
					break
				}
				last = string(page[len(page)-1].ID)
			}
			return inChunksOf(tableIDs, columnPageTables, func(chunk []string) error {
				var columns []model.MetadataColumn
				if err := tx.Select("table_id", "ordinal", "name", "comment").
					Where("table_id IN ?", chunk).Find(&columns).Error; err != nil {
					return fmt.Errorf("read columns to index: %w", err)
				}
				for _, c := range columns {
					row := indexRow{Kind: kindColumn, Ref: columnRef(string(c.TableID), c.Ordinal), Texts: []string{string(c.Name), deref(c.Comment)}}
					if err := emit(row); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}

// refreshMetadataScope rebuilds a catalog's metadata terms when its tables
// changed.
func (r *Repo) refreshMetadataScope(ctx context.Context, catalogID string) error {
	id := model.UUID(catalogID)
	return r.refreshScope(ctx, metadataScopeKey(catalogID), scopeOwner{catalog: &id}, metadataBuilder(catalogID))
}

// ── docs ───────────────────────────────────────────────────────────────────

// docsBuilder builds a workspace's docs scope from its semantic docs.
func docsBuilder(workspaceID string) scopeBuilder {
	return scopeBuilder{
		fingerprint: func(tx *gorm.DB) (string, error) {
			return countAndNewest(tx.Model(&model.SemanticDoc{}).Where("workspace_id = ?", workspaceID), "updated_at")
		},
		rows: func(tx *gorm.DB, emit func(indexRow) error) error {
			var docs []model.SemanticDoc
			if err := tx.Where("workspace_id = ?", workspaceID).Find(&docs).Error; err != nil {
				return fmt.Errorf("read docs to index: %w", err)
			}
			for _, d := range docs {
				texts := append([]string{string(d.TableNameValue), string(d.Description), deref((*string)(d.ColumnName))}, []string(d.Tags)...)
				if err := emit(indexRow{Kind: kindDoc, Ref: string(d.ID), Texts: texts}); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// ── content ────────────────────────────────────────────────────────────────

// contentBuilder builds a workspace's content scope from its saved queries,
// metrics and models.
func contentBuilder(workspaceID string) scopeBuilder {
	return scopeBuilder{
		fingerprint: func(tx *gorm.DB) (string, error) {
			parts := make([]string, 0, 3)
			for _, m := range []any{&model.SavedQuery{}, &model.SemanticMetric{}, &model.SemanticModel{}} {
				part, err := countAndNewest(tx.Model(m).Where("workspace_id = ?", workspaceID), "updated_at")
				if err != nil {
					return "", err
				}
				parts = append(parts, part)
			}
			return strings.Join(parts, ";"), nil
		},
		rows: func(tx *gorm.DB, emit func(indexRow) error) error {
			var queries []model.SavedQuery
			if err := tx.Select("id", "name", "slug", "sql").Where("workspace_id = ?", workspaceID).Find(&queries).Error; err != nil {
				return fmt.Errorf("read queries to index: %w", err)
			}
			for _, q := range queries {
				sqlTerms := search.Terms(q.SQL)
				if len(sqlTerms) > maxSQLTerms {
					sqlTerms = sqlTerms[:maxSQLTerms]
				}
				texts := append([]string{string(q.Name), string(q.Slug)}, sqlTerms...)
				if err := emit(indexRow{Kind: kindSavedQuery, Ref: string(q.ID), Texts: texts}); err != nil {
					return err
				}
			}
			var metrics []model.SemanticMetric
			if err := tx.Select("id", "label", "slug", "description", "table_name", "definition").
				Where("workspace_id = ?", workspaceID).Find(&metrics).Error; err != nil {
				return fmt.Errorf("read metrics to index: %w", err)
			}
			for _, m := range metrics {
				texts := append([]string{m.Label, string(m.Slug), string(m.Description), string(m.TableNameValue)}, search.DefinitionWords(m.Definition)...)
				if err := emit(indexRow{Kind: kindMetric, Ref: string(m.ID), Texts: texts}); err != nil {
					return err
				}
			}
			var models []model.SemanticModel
			if err := tx.Select("id", "label", "slug", "description", "definition").
				Where("workspace_id = ?", workspaceID).Find(&models).Error; err != nil {
				return fmt.Errorf("read models to index: %w", err)
			}
			for _, m := range models {
				texts := append([]string{m.Label, string(m.Slug), string(m.Description)}, search.DefinitionWords(m.Definition)...)
				if err := emit(indexRow{Kind: kindModel, Ref: string(m.ID), Texts: texts}); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// ContentCandidates are the ids of the workspace's saved queries, metrics and
// models whose indexed words match p. The caller reads them with the
// visibility predicates and scores them (ADR 0023).
func (r *Repo) ContentCandidates(ctx context.Context, workspaceID db.UUID, p search.Pattern) (contentsearch.Candidates, error) {
	var out contentsearch.Candidates
	if !workspaceID.Valid {
		return out, nil
	}
	workspace := UUIDString(workspaceID)
	owner := model.UUID(workspace)
	key := scopeContent + workspace
	if err := r.refreshScope(ctx, key, scopeOwner{workspace: &owner}, contentBuilder(workspace)); err != nil {
		return out, err
	}
	var found map[string][]string
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		found, err = r.candidates(tx, []string{key}, []string{kindSavedQuery, kindMetric, kindModel}, p, contentsearch.MaxLimit)
		return err
	})
	if err != nil {
		return out, err
	}
	out.SavedQueries = PGUUIDs(found[kindSavedQuery])
	out.Metrics = PGUUIDs(found[kindMetric])
	out.Models = PGUUIDs(found[kindModel])
	return out, nil
}

// PGUUIDs converts ids to db.UUIDs.
func PGUUIDs(ids []string) []db.UUID {
	out := make([]db.UUID, len(ids))
	for i, id := range ids {
		out[i] = PGUUID(id)
	}
	return out
}

// ── helpers ────────────────────────────────────────────────────────────────

// stamp renders a timestamp for a fingerprint; the zero time is empty.
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return strconv.FormatInt(t.UTC().UnixMicro(), 10)
}

// newest is the greatest value of a timestamp column among the rows q selects,
// the zero time when there are none.
func newest(q *gorm.DB, column string) (time.Time, error) {
	var times []time.Time
	err := q.Order(clause.OrderByColumn{Column: clause.Column{Name: column}, Desc: true}).
		Limit(1).Pluck(column, &times).Error
	if err != nil || len(times) == 0 {
		return time.Time{}, err
	}
	return times[0], nil
}

// countAndNewest fingerprints the rows q selects: how many and how recently
// one changed.
func countAndNewest(q *gorm.DB, column string) (string, error) {
	var n int64
	if err := q.Session(&gorm.Session{}).Count(&n).Error; err != nil {
		return "", fmt.Errorf("count rows to fingerprint: %w", err)
	}
	latest, err := newest(q.Session(&gorm.Session{}), column)
	if err != nil {
		return "", fmt.Errorf("fingerprint rows: %w", err)
	}
	return fmt.Sprintf("%d|%s", n, stamp(latest)), nil
}

// deref is the string p points to, empty for nil.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// inChunks calls fn with ids in slices small enough for an IN list on every
// backend.
func inChunks(ids []string, fn func([]string) error) error {
	return inChunksOf(ids, inChunkSize, fn)
}

// inChunksOf calls fn with ids in slices of at most size.
func inChunksOf(ids []string, size int, fn func([]string) error) error {
	for start := 0; start < len(ids); start += size {
		if err := fn(ids[start:min(start+size, len(ids))]); err != nil {
			return err
		}
	}
	return nil
}

// inChunkSize bounds one IN list.
const inChunkSize = 1000

// uniqueStrings drops repeated values, keeping the first of each.
func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}
