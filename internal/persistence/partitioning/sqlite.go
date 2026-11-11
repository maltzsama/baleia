package partitioning

import (
	"context"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"
)

// sqliteManager emulates range partitioning on SQLite, which has no
// native partitioning (ADR 0023). The table stays a single plain table;
// "partitions" are recorded in a per-table sidecar registry (<table>_partitions)
// and DropPartition is a bounded DELETE rather than a metadata drop. The
// emulation is explicit — Capabilities().Emulated is true and NativeRange is
// false — so the retention layer never mistakes it for native partitioning.
type sqliteManager struct {
	db *gorm.DB
}

func (m *sqliteManager) Capabilities() Capabilities {
	return capabilitiesFor("sqlite")
}

// sidecarTable names the registry that records a table's emulated partitions
// and its partition column. spec.Table is a validated identifier, so the
// concatenation is a safe identifier too.
func sidecarTable(table string) string { return table + "_partitions" }

// Provision creates the plain table if absent, the sidecar registry if absent,
// and records the partitions covering [from, until). Idempotent.
func (m *sqliteManager) Provision(ctx context.Context, model any, spec Spec, from, until time.Time) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := spec.Validate(); err != nil {
			return err
		}
		if !tx.Migrator().HasTable(spec.Table) {
			if err := tx.Migrator().CreateTable(model); err != nil {
				return fmt.Errorf("partitioning: create table %s: %w", spec.Table, err)
			}
			// A model whose table name disagrees with the spec would silently
			// create the wrong table; fail loudly instead.
			if !tx.Migrator().HasTable(spec.Table) {
				return fmt.Errorf("partitioning: model does not create table %q", spec.Table)
			}
		}
		sidecar := sidecarTable(spec.Table)
		if !tx.Migrator().HasTable(sidecar) {
			if err := tx.Exec(fmt.Sprintf(
				`CREATE TABLE "%s" ("name" TEXT PRIMARY KEY, "column_name" TEXT NOT NULL)`, sidecar,
			)).Error; err != nil {
				return fmt.Errorf("partitioning: create registry %s: %w", sidecar, err)
			}
		}
		for _, p := range RangePartitions(spec.Table, spec.Interval, from, until) {
			if err := tx.Exec(fmt.Sprintf(
				`INSERT OR REPLACE INTO "%s" ("name", "column_name") VALUES (?, ?)`, sidecar,
			), p.Name, spec.Column).Error; err != nil {
				return fmt.Errorf("partitioning: record partition %s: %w", p.Name, err)
			}
		}
		return nil
	})
}

// ListPartitions returns the recorded partitions of table, oldest first. A
// table that was never provisioned has none.
func (m *sqliteManager) ListPartitions(ctx context.Context, table string) ([]Partition, error) {
	return listRegistry(m.db.WithContext(ctx), table)
}

func listRegistry(db *gorm.DB, table string) ([]Partition, error) {
	if !identifier.MatchString(table) {
		return nil, fmt.Errorf("partitioning: invalid table name %q", table)
	}
	sidecar := sidecarTable(table)
	if !db.Migrator().HasTable(sidecar) {
		return []Partition{}, nil
	}
	var names []string
	if err := db.Raw(fmt.Sprintf(`SELECT "name" FROM "%s"`, sidecar)).Scan(&names).Error; err != nil {
		return nil, fmt.Errorf("partitioning: list registry %s: %w", sidecar, err)
	}
	var out []Partition
	for _, name := range names {
		if p, ok := partitionFromName(table, name); ok {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].From.Before(out[j].From) })
	return out, nil
}

// DropPartition removes the rows of the partitions covering [from, until) and
// forgets those partitions in the registry. It is a bounded DELETE, not a metadata drop: the retention
// layer reads Capabilities().NativeRange == false and treats it accordingly.
func (m *sqliteManager) DropPartition(ctx context.Context, table string, from, until time.Time) error {
	if !identifier.MatchString(table) {
		return fmt.Errorf("partitioning: invalid table name %q", table)
	}
	sidecar := sidecarTable(table)
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if !tx.Migrator().HasTable(sidecar) {
			return nil
		}
		var meta struct{ ColumnName string }
		if err := tx.Raw(fmt.Sprintf(`SELECT "column_name" AS column_name FROM "%s" LIMIT 1`, sidecar)).Scan(&meta).Error; err != nil {
			return fmt.Errorf("partitioning: read registry %s: %w", sidecar, err)
		}
		if meta.ColumnName == "" {
			return nil // never provisioned: nothing to drop
		}
		existing, err := listRegistry(tx, table)
		if err != nil {
			return err
		}
		for _, p := range partitionsIn(existing, from, until) {
			if err := tx.Exec(fmt.Sprintf(`DELETE FROM "%s" WHERE "%s" >= ? AND "%s" < ?`, table, meta.ColumnName, meta.ColumnName),
				p.From, p.Until).Error; err != nil {
				return fmt.Errorf("partitioning: delete rows from %s: %w", table, err)
			}
			if err := tx.Exec(fmt.Sprintf(`DELETE FROM "%s" WHERE "name" = ?`, sidecar), p.Name).Error; err != nil {
				return fmt.Errorf("partitioning: forget partition %s: %w", p.Name, err)
			}
		}
		return nil
	})
}
