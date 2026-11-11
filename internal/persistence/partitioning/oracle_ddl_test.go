package partitioning

import (
	"strings"
	"testing"
	"time"
)

func TestOraclePartitionByStatement(t *testing.T) {
	t.Parallel()
	spec := Spec{Table: "audit_events", Column: "occurred_at", Strategy: Range, Interval: Monthly}
	months := RangePartitions("audit_events", Monthly,
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
	want := "ALTER TABLE AUDIT_EVENTS DROP PARTITION AUDIT_EVENTS_202601 UPDATE GLOBAL INDEXES"
	if got != want {
		t.Errorf("oracleDropStatement = %q, want %q", got, want)
	}
}

func TestOracleLocalKeyStatement(t *testing.T) {
	t.Parallel()
	got := oracleLocalKeyStatement("audit_events", []string{"id", "occurred_at"})
	want := "ALTER TABLE AUDIT_EVENTS ADD CONSTRAINT PK_AUDIT_EVENTS PRIMARY KEY (ID, OCCURRED_AT) USING INDEX LOCAL"
	if got != want {
		t.Errorf("oracleLocalKeyStatement = %q, want %q", got, want)
	}
}

func TestOraclePartitionByStatementDaily(t *testing.T) {
	t.Parallel()
	spec := Spec{Table: "audit_events", Column: "occurred_at", Strategy: Range, Interval: Daily}
	days := RangePartitions("audit_events", Daily,
		time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC))
	got := oraclePartitionByStatement(spec, days)
	for _, want := range []string{
		"PARTITION AUDIT_EVENTS_20261130 VALUES LESS THAN (TO_DATE('2026-12-01','YYYY-MM-DD'))",
		"PARTITION AUDIT_EVENTS_20261201 VALUES LESS THAN (TO_DATE('2026-12-02','YYYY-MM-DD'))",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("oraclePartitionByStatement missing %q in:\n%s", want, got)
		}
	}
}
