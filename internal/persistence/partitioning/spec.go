// Package partitioning isolates the physical partitioning of append-only
// tables behind a seam (ADR 0023, which amends ADR 0020). Only the PostgreSQL
// adapter authors partition DDL; services and repositories never do.
package partitioning

import (
	"fmt"
	"regexp"
	"time"
)

// Strategy is the partitioning strategy. Only range partitioning is supported.
type Strategy string

const (
	// Range partitions a table by ranges of a timestamp column.
	Range Strategy = "range"
)

// Interval is the width of one partition. Only monthly partitions are supported.
type Interval string

const (
	// Monthly produces one partition per calendar month.
	Monthly Interval = "monthly"
)

// Spec describes how one table is partitioned.
type Spec struct {
	Table    string
	Column   string
	Strategy Strategy
	Interval Interval
}

// Partition is one physical partition of a table.
type Partition struct {
	Name  string
	From  time.Time // inclusive
	Until time.Time // exclusive
}

// identifier restricts the identifiers a spec may carry, so a spec can never
// inject SQL into the adapter's DDL.
var identifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// Validate rejects specs the adapter cannot safely turn into DDL.
func (s Spec) Validate() error {
	if !identifier.MatchString(s.Table) {
		return fmt.Errorf("partitioning: invalid table name %q", s.Table)
	}
	if !identifier.MatchString(s.Column) {
		return fmt.Errorf("partitioning: invalid partition column %q", s.Column)
	}
	if s.Strategy != Range {
		return fmt.Errorf("partitioning: unsupported strategy %q", s.Strategy)
	}
	if s.Interval != Monthly {
		return fmt.Errorf("partitioning: unsupported interval %q", s.Interval)
	}
	return nil
}

// maxIdentifierLen is the longest identifier PostgreSQL keeps untruncated.
const maxIdentifierLen = 63

// LegacyTable is the name a plain table is set aside under while its
// partitioned replacement is filled.
func LegacyTable(table string) string { return table + "_legacy" }

func legacyIndexName(name string) string { return "legacy_" + name }

// MonthStart returns the first instant of t's month in UTC.
func MonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// NextMonth returns the first instant of the month after t's.
func NextMonth(t time.Time) time.Time {
	return MonthStart(t).AddDate(0, 1, 0)
}

// PartitionName is the deterministic name of the partition for the month of t:
// <table>_YYYYMM.
func PartitionName(table string, t time.Time) string {
	return table + "_" + MonthStart(t).Format("200601")
}

// RangeMonths expands [from, until) into month-aligned partitions, in order.
// The boundaries are normalized to the first instant of their month.
func RangeMonths(table string, from, until time.Time) []Partition {
	from, until = MonthStart(from), MonthStart(until)
	var out []Partition
	for m := from; m.Before(until); m = m.AddDate(0, 1, 0) {
		out = append(out, Partition{Name: PartitionName(table, m), From: m, Until: m.AddDate(0, 1, 0)})
	}
	return out
}
