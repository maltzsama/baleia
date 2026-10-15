package connections

import (
	"context"
	"slices"
	"time"
)

// CatalogWalker is implemented by database readers that read every table of
// a catalog with its columns in ONE query (the metadata sync, #15), instead
// of one LoadTable per table. visit gets each table once, ordered by
// namespace and name, its columns by position; a table without columns has
// none. The walk streams: it never holds the whole catalog in memory.
type CatalogWalker interface {
	WalkCatalog(ctx context.Context, catalog string, visit func(TableMeta) error) error
}

// ConditionalLoader is implemented by readers that can tell an unchanged
// table without loading it (Iceberg REST: ETag and If-None-Match).
type ConditionalLoader interface {
	// LoadTableIfChanged loads t unless its version still matches etag (from
	// an earlier call): then it returns a nil TableMeta. The returned etag
	// identifies the version loaded ("" when the server sends none).
	LoadTableIfChanged(ctx context.Context, t TableRef, etag string) (meta *TableMeta, newETag string, err error)
}

// WalkTimeout bounds one catalog walk query: it reads a whole catalog, so it
// gets longer than a single statement.
const WalkTimeout = 5 * time.Minute

// CatalogRow is one column of one table as a catalog walk reads it. Rows
// arrive ordered by namespace, table and column position. A table without
// columns comes as one row whose Column.Name is empty.
type CatalogRow struct {
	Namespace  Namespace
	Table      string
	Type       string // TABLE | VIEW | MATERIALIZED_VIEW
	Comment    string
	Format     string
	Properties map[string]string
	Column     Column
}

// TableGrouper turns the ordered rows of a catalog walk into one TableMeta per
// table, handing each to visit as soon as its last column is read.
type TableGrouper struct {
	catalog string
	visit   func(TableMeta) error
	cur     *TableMeta
}

// NewTableGrouper groups the rows of catalog.
func NewTableGrouper(catalog string, visit func(TableMeta) error) *TableGrouper {
	return &TableGrouper{catalog: catalog, visit: visit}
}

// Add adds one row; it hands over the previous table when r starts another.
func (g *TableGrouper) Add(r CatalogRow) error {
	if g.cur != nil && (g.cur.Ref.Name != r.Table || !slices.Equal(g.cur.Ref.Namespace, r.Namespace)) {
		if err := g.Flush(); err != nil {
			return err
		}
	}
	if g.cur == nil {
		props := r.Properties
		if props == nil {
			props = map[string]string{}
		}
		g.cur = &TableMeta{
			Ref:        TableRef{Catalog: g.catalog, Namespace: slices.Clone(r.Namespace), Name: r.Table, Type: r.Type},
			Comment:    r.Comment,
			Format:     r.Format,
			Properties: props,
		}
	}
	if r.Column.Name != "" {
		g.cur.Columns = append(g.cur.Columns, r.Column)
	}
	return nil
}

// Flush hands over the table being read, if any. Call it after the last row.
func (g *TableGrouper) Flush() error {
	if g.cur == nil {
		return nil
	}
	t := *g.cur
	g.cur = nil
	return g.visit(t)
}

// CatalogWalkerProvider is implemented by providers whose metadata readers
// are CatalogWalkers, so the metadata sync can choose them without opening a
// reader.
type CatalogWalkerProvider interface {
	WalksCatalogs() bool
}
