package partitioning

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// mysqlManager is the MySQL/MariaDB adapter: native RANGE COLUMNS monthly
// partitioning. InnoDB forbids foreign keys on partitioned tables, so
// Capabilities().ForeignKeysOnPartitioned is false (ADR 0023).
//
// MySQL requires the partitions to be defined when the table becomes
// partitioned, so Provision partitions a plain table in a single ALTER with a
// MAXVALUE catch-all and then splits later months off the catch-all with
// REORGANIZE PARTITION. MySQL DDL auto-commits, so Provision does not wrap the
// statements in a transaction (unlike the PostgreSQL adapter, where DDL is
// transactional).
type mysqlManager struct {
	db *gorm.DB
}

func (m *mysqlManager) Capabilities() Capabilities {
	return capabilitiesFor("mysql")
}

// pmax is the suffix of the MAXVALUE catch-all partition. It never matches the
// <table>_YYYYMM shape, so ListPartitions ignores it.
const pmax = "pmax"

// Provision creates the table if absent (plain, through GORM), partitions it
// in one ALTER if it is still plain, and then ensures the partitions covering
// [from, until) exist, splitting months off the catch-all as needed.
func (m *mysqlManager) Provision(ctx context.Context, model any, spec Spec, from, until time.Time) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	if !m.db.Migrator().HasTable(spec.Table) {
		if err := m.db.Migrator().CreateTable(model); err != nil {
			return fmt.Errorf("partitioning: create table %s: %w", spec.Table, err)
		}
		if !m.db.Migrator().HasTable(spec.Table) {
			return fmt.Errorf("partitioning: model does not create table %q", spec.Table)
		}
	}
	existing, err := m.existingPartitions(ctx, spec.Table)
	if err != nil {
		return err
	}
	months := RangeMonths(spec.Table, from, until)
	if len(existing) == 0 {
		// A plain table is converted in place; InnoDB needs the partition column
		// in the primary key, and the same ALTER can swap it so the table is
		// rebuilt once. The auto-increment column stays first in the key.
		key, _, change, err := keyChange(m.db.WithContext(ctx), model, spec.Table)
		if err != nil {
			return err
		}
		stmt := partitionByStatement(spec, months)
		if change {
			stmt = withPrimaryKey(stmt, spec.Table, key, m.hasPrimaryKey(ctx, spec.Table))
		}
		if err := m.db.WithContext(ctx).Exec(stmt).Error; err != nil {
			return fmt.Errorf("partitioning: partition %s: %w", spec.Table, err)
		}
		return nil
	}
	for _, p := range months {
		if existing[p.Name] {
			continue
		}
		if err := m.db.WithContext(ctx).Exec(reorganizeStatement(spec.Table, p)).Error; err != nil {
			return fmt.Errorf("partitioning: add partition %s: %w", p.Name, err)
		}
	}
	return nil
}

// hasPrimaryKey reports whether the table has a primary key to drop.
func (m *mysqlManager) hasPrimaryKey(ctx context.Context, table string) bool {
	_, cols, err := currentKey(m.db.WithContext(ctx), table)
	return err == nil && len(cols) > 0
}

// withPrimaryKey puts the primary key swap in front of the PARTITION BY clause
// of an ALTER built by partitionByStatement, so both apply in one rebuild.
func withPrimaryKey(alter, table string, key []string, drop bool) string {
	prefix := "ALTER TABLE `" + table + "` "
	var b strings.Builder
	b.WriteString(prefix)
	if drop {
		b.WriteString("DROP PRIMARY KEY, ")
	}
	b.WriteString("ADD PRIMARY KEY (")
	for i, c := range key {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("`" + c + "`")
	}
	// partition_options follow the alter specifications with no comma.
	b.WriteString(") ")
	b.WriteString(strings.TrimPrefix(alter, prefix))
	return b.String()
}

// ListPartitions returns the partitions whose names match <table>_YYYYMM,
// oldest first. The MAXVALUE catch-all is excluded.
func (m *mysqlManager) ListPartitions(ctx context.Context, table string) ([]Partition, error) {
	if !identifier.MatchString(table) {
		return nil, fmt.Errorf("partitioning: invalid table name %q", table)
	}
	existing, err := m.existingPartitions(ctx, table)
	if err != nil {
		return nil, err
	}
	var out []Partition
	for name := range existing {
		if p, ok := partitionFromName(table, name); ok {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].From.Before(out[j].From) })
	return out, nil
}

// DropPartition drops the partitions covering [from, until). It is a metadata
// operation: MySQL reclaims the rows with the dropped partition.
func (m *mysqlManager) DropPartition(ctx context.Context, table string, from, until time.Time) error {
	if !identifier.MatchString(table) {
		return fmt.Errorf("partitioning: invalid table name %q", table)
	}
	for _, p := range RangeMonths(table, from, until) {
		if err := m.db.WithContext(ctx).Exec(dropStatement(table, p.Name)).Error; err != nil {
			return fmt.Errorf("partitioning: drop partition %s: %w", p.Name, err)
		}
	}
	return nil
}

// existingPartitions returns the partition names of table from the information
// schema. A plain (unpartitioned) table yields an empty set.
func (m *mysqlManager) existingPartitions(ctx context.Context, table string) (map[string]bool, error) {
	var names []string
	err := m.db.WithContext(ctx).Raw(
		`SELECT PARTITION_NAME FROM information_schema.PARTITIONS
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND PARTITION_NAME IS NOT NULL`,
		table,
	).Scan(&names).Error
	if err != nil {
		return nil, fmt.Errorf("partitioning: list mysql partitions: %w", err)
	}
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set, nil
}

// partitionByStatement builds the single ALTER that partitions a plain table,
// covering months and ending with the MAXVALUE catch-all.
func partitionByStatement(spec Spec, months []Partition) string {
	var b strings.Builder
	b.WriteString("ALTER TABLE `")
	b.WriteString(spec.Table)
	b.WriteString("` PARTITION BY RANGE COLUMNS (`")
	b.WriteString(spec.Column)
	b.WriteString("`) (")
	for i, p := range months {
		if i > 0 {
			b.WriteString(", ")
		}
		writeRangePartition(&b, p.Name, p.Until)
	}
	b.WriteString(", PARTITION `")
	b.WriteString(spec.Table)
	b.WriteString("_")
	b.WriteString(pmax)
	b.WriteString("` VALUES LESS THAN (MAXVALUE))")
	return b.String()
}

// reorganizeStatement splits the MAXVALUE catch-all so p becomes the next
// month, immediately after the current leading bound.
func reorganizeStatement(table string, p Partition) string {
	var b strings.Builder
	b.WriteString("ALTER TABLE `")
	b.WriteString(table)
	b.WriteString("` REORGANIZE PARTITION `")
	b.WriteString(table)
	b.WriteString("_")
	b.WriteString(pmax)
	b.WriteString("` INTO (")
	writeRangePartition(&b, p.Name, p.Until)
	b.WriteString(", PARTITION `")
	b.WriteString(table)
	b.WriteString("_")
	b.WriteString(pmax)
	b.WriteString("` VALUES LESS THAN (MAXVALUE))")
	return b.String()
}

func dropStatement(table, name string) string {
	return "ALTER TABLE `" + table + "` DROP PARTITION `" + name + "`"
}

func writeRangePartition(b *strings.Builder, name string, until time.Time) {
	b.WriteString("PARTITION `")
	b.WriteString(name)
	b.WriteString("` VALUES LESS THAN (")
	b.WriteString(quoteTime(until))
	b.WriteString(")")
}
