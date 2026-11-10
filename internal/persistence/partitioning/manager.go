package partitioning

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

var (
	// ErrUnsupportedPartitioning is returned for a backend with no native range
	// partitioning and no adapter. SQLite is not this: it is emulated.
	ErrUnsupportedPartitioning = errors.New("partitioning is unsupported for this backend")
	// ErrNotImplemented is returned for a backend that supports native
	// partitioning but whose adapter has not shipped yet.
	ErrNotImplemented = errors.New("partitioning adapter is not implemented for this backend")
)

// Capabilities describes what a backend can do, so the retention layer never
// assumes one physical model (ADR 0023).
type Capabilities struct {
	// NativeRange: the backend partitions natively by month, and DropPartition
	// reclaims space in one metadata operation.
	NativeRange bool
	// ForeignKeysOnPartitioned: a partitioned table may still take part in
	// foreign keys. MySQL/MariaDB InnoDB forbid it.
	ForeignKeysOnPartitioned bool
	// Emulated: the backend has no native partitioning and the adapter emulates
	// the contract on a plain table (a sidecar registry plus a bounded DELETE
	// for DropPartition). Mutually exclusive with NativeRange.
	Emulated bool
	// Implemented: this build ships the adapter.
	Implemented bool
}

// CapabilitiesFor reports the backend's capabilities without constructing an
// adapter. Nil yields the zero value.
func CapabilitiesFor(db *gorm.DB) Capabilities {
	if db == nil {
		return Capabilities{}
	}
	return capabilitiesFor(db.Dialector.Name())
}

// Manager owns the physical partitions of a table. It executes operations when
// authorized; it knows nothing about cold storage, manifests or offload
// cursors, which belong to the retention worker. The contract is deliberately
// free of any one engine's DDL shape: Provision creates-or-extends in one call
// because several engines require partitions at creation time.
type Manager interface {
	// Provision creates the partitioned table if needed, converts an existing
	// plain table in place, and creates the partitions covering [from, until).
	// Idempotent. The model's table name must equal spec.Table.
	Provision(ctx context.Context, model any, spec Spec, from, until time.Time) error
	// ListPartitions returns the existing partitions of table, oldest first.
	ListPartitions(ctx context.Context, table string) ([]Partition, error)
	// DropPartition drops the partitions covering [from, until).
	DropPartition(ctx context.Context, table string, from, until time.Time) error
	// Capabilities reports what this adapter can do.
	Capabilities() Capabilities
}

// New resolves the adapter from the GORM dialector. The registry covers every
// backend ADR 0023 names; SQLite has no native partitioning and is unsupported,
// and the engines whose adapter has not shipped return ErrNotImplemented rather
// than pretending parity.
func New(db *gorm.DB) (Manager, error) {
	if db == nil {
		return nil, errors.New("partitioning: nil database")
	}
	name := db.Dialector.Name()
	switch name {
	case "postgres", "gaussdb":
		return &postgresManager{db: db}, nil
	case "mysql", "tidb", "sqlserver", "oracle":
		return nil, fmt.Errorf("%w: %q", ErrNotImplemented, name)
	case "sqlite":
		return &sqliteManager{db: db}, nil
	default:
		return nil, fmt.Errorf("%w: %q has no native range partitioning", ErrUnsupportedPartitioning, name)
	}
}

// capabilitiesFor is the single source of truth for backend capabilities.
func capabilitiesFor(dialector string) Capabilities {
	switch dialector {
	case "postgres", "gaussdb":
		// GaussDB speaks the PostgreSQL protocol and supports PARTITION BY RANGE.
		return Capabilities{NativeRange: true, ForeignKeysOnPartitioned: true, Implemented: true}
	case "mysql", "tidb":
		// MySQL/MariaDB InnoDB and TiDB do not allow foreign keys on
		// partitioned tables (TiDB support is version-dependent).
		return Capabilities{NativeRange: true, ForeignKeysOnPartitioned: false}
	case "sqlserver", "oracle":
		// Both support native partitioning but with a different model
		// (partition function/scheme, interval partitioning).
		return Capabilities{NativeRange: true, ForeignKeysOnPartitioned: true}
	case "sqlite":
		// No native partitioning: emulated on a plain table with a sidecar
		// registry and a bounded DELETE for DropPartition.
		return Capabilities{NativeRange: false, ForeignKeysOnPartitioned: true, Emulated: true, Implemented: true}
	default:
		return Capabilities{}
	}
}
