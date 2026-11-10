package partitioning

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// sqlserverManager is the SQL Server adapter. SQL Server partitions a table by
// placing its clustered index (the primary key) on a partition scheme built
// from a partition function: there is no inline PARTITION BY. A month is
// reclaimed by truncating its partition and merging its boundary — there is no
// metadata "drop partition". The partition function's type is datetimeoffset,
// matching GORM's mapping of time.Time.
type sqlserverManager struct {
	db *gorm.DB
}

func (m *sqlserverManager) Capabilities() Capabilities {
	return capabilitiesFor("sqlserver")
}

func pfName(table string) string { return "pf_" + table }
func psName(table string) string { return "ps_" + table }

// Provision creates the partition function and scheme if absent, creates the
// table ON the scheme if absent, and splits new boundaries for months beyond
// the current function.
func (m *sqlserverManager) Provision(ctx context.Context, model any, spec Spec, from, until time.Time) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	months := RangeMonths(spec.Table, from, until)
	pf, ps := pfName(spec.Table), psName(spec.Table)

	if ok, err := m.exists(ctx, "sys.partition_functions", pf); err != nil {
		return err
	} else if !ok {
		if err := m.db.WithContext(ctx).Exec(createPartitionFunctionStatement(pf, months)).Error; err != nil {
			return fmt.Errorf("partitioning: create partition function %s: %w", pf, err)
		}
	}
	if ok, err := m.exists(ctx, "sys.partition_schemes", ps); err != nil {
		return err
	} else if !ok {
		if err := m.db.WithContext(ctx).Exec(createPartitionSchemeStatement(ps, pf)).Error; err != nil {
			return fmt.Errorf("partitioning: create partition scheme %s: %w", ps, err)
		}
	}
	if !m.db.Migrator().HasTable(spec.Table) {
		// The base GORM migrator appends gorm:table_options after the column
		// list, so this places the clustered primary key on the scheme.
		if err := m.db.Set("gorm:table_options", fmt.Sprintf(" ON [%s] ([%s])", ps, spec.Column)).
			Migrator().CreateTable(model); err != nil {
			return fmt.Errorf("partitioning: create table %s: %w", spec.Table, err)
		}
		if !m.db.Migrator().HasTable(spec.Table) {
			return fmt.Errorf("partitioning: model does not create table %q", spec.Table)
		}
	}

	existing, err := m.boundaries(ctx, pf)
	if err != nil {
		return err
	}
	for _, p := range months {
		if containsTime(existing, p.Until) {
			continue
		}
		if err := m.split(ctx, ps, pf, p.Until); err != nil {
			return fmt.Errorf("partitioning: split partition function %s: %w", pf, err)
		}
	}
	return nil
}

// ListPartitions derives one partition per boundary of the partition function,
// oldest first. A boundary at month end means the partition covers the month
// that ends there.
func (m *sqlserverManager) ListPartitions(ctx context.Context, table string) ([]Partition, error) {
	if !identifier.MatchString(table) {
		return nil, fmt.Errorf("partitioning: invalid table name %q", table)
	}
	bounds, err := m.boundaries(ctx, pfName(table))
	if err != nil {
		return nil, err
	}
	var out []Partition
	for _, until := range bounds {
		from := until.AddDate(0, -1, 0)
		out = append(out, Partition{Name: table + "_" + from.Format("200601"), From: from, Until: until})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].From.Before(out[j].From) })
	return out, nil
}

// DropPartition truncates the partitions covering [from, until) and merges
// their boundaries, reclaiming the rows.
func (m *sqlserverManager) DropPartition(ctx context.Context, table string, from, until time.Time) error {
	if !identifier.MatchString(table) {
		return fmt.Errorf("partitioning: invalid table name %q", table)
	}
	pf := pfName(table)
	bounds, err := m.boundaries(ctx, pf)
	if err != nil {
		return err
	}
	for _, p := range RangeMonths(table, from, until) {
		n, ok := partitionNumber(bounds, p.Until)
		if !ok {
			continue
		}
		if err := m.db.WithContext(ctx).Exec(truncatePartitionStatement(table, n)).Error; err != nil {
			return fmt.Errorf("partitioning: truncate partition %s: %w", p.Name, err)
		}
		if err := m.db.WithContext(ctx).Exec(mergeRangeStatement(pf, p.Until)).Error; err != nil {
			return fmt.Errorf("partitioning: merge range %s: %w", p.Name, err)
		}
	}
	return nil
}

func (m *sqlserverManager) exists(ctx context.Context, catalog, name string) (bool, error) {
	var n int64
	if err := m.db.WithContext(ctx).Raw(fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE name = ?", catalog), name).Scan(&n).Error; err != nil {
		return false, fmt.Errorf("partitioning: read %s: %w", catalog, err)
	}
	return n > 0, nil
}

func (m *sqlserverManager) boundaries(ctx context.Context, pf string) ([]time.Time, error) {
	var vals []string
	if err := m.db.WithContext(ctx).Raw(
		`SELECT CONVERT(varchar(10), rv.value, 120)
		 FROM sys.partition_functions pf
		 JOIN sys.partition_range_values rv ON rv.function_id = pf.function_id
		 WHERE pf.name = ? ORDER BY rv.boundary_id`, pf,
	).Scan(&vals).Error; err != nil {
		return nil, fmt.Errorf("partitioning: list boundaries of %s: %w", pf, err)
	}
	out := make([]time.Time, 0, len(vals))
	for _, v := range vals {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *sqlserverManager) split(ctx context.Context, ps, pf string, boundary time.Time) error {
	if err := m.db.WithContext(ctx).Exec(nextUsedStatement(ps)).Error; err != nil {
		return err
	}
	return m.db.WithContext(ctx).Exec(splitRangeStatement(pf, boundary)).Error
}

func createPartitionFunctionStatement(pf string, months []Partition) string {
	var b strings.Builder
	b.WriteString("CREATE PARTITION FUNCTION [")
	b.WriteString(pf)
	b.WriteString("] (datetimeoffset) AS RANGE RIGHT FOR VALUES (")
	for i, p := range months {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quoteTime(p.Until))
	}
	b.WriteString(")")
	return b.String()
}

func createPartitionSchemeStatement(ps, pf string) string {
	return "CREATE PARTITION SCHEME [" + ps + "] AS PARTITION [" + pf + "] ALL TO ([PRIMARY])"
}

func nextUsedStatement(ps string) string {
	return "ALTER PARTITION SCHEME [" + ps + "] NEXT USED [PRIMARY]"
}

func splitRangeStatement(pf string, boundary time.Time) string {
	return "ALTER PARTITION FUNCTION [" + pf + "] SPLIT RANGE (" + quoteTime(boundary) + ")"
}

func truncatePartitionStatement(table string, n int) string {
	return fmt.Sprintf("TRUNCATE TABLE [%s] WITH (PARTITIONS (%d))", table, n)
}

func mergeRangeStatement(pf string, boundary time.Time) string {
	return "ALTER PARTITION FUNCTION [" + pf + "] MERGE RANGE (" + quoteTime(boundary) + ")"
}

func partitionNumber(bounds []time.Time, until time.Time) (int, bool) {
	for i, b := range bounds {
		if b.Equal(until) {
			return i + 1, true
		}
	}
	return 0, false
}

func containsTime(ts []time.Time, want time.Time) bool {
	for _, t := range ts {
		if t.Equal(want) {
			return true
		}
	}
	return false
}
