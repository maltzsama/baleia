package partitioning

import (
	"strings"
	"testing"
	"time"
)

func TestOraclePartitionByStatement(t *testing.T) {
	t.Parallel()
	spec := Spec{Table: "audit_events", Column: "occurred_at", Strategy: Range, Interval: Monthly}
	months := RangeMonths("audit_events",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	got := oraclePartitionByStatement(spec, months)
	for _, want := range []string{
		"ALTER TABLE AUDIT_EVENTS MODIFY PARTITION BY RANGE (OCCURRED_AT) (",
		"PARTITION AUDIT_EVENTS_202601 VALUES LESS THAN (TO_DATE('2026-02-01','YYYY-MM-DD'))",
		"PARTITION AUDIT_EVENTS_202602 VALUES LESS THAN (TO_DATE('2026-03-01','YYYY-MM-DD'))",
		"PARTITION AUDIT_EVENTS_PMAX VALUES LESS THAN (MAXVALUE))",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("oraclePartitionByStatement missing %q in:\n%s", want, got)
		}
	}
}

func TestOracleSplitStatement(t *testing.T) {
	t.Parallel()
	p := Partition{Name: "audit_events_202603", Until: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)}
	got := oracleSplitStatement("audit_events", p)
	want := "ALTER TABLE AUDIT_EVENTS SPLIT PARTITION AUDIT_EVENTS_PMAX AT (TO_DATE('2026-04-01','YYYY-MM-DD')) INTO (PARTITION AUDIT_EVENTS_202603, PARTITION AUDIT_EVENTS_PMAX)"
	if got != want {
		t.Errorf("oracleSplitStatement = %q, want %q", got, want)
	}
}

func TestOracleDropStatement(t *testing.T) {
	t.Parallel()
	got := oracleDropStatement("audit_events", "audit_events_202601")
	want := "ALTER TABLE AUDIT_EVENTS DROP PARTITION AUDIT_EVENTS_202601"
	if got != want {
		t.Errorf("oracleDropStatement = %q, want %q", got, want)
	}
}
