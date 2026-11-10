package partitioning

import (
	"testing"
	"time"
)

func TestMonthHelpers(t *testing.T) {
	d := time.Date(2026, 11, 10, 21, 30, 0, 0, time.UTC)
	if got, want := MonthStart(d), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("MonthStart = %v, want %v", got, want)
	}
	if got, want := NextMonth(d), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("NextMonth = %v, want %v", got, want)
	}
	if got := PartitionName("audit_events", d); got != "audit_events_202611" {
		t.Errorf("PartitionName = %q", got)
	}
	// A non-UTC instant normalizes to UTC so the month is stable.
	loc := time.FixedZone("UTC-3", -3*3600)
	late := time.Date(2026, 11, 1, 1, 0, 0, 0, loc) // 2026-11-01T04:00Z
	if got := PartitionName("x", late); got != "x_202611" {
		t.Errorf("PartitionName across zones = %q", got)
	}
}

func TestRangeMonths(t *testing.T) {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	until := time.Date(2027, 2, 3, 0, 0, 0, 0, time.UTC)
	got := RangeMonths("audit_events", from, until)
	want := []string{"audit_events_202611", "audit_events_202612", "audit_events_202701"}
	if len(got) != len(want) {
		t.Fatalf("RangeMonths returned %d partitions, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("partition %d = %q, want %q", i, got[i].Name, want[i])
		}
		if !got[i].Until.Equal(got[i].From.AddDate(0, 1, 0)) {
			t.Errorf("partition %d: Until %v is not one month after From %v", i, got[i].Until, got[i].From)
		}
	}
}

func TestRangeMonthsEmpty(t *testing.T) {
	d := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if got := RangeMonths("t", d, d); len(got) != 0 {
		t.Errorf("RangeMonths with until == from = %v, want empty", got)
	}
	if got := RangeMonths("t", d, d.AddDate(0, 0, -10)); len(got) != 0 {
		t.Errorf("RangeMonths with until before from = %v, want empty", got)
	}
}

func TestSpecValidate(t *testing.T) {
	valid := Spec{Table: "audit_events", Column: "occurred_at", Strategy: Range, Interval: Monthly}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	cases := map[string]Spec{
		"bad table":    {Table: "audit;drop", Column: "occurred_at", Strategy: Range, Interval: Monthly},
		"bad column":   {Table: "audit_events", Column: "1occurred", Strategy: Range, Interval: Monthly},
		"strategy":     {Table: "audit_events", Column: "occurred_at", Strategy: "hash", Interval: Monthly},
		"interval":     {Table: "audit_events", Column: "occurred_at", Strategy: Range, Interval: "daily"},
		"empty table":  {Table: "", Column: "occurred_at", Strategy: Range, Interval: Monthly},
		"empty column": {Table: "audit_events", Column: "", Strategy: Range, Interval: Monthly},
	}
	for name, spec := range cases {
		if err := spec.Validate(); err == nil {
			t.Errorf("%s: Validate accepted %+v", name, spec)
		}
	}
}

func TestNewRejectsNil(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) returned no error")
	}
}
