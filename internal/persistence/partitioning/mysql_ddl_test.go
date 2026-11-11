package partitioning

import (
	"strings"
	"testing"
	"time"
)

func TestPartitionByStatement(t *testing.T) {
	t.Parallel()
	spec := Spec{Table: "audit_events", Column: "occurred_at", Strategy: Range, Interval: Monthly}
	months := RangePartitions("audit_events", Monthly,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	got := partitionByStatement(spec, months)
	for _, want := range []string{
		"ALTER TABLE `audit_events` PARTITION BY RANGE COLUMNS (`occurred_at`) (",
		"PARTITION `audit_events_202601` VALUES LESS THAN ('2026-02-01')",
		"PARTITION `audit_events_202602` VALUES LESS THAN ('2026-03-01')",
		"PARTITION `audit_events_pmax` VALUES LESS THAN (MAXVALUE))",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("partitionByStatement missing %q in:\n%s", want, got)
		}
	}
}

func TestReorganizeStatement(t *testing.T) {
	t.Parallel()
	p := Partition{
		Name:  "audit_events_202603",
		From:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		Until: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	got := reorganizeStatement("audit_events", p)
	for _, want := range []string{
		"ALTER TABLE `audit_events` REORGANIZE PARTITION `audit_events_pmax` INTO (",
		"PARTITION `audit_events_202603` VALUES LESS THAN ('2026-04-01')",
		"PARTITION `audit_events_pmax` VALUES LESS THAN (MAXVALUE))",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("reorganizeStatement missing %q in:\n%s", want, got)
		}
	}
}

func TestDropStatement(t *testing.T) {
	t.Parallel()
	got := dropStatement("audit_events", "audit_events_202601")
	want := "ALTER TABLE `audit_events` DROP PARTITION `audit_events_202601`"
	if got != want {
		t.Errorf("dropStatement = %q, want %q", got, want)
	}
}

func TestWithPrimaryKey(t *testing.T) {
	t.Parallel()
	spec := Spec{Table: "audit_events", Column: "occurred_at", Strategy: Range, Interval: Monthly}
	months := RangePartitions("audit_events", Monthly,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	alter := partitionByStatement(spec, months)

	got := withPrimaryKey(alter, "audit_events", []string{"id", "occurred_at"}, true)
	want := "ALTER TABLE `audit_events` DROP PRIMARY KEY, ADD PRIMARY KEY (`id`, `occurred_at`) PARTITION BY RANGE COLUMNS"
	if !strings.HasPrefix(got, want) {
		t.Errorf("withPrimaryKey = %q, want prefix %q", got, want)
	}
	if got := withPrimaryKey(alter, "audit_events", []string{"id"}, false); strings.Contains(got, "DROP PRIMARY KEY") {
		t.Errorf("no drop expected for a table without a key: %q", got)
	}
}

func TestPartitionByStatementDaily(t *testing.T) {
	t.Parallel()
	spec := Spec{Table: "audit_events", Column: "occurred_at", Strategy: Range, Interval: Daily}
	days := RangePartitions("audit_events", Daily,
		time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC))
	got := partitionByStatement(spec, days)
	for _, want := range []string{
		"PARTITION `audit_events_20261130` VALUES LESS THAN ('2026-12-01')",
		"PARTITION `audit_events_20261201` VALUES LESS THAN ('2026-12-02')",
		"PARTITION `audit_events_pmax` VALUES LESS THAN (MAXVALUE))",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("partitionByStatement missing %q in:\n%s", want, got)
		}
	}
}
