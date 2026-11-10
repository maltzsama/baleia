package partitioning

import (
	"strings"
	"testing"
	"time"
)

func TestPartitionByStatement(t *testing.T) {
	spec := Spec{Table: "audit_events", Column: "occurred_at", Strategy: Range, Interval: Monthly}
	months := RangeMonths("audit_events",
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
	got := dropStatement("audit_events", "audit_events_202601")
	want := "ALTER TABLE `audit_events` DROP PARTITION `audit_events_202601`"
	if got != want {
		t.Errorf("dropStatement = %q, want %q", got, want)
	}
}
