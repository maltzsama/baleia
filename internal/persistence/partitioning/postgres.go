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
func (m *postgresManager) Provision(ctx context.Context, model any, spec Spec, from, until time.Time) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := spec.Validate(); err != nil {
			return err
		}
		if !tx.Migrator().HasTable(spec.Table) {
			if err := createPartitionedTable(tx, model, spec); err != nil {
				return err
			}
		}
		return ensurePartitions(tx, spec, from, until)
	})
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
