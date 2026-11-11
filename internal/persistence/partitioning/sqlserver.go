package partitioning

import (
	"context"
	"errors"
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
	months := RangePartitions(spec.Table, spec.Interval, from, until)
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
	} else if on, err := m.onScheme(ctx, spec.Table); err != nil {
		return err
	} else if !on {
		if err := m.convert(ctx, model, spec, ps); err != nil {
			return err
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

// onScheme reports whether the table's clustered index is placed on a
// partition scheme.
func (m *sqlserverManager) onScheme(ctx context.Context, table string) (bool, error) {
	var n int64
	if err := m.db.WithContext(ctx).Raw(
		`SELECT COUNT(*) FROM sys.indexes i
		 JOIN sys.partition_schemes s ON s.data_space_id = i.data_space_id
		 WHERE i.object_id = OBJECT_ID(?) AND i.index_id IN (0, 1)`, table,
	).Scan(&n).Error; err != nil {
		return false, fmt.Errorf("partitioning: read partition schemes of %s: %w", table, err)
	}
	return n > 0, nil
}

// convert moves a plain table onto the partition scheme in place: its primary
// key is dropped and re-added as the clustered key the model declares, placed
// on the scheme, and the model's secondary indexes are rebuilt so they follow
// the table onto the scheme. A non-aligned index would make TRUNCATE ... WITH
// (PARTITIONS) fail, which is how DropPartition reclaims a month. IDENTITY is
// untouched.
func (m *sqlserverManager) convert(ctx context.Context, model any, spec Spec, ps string) error {
	db := m.db.WithContext(ctx)
	key, current, _, err := keyChange(db, model, spec.Table)
	if err != nil {
		return err
	}
	secondary, err := secondaryIndexes(db, model, spec.Table)
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, name := range secondary {
			if err := tx.Migrator().DropIndex(model, name); err != nil {
				return fmt.Errorf("partitioning: drop index %s: %w", name, err)
			}
		}
		if current != "" {
			if err := tx.Exec("ALTER TABLE [" + spec.Table + "] DROP CONSTRAINT " + bracket(current)).Error; err != nil {
				return fmt.Errorf("partitioning: drop primary key of %s: %w", spec.Table, err)
			}
		}
		if err := tx.Exec(addClusteredKeyStatement(spec, key, ps)).Error; err != nil {
			return fmt.Errorf("partitioning: add partitioned primary key to %s: %w", spec.Table, err)
		}
		for _, name := range secondary {
			if err := tx.Migrator().CreateIndex(model, name); err != nil {
				return fmt.Errorf("partitioning: recreate index %s: %w", name, err)
			}
		}
		return nil
	})
}

// secondaryIndexes returns the names of the indexes the table has that the model
// declares (the primary key excluded), so they can be rebuilt by the ORM.
func secondaryIndexes(db *gorm.DB, model any, table string) ([]string, error) {
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(model); err != nil {
		return nil, fmt.Errorf("partitioning: parse model: %w", err)
	}
	declared := map[string]bool{}
	for _, ix := range stmt.Schema.ParseIndexes() {
		declared[ix.Name] = true
	}
	indexes, err := db.Migrator().GetIndexes(table)
	if err != nil {
		return nil, fmt.Errorf("partitioning: list indexes of %s: %w", table, err)
	}
	var out []string
	for _, idx := range indexes {
		if primary, ok := idx.PrimaryKey(); ok && primary {
			continue
		}
		if declared[idx.Name()] {
			out = append(out, idx.Name())
		}
	}
	return out, nil
}

// bracket quotes a server-generated identifier (the primary key's constraint
// name is not under our control) for T-SQL.
func bracket(name string) string { return "[" + strings.ReplaceAll(name, "]", "]]") + "]" }

func addClusteredKeyStatement(spec Spec, key []string, ps string) string {
	cols := make([]string, len(key))
	for i, c := range key {
		cols[i] = "[" + c + "]"
	}
	return "ALTER TABLE [" + spec.Table + "] ADD CONSTRAINT [pk_" + spec.Table + "] PRIMARY KEY CLUSTERED (" +
		strings.Join(cols, ", ") + ") ON [" + ps + "] ([" + spec.Column + "])"
}

// ListPartitions derives one partition per boundary of the partition function,
// oldest first. A boundary ends the partition that precedes it; the width is
// read from the spacing of the boundaries (see boundaryInterval).
func (m *sqlserverManager) ListPartitions(ctx context.Context, table string) ([]Partition, error) {
	if !identifier.MatchString(table) {
		return nil, fmt.Errorf("partitioning: invalid table name %q", table)
	}
	bounds, err := m.boundaries(ctx, pfName(table))
	if err != nil {
		return nil, err
	}
	if len(bounds) == 0 {
		return nil, nil // never provisioned
	}
	iv, err := boundaryInterval(bounds)
	if err != nil {
		return nil, fmt.Errorf("partitioning: list partitions of %s: %w", table, err)
	}
	var out []Partition
	for _, until := range bounds {
		from := iv.previous(until)
		out = append(out, Partition{Name: PartitionName(table, iv, from), From: from, Until: until})
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
	existing, err := m.ListPartitions(ctx, table)
	if err != nil {
		return err
	}
	for _, p := range partitionsIn(existing, from, until) {
		// Merging a boundary renumbers the partitions after it, so the numbers
		// are read again for every partition: reusing the list from before the
		// first merge truncates the wrong day.
		bounds, err := m.boundaries(ctx, pf)
		if err != nil {
			return err
		}
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
	return "ALTER PARTITION FUNCTION [" + pf + "]() SPLIT RANGE (" + quoteTime(boundary) + ")"
}

func truncatePartitionStatement(table string, n int) string {
	return fmt.Sprintf("TRUNCATE TABLE [%s] WITH (PARTITIONS (%d))", table, n)
}

func mergeRangeStatement(pf string, boundary time.Time) string {
	return "ALTER PARTITION FUNCTION [" + pf + "]() MERGE RANGE (" + quoteTime(boundary) + ")"
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

// errAmbiguousWidth reports a partition function whose width cannot be read.
var errAmbiguousWidth = errors.New("the partition width cannot be established from fewer than two boundaries")

// boundaryInterval infers the partition width from the partition function's
// boundaries, which carry no width of their own: the smallest gap between two
// consecutive boundaries is a day or more than 28 days (monthly). The smallest
// gap, not the first, because dropping a day merges its boundary and leaves a
// wider gap behind. With fewer than two boundaries there is no gap to read, and
// guessing could truncate a partition that holds more than the day a manifest
// covers, so it fails closed.
func boundaryInterval(bounds []time.Time) (Interval, error) {
	if len(bounds) < 2 {
		return "", errAmbiguousWidth
	}
	smallest := bounds[1].Sub(bounds[0])
	for i := 2; i < len(bounds); i++ {
		if gap := bounds[i].Sub(bounds[i-1]); gap < smallest {
			smallest = gap
		}
	}
	if smallest >= 28*24*time.Hour {
		return Monthly, nil
	}
	return Daily, nil
}

// previous returns the start of the partition that ends at until.
func (i Interval) previous(until time.Time) time.Time {
	if i == Daily {
		return until.AddDate(0, 0, -1)
	}
	return until.AddDate(0, -1, 0)
}
