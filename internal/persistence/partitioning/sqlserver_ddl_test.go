package partitioning

import (
	"strings"
	"testing"
	"time"
)

func TestCreatePartitionFunctionStatement(t *testing.T) {
	months := RangeMonths("audit_events",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	got := createPartitionFunctionStatement("pf_audit_events", months)
	for _, want := range []string{
		"CREATE PARTITION FUNCTION [pf_audit_events] (datetimeoffset) AS RANGE RIGHT FOR VALUES (",
		"'2026-02-01'",
		"'2026-03-01'",
		")",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("createPartitionFunctionStatement missing %q in:\n%s", want, got)
		}
	}
}

func TestCreatePartitionSchemeStatement(t *testing.T) {
	got := createPartitionSchemeStatement("ps_audit_events", "pf_audit_events")
	want := "CREATE PARTITION SCHEME [ps_audit_events] AS PARTITION [pf_audit_events] ALL TO ([PRIMARY])"
	if got != want {
		t.Errorf("createPartitionSchemeStatement = %q, want %q", got, want)
	}
}

func TestSplitMergeTruncateStatements(t *testing.T) {
	b := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	if got, want := nextUsedStatement("ps_x"), "ALTER PARTITION SCHEME [ps_x] NEXT USED [PRIMARY]"; got != want {
		t.Errorf("nextUsedStatement = %q, want %q", got, want)
	}
	if got, want := splitRangeStatement("pf_x", b), "ALTER PARTITION FUNCTION [pf_x] SPLIT RANGE ('2026-04-01')"; got != want {
		t.Errorf("splitRangeStatement = %q, want %q", got, want)
	}
	if got, want := mergeRangeStatement("pf_x", b), "ALTER PARTITION FUNCTION [pf_x] MERGE RANGE ('2026-04-01')"; got != want {
		t.Errorf("mergeRangeStatement = %q, want %q", got, want)
	}
	if got, want := truncatePartitionStatement("audit_events", 3), "TRUNCATE TABLE [audit_events] WITH (PARTITIONS (3))"; got != want {
		t.Errorf("truncatePartitionStatement = %q, want %q", got, want)
	}
}

func TestPartitionNumber(t *testing.T) {
	bounds := []time.Time{
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	if n, ok := partitionNumber(bounds, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)); !ok || n != 2 {
		t.Errorf("partitionNumber = %d, %v, want 2, true", n, ok)
	}
	if _, ok := partitionNumber(bounds, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)); ok {
		t.Error("partitionNumber found a missing boundary")
	}
}
