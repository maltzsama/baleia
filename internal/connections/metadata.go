package connections

import "context"

// CatalogRef is a top-level container: an Iceberg warehouse or a database.
type CatalogRef struct {
	Name string
	Kind string
}

// Namespace is multi-level for Iceberg and [schema] for databases.
type Namespace []string

// TableRef addresses one table or view.
type TableRef struct {
	Catalog   string
	Namespace Namespace
	Name      string
	Type      string // TABLE | VIEW | MATERIALIZED_VIEW
}

// Column is one column of a table, in the source's native type system.
type Column struct {
	Name       string
	NativeType string
	Nullable   bool
	Comment    string
	Position   int
}

// Snapshot is one Iceberg snapshot. Other formats have none.
type Snapshot struct {
	ID          int64
	ParentID    *int64
	TimestampMs int64
	Operation   string
	Summary     map[string]string
}

// TableMeta is a table's metadata.
type TableMeta struct {
	Ref     TableRef
	Columns []Column
	Comment string
	// Format is informational: "iceberg", "heap", "innodb", …
	Format string
	// Properties carries format-specific details (location, table engine, …).
	Properties map[string]string
	// Snapshots are oldest first; empty for non-Iceberg tables.
	Snapshots []Snapshot
}

// MetadataReader reads metadata directly from a source. Readers run read-only
// statements with a timeout and must be closed.
type MetadataReader interface {
	ListCatalogs(ctx context.Context) ([]CatalogRef, error)
	ListNamespaces(ctx context.Context, catalog string, parent Namespace) ([]Namespace, error)
	// ListTables returns one page and the token of the next one ("" when done).
	ListTables(ctx context.Context, catalog string, ns Namespace, pageToken string) ([]TableRef, string, error)
	LoadTable(ctx context.Context, t TableRef) (*TableMeta, error)
	Close() error
}
