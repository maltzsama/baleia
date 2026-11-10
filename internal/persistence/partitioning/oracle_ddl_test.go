package partitioning

import (
	"strings"
	"testing"
	"time"
)

func TestOraclePartitionByStatement(t *testing.T) {
	spec := Spec{Table: "audit_events", Column: "occurred_at", Strategy: Range, Interval: Monthly}
	months := RangeMonths("audit_events",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	got := oraclePartitionByStatement(spec, months)
	for _, want := range []string{
		`ALTER TABLE "audit_events" MODIFY PARTITION BY RANGE ("occurred_at") (`,
		`PARTITION "audit_events_202601" VALUES LESS THAN (TO_DATE('2026-02-01','YYYY-MM-DD'))`,
		`PARTITION "audit_events_202602" VALUES LESS THAN (TO_DATE('2026-03-01','YYYY-MM-DD'))`,
		`PARTITION "audit_events_pmax" VALUES LESS THAN (MAXVALUE))`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("oraclePartitionByStatement missing %q in:\n%s", want, got)
		}
	}
}

func TestOracleSplitStatement(t *testing.T) {
	p := Partition{Name: "audit_events_202603", Until: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)}
	got := oracleSplitStatement("audit_events", p)
	for _, want := range []string{
		`ALTER TABLE "audit_events" SPLIT PARTITION "audit_events_pmax" AT (TO_DATE('2026-04-01','YYYY-MM-DD')) INTO (`,
		`PARTITION "audit_events_202603" VALUES LESS THAN (TO_DATE('2026-04-01','YYYY-MM-DD'))`,
		`PARTITION "audit_events_pmax" VALUES LESS THAN (MAXVALUE))`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("oracleSplitStatement missing %q in:\n%s", want, got)
		}
	}
}

func TestOracleDropStatement(t *testing.T) {
	got := oracleDropStatement("audit_events", "audit_events_202601")
	want := `ALTER TABLE "audit_events" DROP PARTITION "audit_events_202601"`
	if got != want {
		t.Errorf("oracleDropStatement = %q, want %q", got, want)
	}
}
