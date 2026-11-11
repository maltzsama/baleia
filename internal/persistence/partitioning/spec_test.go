package partitioning

import (
	"testing"
	"time"
)

func TestIntervalHelpers(t *testing.T) {
	t.Parallel()
	d := time.Date(2026, 11, 10, 21, 30, 0, 0, time.UTC)
	// A non-UTC instant normalizes to UTC so the partition is stable.
	loc := time.FixedZone("UTC-3", -3*3600)
	late := time.Date(2026, 11, 1, 1, 0, 0, 0, loc) // 2026-11-01T04:00Z
	for _, tc := range []struct {
		name      string
		iv        Interval
		at        time.Time
		start     time.Time
		next      time.Time
		partition string
	}{
		{"monthly", Monthly, d, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), "audit_events_202611"},
		{"daily", Daily, d, time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC), "audit_events_20261110"},
		{"daily crosses month end", Daily, time.Date(2026, 11, 30, 23, 59, 0, 0, time.UTC), time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), "audit_events_20261130"},
		{"monthly across zones", Monthly, late, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), "audit_events_202611"},
		{"daily across zones", Daily, late, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), "audit_events_20261101"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.iv.Start(tc.at); !got.Equal(tc.start) {
				t.Errorf("Start = %v, want %v", got, tc.start)
			}
			if got := tc.iv.Next(tc.at); !got.Equal(tc.next) {
				t.Errorf("Next = %v, want %v", got, tc.next)
			}
			if got := PartitionName("audit_events", tc.iv, tc.at); got != tc.partition {
				t.Errorf("PartitionName = %q, want %q", got, tc.partition)
			}
		})
	}
}

func TestRangePartitions(t *testing.T) {
	t.Parallel()
	t.Run("monthly", func(t *testing.T) {
		t.Parallel()
		from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
		until := time.Date(2027, 2, 3, 0, 0, 0, 0, time.UTC)
		got := RangePartitions("audit_events", Monthly, from, until)
		want := []string{"audit_events_202611", "audit_events_202612", "audit_events_202701"}
		if len(got) != len(want) {
			t.Fatalf("returned %d partitions, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i].Name != want[i] {
				t.Errorf("partition %d = %q, want %q", i, got[i].Name, want[i])
			}
			if !got[i].Until.Equal(got[i].From.AddDate(0, 1, 0)) {
				t.Errorf("partition %d: Until %v is not one month after From %v", i, got[i].Until, got[i].From)
			}
		}
	})
	t.Run("daily", func(t *testing.T) {
		t.Parallel()
		from := time.Date(2026, 11, 29, 12, 0, 0, 0, time.UTC)
		until := time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC)
		got := RangePartitions("audit_events", Daily, from, until)
		want := []string{"audit_events_20261129", "audit_events_20261130", "audit_events_20261201"}
		if len(got) != len(want) {
			t.Fatalf("returned %d partitions, want %d: %+v", len(got), len(want), got)
		}
		for i := range want {
			if got[i].Name != want[i] {
				t.Errorf("partition %d = %q, want %q", i, got[i].Name, want[i])
			}
			if !got[i].Until.Equal(got[i].From.AddDate(0, 0, 1)) {
				t.Errorf("partition %d: Until %v is not one day after From %v", i, got[i].Until, got[i].From)
			}
		}
	})
	for _, iv := range []Interval{Monthly, Daily} {
		t.Run("empty "+string(iv), func(t *testing.T) {
			t.Parallel()
			d := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
			if got := RangePartitions("t", iv, d, d); len(got) != 0 {
				t.Errorf("until == from = %v, want empty", got)
			}
			if got := RangePartitions("t", iv, d, d.AddDate(0, 0, -10)); len(got) != 0 {
				t.Errorf("until before from = %v, want empty", got)
			}
		})
	}
}

