// Package browse reads the stored metadata of the catalogs granted to a
// workspace (#18): catalogs, namespaces, a page of tables, one table with its
// columns and docs, a search, and a catalog's sync runs. The caller checks
// the user may read the workspace, or the catalog for its sync runs.
package browse

import (
	"context"
	"encoding/base64"
	"errors"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Catalogs reads the granted catalogs and their namespaces; *db.Queries
// implements it.
type Catalogs interface {
	ListMetadataCatalogs(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListMetadataCatalogsRow, error)
	GetGrantedMetadataCatalog(ctx context.Context, arg db.GetGrantedMetadataCatalogParams) (db.GetGrantedMetadataCatalogRow, error)
	ListChildNamespaces(ctx context.Context, arg db.ListChildNamespacesParams) ([][]string, error)
	ListMetadataSyncRuns(ctx context.Context, arg db.ListMetadataSyncRunsParams) ([]db.ListMetadataSyncRunsRow, error)
}

// Tables reads tables, their columns and docs; *db.Queries implements it.
type Tables interface {
	ListMetadataTablesPage(ctx context.Context, arg db.ListMetadataTablesPageParams) ([]db.ListMetadataTablesPageRow, error)
	GetGrantedMetadataTable(ctx context.Context, arg db.GetGrantedMetadataTableParams) (db.GetGrantedMetadataTableRow, error)
	GetCatalogBindingForDisplay(ctx context.Context, arg db.GetCatalogBindingForDisplayParams) (db.GetCatalogBindingForDisplayRow, error)
	ListMetadataColumns(ctx context.Context, tableID pgtype.UUID) ([]db.ListMetadataColumnsRow, error)
	ListSemanticDocsForTable(ctx context.Context, arg db.ListSemanticDocsForTableParams) ([]db.SemanticDoc, error)
	SearchMetadata(ctx context.Context, arg db.SearchMetadataParams) ([]db.SearchMetadataRow, error)
}

// Store is Catalogs and Tables.
type Store interface {
	Catalogs
	Tables
}

// Limits on pages and searches.
const (
	DefaultTablesPage = 100
	MaxTablesPage     = 500
	DefaultSearch     = 50
	MaxSearch         = 200
	MinSearchQuery    = 2
	MaxSearchQuery    = 200
	DefaultSyncRuns   = 20
	MaxSyncRuns       = 100
)

var (
	// ErrCatalogNotFound: the catalog does not exist or is not granted to
	// the workspace.
	ErrCatalogNotFound = errors.New("browse: catalog not found")
	// ErrTableNotFound: the table does not exist or its catalog is not
	// granted to the workspace.
	ErrTableNotFound = errors.New("browse: table not found")
	// ErrBindingNotFound: the requested binding does not serve the table's
	// catalog.
	ErrBindingNotFound = errors.New("browse: binding not found")
)

// InvalidError says which parameter was refused.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// Service reads metadata.
type Service struct{ store Store }

// New builds the service.
func New(store Store) *Service { return &Service{store: store} }

// Catalogs lists the catalogs granted to ws with their bindings.
func (s *Service) Catalogs(ctx context.Context, ws pgtype.UUID) ([]db.ListMetadataCatalogsRow, error) {
	return s.store.ListMetadataCatalogs(ctx, ws)
}

// Catalog returns a catalog granted to ws.
func (s *Service) Catalog(ctx context.Context, ws, id pgtype.UUID) (db.GetGrantedMetadataCatalogRow, error) {
	c, err := s.store.GetGrantedMetadataCatalog(ctx, db.GetGrantedMetadataCatalogParams{WorkspaceID: ws, CatalogID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrCatalogNotFound
	}
	return c, err
}

// Namespaces lists the namespaces directly under parent in a granted catalog.
func (s *Service) Namespaces(ctx context.Context, ws, catalogID pgtype.UUID, parent []string) (db.GetGrantedMetadataCatalogRow, [][]string, error) {
	cat, err := s.Catalog(ctx, ws, catalogID)
	if err != nil {
		return cat, nil, err
	}
	if parent == nil {
		parent = []string{}
	}
	ns, err := s.store.ListChildNamespaces(ctx, db.ListChildNamespacesParams{CatalogID: cat.ID, Parent: parent})
	return cat, ns, err
}

// TablesQuery asks for a page of a namespace's tables. Cursor is the
// NextCursor of the previous page; a nil Limit is DefaultTablesPage.
type TablesQuery struct {
	Namespace []string
	Cursor    string
	Limit     *int
}

// TablesPage is a page of tables, ordered by name.
type TablesPage struct {
	Catalog db.GetGrantedMetadataCatalogRow
	Tables  []db.ListMetadataTablesPageRow
	// NextCursor is set when more tables follow.
	NextCursor *string
}

// Tables returns a page of tables of a granted catalog.
func (s *Service) Tables(ctx context.Context, ws, catalogID pgtype.UUID, q TablesQuery) (TablesPage, error) {
	cat, err := s.Catalog(ctx, ws, catalogID)
	if err != nil {
		return TablesPage{}, err
	}
	limit := DefaultTablesPage
	if q.Limit != nil {
		if *q.Limit < 1 || *q.Limit > MaxTablesPage {
			return TablesPage{}, &InvalidError{Msg: "limit must be between 1 and 500"}
		}
		limit = *q.Limit
	}
	after := ""
	if q.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		if err != nil || !utf8.Valid(raw) {
			return TablesPage{}, &InvalidError{Msg: "invalid cursor"}
		}
		after = string(raw)
	}
	ns := q.Namespace
	if ns == nil {
		ns = []string{}
	}
	rows, err := s.store.ListMetadataTablesPage(ctx, db.ListMetadataTablesPageParams{CatalogID: cat.ID, Namespace: ns, After: after, Lim: int32(limit + 1)})
	if err != nil {
		return TablesPage{}, err
	}
	page := TablesPage{Catalog: cat, Tables: rows}
	if len(rows) > limit {
		page.Tables = rows[:limit]
		next := base64.RawURLEncoding.EncodeToString([]byte(rows[limit-1].Name))
		page.NextCursor = &next
	}
	return page, nil
}

// Table is one table with its columns, its docs and, when one serves its
// catalog, the binding it is shown through.
type Table struct {
	Table   db.GetGrantedMetadataTableRow
	Binding *db.GetCatalogBindingForDisplayRow
	Columns []db.ListMetadataColumnsRow
	Docs    []db.SemanticDoc
}

// Table returns a table of a catalog granted to ws. A valid bindingID shows
// it through that binding, which must serve the catalog; otherwise through
// the catalog's default binding, if any.
func (s *Service) Table(ctx context.Context, ws, tableID, bindingID pgtype.UUID) (Table, error) {
	t, err := s.store.GetGrantedMetadataTable(ctx, db.GetGrantedMetadataTableParams{WorkspaceID: ws, TableID: tableID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Table{}, ErrTableNotFound
	}
	if err != nil {
		return Table{}, err
	}
	out := Table{Table: t}
	b, err := s.store.GetCatalogBindingForDisplay(ctx, db.GetCatalogBindingForDisplayParams{CatalogID: t.CatalogID, BindingID: bindingID})
	switch {
	case err == nil:
		out.Binding = &b
	case !errors.Is(err, pgx.ErrNoRows):
		return Table{}, err
	case bindingID.Valid:
		return Table{}, ErrBindingNotFound
	}
	if out.Columns, err = s.store.ListMetadataColumns(ctx, t.ID); err != nil {
		return Table{}, err
	}
	out.Docs, err = s.store.ListSemanticDocsForTable(ctx, db.ListSemanticDocsForTableParams{
		WorkspaceID: ws, CatalogID: t.CatalogID, Namespace: t.Namespace, TableName: t.Name, ColumnName: nil,
	})
	if err != nil {
		return Table{}, err
	}
	return out, nil
}

// Search kinds.
const (
	KindTable  = "table"
	KindColumn = "column"
	KindAll    = "all"
)

// SearchQuery is a search of tables and columns. An empty Kind is KindAll, a
// nil Limit DefaultSearch, and a valid CatalogID narrows to one catalog.
type SearchQuery struct {
	Text      string
	Kind      string
	Limit     *int
	CatalogID pgtype.UUID
}

// Search finds tables and columns of the catalogs granted to ws.
func (s *Service) Search(ctx context.Context, ws pgtype.UUID, q SearchQuery) ([]db.SearchMetadataRow, error) {
	if n := utf8.RuneCountInString(q.Text); n < MinSearchQuery || n > MaxSearchQuery {
		return nil, &InvalidError{Msg: "q must be 2 to 200 characters"}
	}
	kind := KindAll
	switch q.Kind {
	case "":
	case KindTable, KindColumn, KindAll:
		kind = q.Kind
	default:
		return nil, &InvalidError{Msg: "kind must be table, column or all"}
	}
	limit := DefaultSearch
	if q.Limit != nil {
		if *q.Limit < 1 || *q.Limit > MaxSearch {
			return nil, &InvalidError{Msg: "limit must be between 1 and 200"}
		}
		limit = *q.Limit
	}
	return s.store.SearchMetadata(ctx, db.SearchMetadataParams{WorkspaceID: ws, CatalogID: q.CatalogID, Q: q.Text, Kind: kind, Lim: int32(limit)})
}

// SyncRuns lists a catalog's latest metadata passes. A limit outside
// 1..MaxSyncRuns is DefaultSyncRuns.
func (s *Service) SyncRuns(ctx context.Context, catalogID pgtype.UUID, limit int) ([]db.ListMetadataSyncRunsRow, error) {
	if limit < 1 || limit > MaxSyncRuns {
		limit = DefaultSyncRuns
	}
	return s.store.ListMetadataSyncRuns(ctx, db.ListMetadataSyncRunsParams{CatalogID: catalogID, Limit: int32(limit)})
}
