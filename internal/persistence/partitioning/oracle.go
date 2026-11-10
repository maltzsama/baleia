package partitioning

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// oracleManager is the Oracle adapter: native RANGE monthly partitioning via
// ALTER TABLE ... MODIFY PARTITION BY RANGE, with a MAXVALUE catch-all split
// off as later months are added (mirrors the MySQL adapter). Native
// partitioning requires the paid Partitioning option (ADR 0023), so this
// adapter reports it as a capability but works only on editions that license
// it. Oracle DDL auto-commits, so Provision does not wrap statements in a
// transaction.
type oracleManager struct {
	db *gorm.DB
}

func (m *oracleManager) Capabilities() Capabilities {
	return capabilitiesFor("oracle")
}

// Provision creates the table if absent (plain, through GORM), partitions it
// in one ALTER if it is still plain, and then ensures the partitions covering
// [from, until) exist, splitting months off the MAXVALUE catch-all.
func (m *oracleManager) Provision(ctx context.Context, model any, spec Spec, from, until time.Time) error {
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
	months := RangeMonths(spec.Table, from, until)
	partitioned, err := m.isPartitioned(ctx, spec.Table)
	if err != nil {
		return err
	}
	if !partitioned {
		return m.db.WithContext(ctx).Exec(oraclePartitionByStatement(spec, months)).Error
	}
	existing, err := m.partitionNames(ctx, spec.Table)
	if err != nil {
		return err
	}
	for _, p := range months {
		if existing[p.Name] {
			continue
		}
		if err := m.db.WithContext(ctx).Exec(oracleSplitStatement(spec.Table, p)).Error; err != nil {
			return fmt.Errorf("partitioning: split partition %s: %w", p.Name, err)
		}
	}
	return nil
}

// ListPartitions returns the partitions whose names match <table>_YYYYMM,
// oldest first. The MAXVALUE catch-all is excluded.
func (m *oracleManager) ListPartitions(ctx context.Context, table string) ([]Partition, error) {
	if !identifier.MatchString(table) {
		return nil, fmt.Errorf("partitioning: invalid table name %q", table)
	}
	names, err := m.partitionNames(ctx, table)
	if err != nil {
		return nil, err
	}
	var out []Partition
	for name := range names {
		if p, ok := partitionFromName(table, name); ok {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].From.Before(out[j].From) })
	return out, nil
}

// DropPartition drops the partitions covering [from, until). It is a metadata
// operation: Oracle reclaims the rows with the dropped partition.
func (m *oracleManager) DropPartition(ctx context.Context, table string, from, until time.Time) error {
	if !identifier.MatchString(table) {
		return fmt.Errorf("partitioning: invalid table name %q", table)
	}
	for _, p := range RangeMonths(table, from, until) {
		if err := m.db.WithContext(ctx).Exec(oracleDropStatement(table, p.Name)).Error; err != nil {
			return fmt.Errorf("partitioning: drop partition %s: %w", p.Name, err)
		}
	}
	return nil
}

func (m *oracleManager) isPartitioned(ctx context.Context, table string) (bool, error) {
	var n int64
	if err := m.db.WithContext(ctx).Raw(
		`SELECT COUNT(*) FROM user_part_tables WHERE table_name = ?`, table,
	).Scan(&n).Error; err != nil {
		return false, fmt.Errorf("partitioning: read user_part_tables: %w", err)
	}
	return n > 0, nil
}

func (m *oracleManager) partitionNames(ctx context.Context, table string) (map[string]bool, error) {
	var names []string
	if err := m.db.WithContext(ctx).Raw(
		`SELECT partition_name FROM user_tab_partitions WHERE table_name = ?`, table,
	).Scan(&names).Error; err != nil {
		return nil, fmt.Errorf("partitioning: list oracle partitions: %w", err)
	}
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set, nil
}

// oraclePartitionByStatement builds the single ALTER that partitions a plain
// table, covering months and ending with the MAXVALUE catch-all.
func oraclePartitionByStatement(spec Spec, months []Partition) string {
	var b strings.Builder
	b.WriteString(`ALTER TABLE "`)
	b.WriteString(spec.Table)
	b.WriteString(`" MODIFY PARTITION BY RANGE ("`)
	b.WriteString(spec.Column)
	b.WriteString(`") (`)
	for i, p := range months {
		if i > 0 {
			b.WriteString(", ")
		}
		writeOracleRangePartition(&b, p.Name, p.Until)
	}
	b.WriteString(` , PARTITION "`)
	b.WriteString(spec.Table)
	b.WriteString("_")
	b.WriteString(pmax)
	b.WriteString(`" VALUES LESS THAN (MAXVALUE))`)
	return b.String()
}

// oracleSplitStatement splits the MAXVALUE catch-all so p becomes the next
// month, immediately after the current leading bound.
func oracleSplitStatement(table string, p Partition) string {
	var b strings.Builder
	b.WriteString(`ALTER TABLE "`)
	b.WriteString(table)
	b.WriteString(`" SPLIT PARTITION "`)
	b.WriteString(table)
	b.WriteString("_")
	b.WriteString(pmax)
	b.WriteString(`" AT (`)
	b.WriteString(oracleDate(p.Until))
	b.WriteString(") INTO (")
	writeOracleRangePartition(&b, p.Name, p.Until)
	b.WriteString(` , PARTITION "`)
	b.WriteString(table)
	b.WriteString("_")
	b.WriteString(pmax)
	b.WriteString(`" VALUES LESS THAN (MAXVALUE))`)
	return b.String()
}

func oracleDropStatement(table, name string) string {
	return `ALTER TABLE "` + table + `" DROP PARTITION "` + name + `"`
}

func writeOracleRangePartition(b *strings.Builder, name string, until time.Time) {
	b.WriteString(`PARTITION "`)
	b.WriteString(name)
	b.WriteString(`" VALUES LESS THAN (`)
	b.WriteString(oracleDate(until))
	b.WriteString(`)`)
}

// oracleDate renders a month boundary as an Oracle DATE literal.
func oracleDate(t time.Time) string {
	return "TO_DATE('" + t.UTC().Format("2006-01-02") + "','YYYY-MM-DD')"
}
