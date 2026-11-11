package partitioning

import (
	"strings"
	"testing"
	"time"
)

func TestCreatePartitionFunctionStatement(t *testing.T) {
	t.Parallel()
	months := RangePartitions("audit_events", Monthly,
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
	t.Parallel()
	got := createPartitionSchemeStatement("ps_audit_events", "pf_audit_events")
	want := "CREATE PARTITION SCHEME [ps_audit_events] AS PARTITION [pf_audit_events] ALL TO ([PRIMARY])"
	if got != want {
		t.Errorf("createPartitionSchemeStatement = %q, want %q", got, want)
	}
}

func TestSplitMergeTruncateStatements(t *testing.T) {
	t.Parallel()
	b := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	if got, want := nextUsedStatement("ps_x"), "ALTER PARTITION SCHEME [ps_x] NEXT USED [PRIMARY]"; got != want {
		t.Errorf("nextUsedStatement = %q, want %q", got, want)
	}
	if got, want := splitRangeStatement("pf_x", b), "ALTER PARTITION FUNCTION [pf_x]() SPLIT RANGE ('2026-04-01')"; got != want {
		t.Errorf("splitRangeStatement = %q, want %q", got, want)
	}
	if got, want := mergeRangeStatement("pf_x", b), "ALTER PARTITION FUNCTION [pf_x]() MERGE RANGE ('2026-04-01')"; got != want {
		t.Errorf("mergeRangeStatement = %q, want %q", got, want)
	}
	if got, want := truncatePartitionStatement("audit_events", 3), "TRUNCATE TABLE [audit_events] WITH (PARTITIONS (3))"; got != want {
		t.Errorf("truncatePartitionStatement = %q, want %q", got, want)
	}
}

func TestPartitionNumber(t *testing.T) {
	t.Parallel()
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

func TestAddClusteredKeyStatement(t *testing.T) {
	t.Parallel()
	spec := Spec{Table: "audit_events", Column: "occurred_at", Strategy: Range, Interval: Monthly}
	got := addClusteredKeyStatement(spec, []string{"id", "occurred_at"}, "ps_audit_events")
	want := "ALTER TABLE [audit_events] ADD CONSTRAINT [pk_audit_events] PRIMARY KEY CLUSTERED ([id], [occurred_at]) ON [ps_audit_events] ([occurred_at])"
	if got != want {
		t.Errorf("addClusteredKeyStatement = %q, want %q", got, want)
	}
	if got := bracket("PK__a]b"); got != "[PK__a]]b]" {
		t.Errorf("bracket = %q", got)
	}
}

func TestCreatePartitionFunctionStatementDaily(t *testing.T) {
	t.Parallel()
	days := RangePartitions("audit_events", Daily,
		time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC))
	got := createPartitionFunctionStatement("pf_audit_events", days)
	want := "CREATE PARTITION FUNCTION [pf_audit_events] (datetimeoffset) AS RANGE RIGHT FOR VALUES ('2026-12-01', '2026-12-02')"
	if got != want {
		t.Errorf("createPartitionFunctionStatement = %q, want %q", got, want)
	}
}

func TestBoundaryInterval(t *testing.T) {
	t.Parallel()
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	for _, tc := range []struct {
		name   string
		bounds []time.Time
		want   Interval
	}{
		{"daily", []time.Time{d(2026, 12, 1), d(2026, 12, 2), d(2026, 12, 3)}, Daily},
		{"monthly", []time.Time{d(2026, 2, 1), d(2026, 3, 1), d(2026, 4, 1)}, Monthly},
		{"february", []time.Time{d(2027, 2, 1), d(2027, 3, 1)}, Monthly},
		{"single boundary defaults to daily", []time.Time{d(2026, 12, 1)}, Daily},
		{"none defaults to daily", nil, Daily},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := boundaryInterval(tc.bounds); got != tc.want {
				t.Errorf("boundaryInterval = %q, want %q", got, tc.want)
			}
		})
	}
}