func TestPartitionFromName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		in          string
		ok          bool
		from, until time.Time
	}{
		{"monthly", "audit_events_202611", true, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)},
		{"daily", "audit_events_20261110", true, time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)},
		{"daily december", "audit_events_20261231", true, time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"other table", "query_events_20261110", false, time.Time{}, time.Time{}},
		{"legacy table", "audit_events_legacy", false, time.Time{}, time.Time{}},
		{"catch-all", "audit_events_pmax", false, time.Time{}, time.Time{}},
		{"bad month", "audit_events_202613", false, time.Time{}, time.Time{}},
		{"bad day", "audit_events_20261132", false, time.Time{}, time.Time{}},
		{"seven digits", "audit_events_2026111", false, time.Time{}, time.Time{}},
		{"no suffix", "audit_events_", false, time.Time{}, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, ok := partitionFromName("audit_events", tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (%+v)", ok, tc.ok, p)
			}
			if !ok {
				return
			}
			if !p.From.Equal(tc.from) || !p.Until.Equal(tc.until) || p.Name != tc.in {
				t.Errorf("got %+v, want [%v, %v) named %q", p, tc.from, tc.until, tc.in)
			}
		})
	}
}

func TestPartitionsIn(t *testing.T) {
	t.Parallel()
	day := func(m time.Month, d int) Partition {
		from := time.Date(2026, m, d, 0, 0, 0, 0, time.UTC)
		return Partition{Name: PartitionName("t", Daily, from), From: from, Until: from.AddDate(0, 0, 1)}
	}
	month := func(m time.Month) Partition {
		from := time.Date(2026, m, 1, 0, 0, 0, 0, time.UTC)
		return Partition{Name: PartitionName("t", Monthly, from), From: from, Until: from.AddDate(0, 1, 0)}
	}
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, time.UTC) }
	names := func(ps []Partition) []string {
		var out []string
		for _, p := range ps {
			out = append(out, p.Name)
		}
		return out
	}
	days := []Partition{day(11, 1), day(11, 2), day(11, 3), day(11, 4)}
	for _, tc := range []struct {
		name        string
		parts       []Partition
		from, until time.Time
		want        []string
	}{
		{"aligned days", days, at(11, 2, 0), at(11, 4, 0), []string{"t_20261102", "t_20261103"}},
		{"unaligned bounds select the partition they fall in", days, at(11, 2, 12), at(11, 4, 6), []string{"t_20261102", "t_20261103"}},
		{"an empty range selects nothing", days, at(11, 2, 0), at(11, 2, 0), nil},
		{"a month bound selects the days inside it", days, at(11, 1, 0), at(12, 1, 0), []string{"t_20261101", "t_20261102", "t_20261103", "t_20261104"}},
		{"monthly partitions", []Partition{month(1), month(2), month(3)}, at(1, 10, 0), at(2, 1, 0), []string{"t_202601"}},
		{"mixed widths", []Partition{month(10), day(11, 1), day(11, 2)}, at(10, 1, 0), at(11, 2, 0), []string{"t_202610", "t_20261101"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := names(partitionsIn(tc.parts, tc.from, tc.until))
			if len(got) != len(tc.want) {
				t.Fatalf("partitionsIn = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("partitionsIn = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestSpecValidate(t *testing.T) {
	t.Parallel()
	for _, iv := range []Interval{Monthly, Daily} {
		valid := Spec{Table: "audit_events", Column: "occurred_at", Strategy: Range, Interval: iv}
		if err := valid.Validate(); err != nil {
			t.Fatalf("valid %s spec rejected: %v", iv, err)
		}
	}
	cases := map[string]Spec{
		"bad table":    {Table: "audit;drop", Column: "occurred_at", Strategy: Range, Interval: Monthly},
		"bad column":   {Table: "audit_events", Column: "1occurred", Strategy: Range, Interval: Monthly},
		"strategy":     {Table: "audit_events", Column: "occurred_at", Strategy: "hash", Interval: Monthly},
		"interval":     {Table: "audit_events", Column: "occurred_at", Strategy: Range, Interval: "hourly"},
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
	t.Parallel()
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) returned no error")
	}
}
