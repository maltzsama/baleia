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
	// Unsupported is set when the provider lists the table but cannot read it.
	Unsupported Unsupported
}

// Why a listed table cannot be read. The codes are stable: they are stored with
// the table and become the keys of the message the UI shows.
const (
	// UnsupportedKind: the provider reports a kind of table its protocol cannot
	// serve; Detail is that kind (Unity: TABLE_DB_STORAGE).
	UnsupportedKind = "table_kind"
	// UnsupportedRefused: the provider's protocol refused the table when it was
	// loaded, without saying why.
	UnsupportedRefused = "refused"
	// UnsupportedRegistration: the table is registered in a way the engine's
	// connector does not recognize; Detail is the registration found (Glue: a
	// Delta table marked only with table_type=DELTA, which Trino's Delta
	// connector does not read).
	UnsupportedRegistration = "registration"
)

// Unsupported says why a table that a provider lists cannot be read. The zero
// value means the table is readable.
type Unsupported struct {
	Code   string
	Detail string
}

// Is reports whether the table is unsupported.
func (u Unsupported) Is() bool { return u.Code != "" }

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

// ForeignKey is one column of a table referencing one column of another.
// Names are as the source reports them; the referenced table may live in
// another namespace.
type ForeignKey struct {
	Column       string
	RefNamespace Namespace
	RefTable     string
	RefColumn    string
}

// TableMeta is a table's metadata.
type TableMeta struct {
	Ref         TableRef
	Columns     []Column
	ForeignKeys []ForeignKey
	Comment     string
	// Format is informational: "iceberg", "heap", "innodb", …
	Format string
	// Properties carries format-specific details (location, table engine, …).
	Properties map[string]string
	// TableProperties are the table's own properties as its catalog stores
	// them (Iceberg's history.expire.*, write.*, …); nil when not read.
	TableProperties map[string]string
	// FormatVersion is the Iceberg format version; 0 when unknown.
	FormatVersion int
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
