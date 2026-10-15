// Package metadata keeps the physical metadata snapshots (#15): namespaces,
// tables and columns per logical catalog, read from the catalog's own source
// (catalog manager, database driver) or, when it has none Baleia can read,
// introspected through an engine. Snapshots are keyed by catalog, never by
// workspace; grants scope them at read time (#18).
package metadata

import (
	"context"
	"errors"
)

// Source paths, recorded on every metadata_sync_runs row.
const (
	PathProvider = "provider"
	PathEngine   = "engine"
)

// Table kinds stored in metadata_tables.kind.
const (
	KindTable            = "table"
	KindView             = "view"
	KindMaterializedView = "materialized_view"
)

// ErrSyncInProgress means another sync of the same catalog holds its lock.
var ErrSyncInProgress = errors.New("a metadata sync of this catalog is already running")

// TableSnapshot is one table as a source reports it in a pass.
type TableSnapshot struct {
	Namespace   []string
	Name        string
	Kind        string
	Fingerprint string
	// Unchanged means the source knows the table did not change since the
	// stored fingerprint (Known) and loaded nothing: Columns, Properties and
	// Comment are empty and the stored ones stay.
	Unchanged  bool
	Columns    []ColumnSnapshot
	Properties map[string]string
	Comment    string
}

// ColumnSnapshot is one column, in the source's own type system.
type ColumnSnapshot struct {
	Ordinal    int
	Name       string
	NativeType string
	Nullable   bool
	Comment    string
}

// Known is what earlier passes stored about a table that is still present.
type Known struct {
	Fingerprint string
	// ETag is the source's version tag of the stored table (Iceberg REST),
	// "" when it has none.
	ETag string
}

// Visitor receives what a source walks.
type Visitor interface {
	Namespace(ns []string) error
	Table(t TableSnapshot) error
}

// Source walks one catalog. Implementations stream: they never load the
// whole catalog in memory.
type Source interface {
	// TypeSystem names the type system of the source's column types:
	// iceberg, postgresql, mysql, tsql, oracle, sqlite, clickhouse, trino, …
	TypeSystem() string
	// Walk visits every namespace and table of the catalog. known returns
	// what earlier passes stored for a table, so unchanged tables need not
	// be loaded.
	Walk(ctx context.Context, known func(ns []string, name string) (Known, bool), v Visitor) error
	Close() error
}

// RunStats summarizes one pass.
type RunStats struct {
	Path          string
	TablesSeen    int
	TablesChanged int
	TablesRemoved int
}
