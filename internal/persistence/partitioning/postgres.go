package partitioning

import (
	"context"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"
)

// postgresManager is the PostgreSQL adapter. All dialect-specific partition
// DDL lives here: this is the carve-out from ADR 0020 recorded by ADR 0023.
type postgresManager struct {
	db *gorm.DB
}

// Provision creates the partitioned table if needed and creates the monthly
// partitions covering [from, until). Idempotent. The model's table name must
// equal spec.Table.
//
// PostgreSQL cannot partition a populated table in place, so a plain table is
// set aside as LegacyTable(spec.Table) (its indexes renamed, because index
// names are schema-global) and the partitioned table is created from the model,
// which carries the partition-key primary key. Copying the rows across is the
// caller's job, through the ORM, followed by SyncSequence and dropping the
// legacy table: only the DDL is the adapter's (ADR 0024).
func (m *postgresManager) Provision(ctx context.Context, model any, spec Spec, from, until time.Time) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := spec.Validate(); err != nil {
			return err
		}
		if tx.Migrator().HasTable(spec.Table) {
			plain, err := isPlainTable(tx, spec.Table)
			if err != nil {
				return err
			}
			if plain {
				if err := setAsideLegacy(tx, model, spec.Table); err != nil {
					return err
				}
			}
		}
		if !tx.Migrator().HasTable(spec.Table) {
			if err := createPartitionedTable(tx, model, spec); err != nil {
				return err
			}
		}
		return ensurePartitions(tx, spec, from, until)
	})
}

// SyncSequence moves the sequence behind table.column so the next generated
// value is next+1. Copying rows with explicit ids does not advance it, and the
// id must stay a single global sequence (ADR 0024). This is the one statement
// of the conversion the ORM cannot express.
func (m *postgresManager) SyncSequence(ctx context.Context, table, column string, next int64) error {
	if !identifier.MatchString(table) || !identifier.MatchString(column) {
		return fmt.Errorf("partitioning: invalid sequence target %q.%q", table, column)
	}
	if err := m.db.WithContext(ctx).
		Exec("SELECT setval(pg_get_serial_sequence(?, ?), ?)", table, column, next).Error; err != nil {
		return fmt.Errorf("partitioning: sync sequence of %s.%s: %w", table, column, err)
	}
	return nil
}

// isPlainTable reports whether table exists as a regular (non-partitioned)
// table, by catalog introspection through the query builder.
func isPlainTable(db *gorm.DB, table string) (bool, error) {
	var n int64
	err := db.Table("pg_class AS c").
		Joins("JOIN pg_namespace AS n ON n.oid = c.relnamespace").
		Where("c.relname = ? AND c.relkind = ? AND n.nspname = current_schema()", table, "r").
		Count(&n).Error
	if err != nil {
		return false, fmt.Errorf("partitioning: inspect %s: %w", table, err)
	}
	return n > 0, nil
}

// setAsideLegacy renames a plain table, and every index it owns, out of the way
// of the partitioned table that replaces it. The primary key's index carries the
// constraint with it.
func setAsideLegacy(db *gorm.DB, model any, table string) error {
	legacy := LegacyTable(table)
	if db.Migrator().HasTable(legacy) {
		return fmt.Errorf("partitioning: %s is plain but %s already exists; finish or remove the interrupted conversion", table, legacy)
	}
	indexes, err := db.Migrator().GetIndexes(table)
	if err != nil {
		return fmt.Errorf("partitioning: list indexes of %s: %w", table, err)
	}
	if err := db.Migrator().RenameTable(table, legacy); err != nil {
		return fmt.Errorf("partitioning: set aside %s: %w", table, err)
	}
	for _, idx := range indexes {
		renamed := legacyIndexName(idx.Name())
		if len(renamed) > maxIdentifierLen {
			return fmt.Errorf("partitioning: index name %q is too long to set aside", idx.Name())
		}
		if err := db.Migrator().RenameIndex(model, idx.Name(), renamed); err != nil {
			return fmt.Errorf("partitioning: rename index %s: %w", idx.Name(), err)
		}
	}
	return nil
}

func (m *postgresManager) ListPartitions(ctx context.Context, table string) ([]Partition, error) {
	return listPartitions(m.db.WithContext(ctx), table)
}

func (m *postgresManager) DropPartition(ctx context.Context, table string, from, until time.Time) error {
	return dropPartition(m.db.WithContext(ctx), table, from, until)
}

func (m *postgresManager) Capabilities() Capabilities {
	return capabilitiesFor("postgres")
}

func createPartitionedTable(db *gorm.DB, model any, spec Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	if db.Migrator().HasTable(spec.Table) {
		return nil
	}
	opts := fmt.Sprintf("PARTITION BY RANGE (%s)", spec.Column)
	if err := db.Set("gorm:table_options", opts).Migrator().CreateTable(model); err != nil {
		return fmt.Errorf("partitioning: create parent %s: %w", spec.Table, err)
	}
	// A model whose table name disagrees with the spec would silently create
	// the wrong table; fail loudly instead.
	if !db.Migrator().HasTable(spec.Table) {
		return fmt.Errorf("partitioning: model does not create table %q", spec.Table)
	}
	return nil
}

func ensurePartitions(db *gorm.DB, spec Spec, from, until time.Time) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	for _, p := range RangeMonths(spec.Table, from, until) {
		stmt := fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS "%s" PARTITION OF "%s" FOR VALUES FROM (%s) TO (%s)`,
			p.Name, spec.Table, quoteTime(p.From), quoteTime(p.Until),
		)
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("partitioning: create partition %s: %w", p.Name, err)
		}
	}
	return nil
}

func listPartitions(db *gorm.DB, table string) ([]Partition, error) {
	if !identifier.MatchString(table) {
		return nil, fmt.Errorf("partitioning: invalid table name %q", table)
	}
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return nil, fmt.Errorf("partitioning: list tables: %w", err)
	}
	prefix := table + "_"
	var out []Partition
	for _, name := range tables {
		if len(name) != len(prefix)+6 || name[:len(prefix)] != prefix {
			continue
		}
		month, err := time.Parse("200601", name[len(prefix):])
		if err != nil {
			continue
		}
		out = append(out, Partition{Name: name, From: month, Until: month.AddDate(0, 1, 0)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].From.Before(out[j].From) })
	return out, nil
}

func dropPartition(db *gorm.DB, table string, from, until time.Time) error {
	if !identifier.MatchString(table) {
		return fmt.Errorf("partitioning: invalid table name %q", table)
	}
	for _, p := range RangeMonths(table, from, until) {
		if err := db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS "%s"`, p.Name)).Error; err != nil {
			return fmt.Errorf("partitioning: drop partition %s: %w", p.Name, err)
		}
	}
	return nil
}

func quoteTime(t time.Time) string {
	return "'" + t.UTC().Format("2006-01-02") + "'"
}
