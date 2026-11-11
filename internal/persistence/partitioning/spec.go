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

// Interval is the width of one partition.
type Interval string

const (
	// Monthly produces one partition per calendar month (UTC).
	Monthly Interval = "monthly"
	// Daily produces one partition per calendar day (UTC).
	Daily Interval = "daily"
)

// Valid reports whether i is a supported width.
func (i Interval) Valid() bool { return i == Monthly || i == Daily }

// Start returns the first instant of t's partition in UTC.
func (i Interval) Start(t time.Time) time.Time {
	t = t.UTC()
	if i == Daily {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// Next returns the first instant of the partition after t's.
func (i Interval) Next(t time.Time) time.Time {
	if i == Daily {
		return i.Start(t).AddDate(0, 0, 1)
	}
	return i.Start(t).AddDate(0, 1, 0)
}

// suffixLayout is the time layout of the partition-name suffix.
func (i Interval) suffixLayout() string {
	if i == Daily {
		return "20060102"
	}
	return "200601"
}

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
	if !s.Interval.Valid() {
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

// PartitionName is the deterministic name of the partition of width iv that
// holds t: <table>_YYYYMM for Monthly, <table>_YYYYMMDD for Daily.
func PartitionName(table string, iv Interval, t time.Time) string {
	return table + "_" + iv.Start(t).Format(iv.suffixLayout())
}

// RangePartitions expands [from, until) into partitions of width iv, in order.
// The boundaries are normalized to the first instant of their partition.
func RangePartitions(table string, iv Interval, from, until time.Time) []Partition {
	from, until = iv.Start(from), iv.Start(until)
	var out []Partition
	for p := from; p.Before(until); p = iv.Next(p) {
		out = append(out, Partition{Name: PartitionName(table, iv, p), From: p, Until: iv.Next(p)})
	}
	return out
}

// partitionFromName decodes a partition name of the form <table>_YYYYMM
// (monthly) or <table>_YYYYMMDD (daily). The width is read from the suffix, so
// listing needs no spec and a table holding both widths decodes correctly.
func partitionFromName(table, name string) (Partition, bool) {
	prefix := table + "_"
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		return Partition{}, false
	}
	suffix := name[len(prefix):]
	var iv Interval
	switch len(suffix) {
	case len(Monthly.suffixLayout()):
		iv = Monthly
	case len(Daily.suffixLayout()):
		iv = Daily
	default:
		return Partition{}, false
	}
	from, err := time.Parse(iv.suffixLayout(), suffix)
	if err != nil {
		return Partition{}, false
	}
	return Partition{Name: name, From: from, Until: iv.Next(from)}, true
}

// partitionsIn returns the existing partitions covering [from, until): those
// that start at or after from and before until, with both bounds normalized to
// the start of each partition's own width (as RangePartitions does), so an
// unaligned bound selects the partition it falls in. The width is read from the
// partition, which keeps a table holding both widths correct.
func partitionsIn(parts []Partition, from, until time.Time) []Partition {
	var out []Partition
	for _, p := range parts {
		iv := p.interval()
		if !p.From.Before(iv.Start(from)) && p.From.Before(iv.Start(until)) {
			out = append(out, p)
		}
	}
	return out
}

// interval is the width of the partition.
func (p Partition) interval() Interval {
	if p.Until.Equal(Daily.Next(p.From)) {
		return Daily
	}
	return Monthly
}
