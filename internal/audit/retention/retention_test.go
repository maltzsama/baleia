package retention

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/persistence/partitioning"
)

type fakePartitions struct {
	parts   []partitioning.Partition
	dropped [][]time.Time
}

func (f *fakePartitions) ListPartitions(context.Context, string) ([]partitioning.Partition, error) {
	return f.parts, nil
}

func (f *fakePartitions) DropPartition(_ context.Context, _ string, from, until time.Time) error {
	f.dropped = append(f.dropped, []time.Time{from, until})
	return nil
}

type fakeCursor struct {
	at time.Time
}

func (f *fakeCursor) OccurredAt(context.Context, string, string) (time.Time, error) { return f.at, nil }

// fakeManifest is a manifest check: a day in have verifies, a day in invalid
// fails with its reason, and any other day has no manifest.
type fakeManifest struct {
	have    map[time.Time]bool
	invalid map[time.Time]string
	err     error
}

func (f *fakeManifest) Verify(_ context.Context, day time.Time) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if reason, ok := f.invalid[day]; ok {
		return reason, nil
	}
	if !f.have[day] {
		return "manifest missing", nil
	}
	return "", nil
}

func month(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func TestRetainGates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)

	// Partitions: Jan (old, cursor+manifest ok), Feb (old, manifest missing),
	// Mar (old, cursor behind), Oct (hot, not a candidate).
	jan := month(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	feb := month(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	mar := month(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	oct := month(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))

	parts := &fakePartitions{parts: []partitioning.Partition{
		{Name: "audit_events_202601", From: jan, Until: jan.AddDate(0, 1, 0)},
		{Name: "audit_events_202602", From: feb, Until: feb.AddDate(0, 1, 0)},
		{Name: "audit_events_202603", From: mar, Until: mar.AddDate(0, 1, 0)},
		{Name: "audit_events_202610", From: oct, Until: oct.AddDate(0, 1, 0)},
	}}
	cursor := &fakeCursor{at: feb.AddDate(0, 1, 0)} // advanced past Feb, not Mar
	manifests := &fakeManifest{have: map[time.Time]bool{jan: true, mar: true}}

	var skipped []string
	r := &Retain{
		Table: "audit_events", Stream: "audit", SinkName: "s3",
		Partitions: parts, Cursor: cursor, Manifest: manifests,
		Retention: 30 * 24 * time.Hour, Grace: 7 * 24 * time.Hour,
		MaxPartitions: 2,
		Now:           func() time.Time { return now },
		OnSkip:        func(p, reason string) { skipped = append(skipped, p+":"+reason) },
	}
	dropped, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(dropped) != 1 || dropped[0] != "audit_events_202601" {
		t.Fatalf("dropped = %v, want [audit_events_202601]", dropped)
	}
	// Feb skipped (manifest missing), Mar skipped (cursor behind), Oct not a
	// candidate.
	if len(skipped) != 2 {
		t.Fatalf("skipped = %v", skipped)
	}
	for _, s := range skipped {
		if s == "audit_events_202602:manifest missing" || s == "audit_events_202603:cursor not advanced" {
			continue
		}
		t.Fatalf("unexpected skip %q", s)
	}
}

func TestRetainMaxPartitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	jan := month(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	feb := month(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))

	parts := &fakePartitions{parts: []partitioning.Partition{
		{Name: "audit_events_202601", From: jan, Until: jan.AddDate(0, 1, 0)},
		{Name: "audit_events_202602", From: feb, Until: feb.AddDate(0, 1, 0)},
	}}
	cursor := &fakeCursor{at: now}
	manifests := &fakeManifest{have: map[time.Time]bool{jan: true, feb: true}}

	r := &Retain{
		Table: "audit_events", Stream: "audit", SinkName: "s3",
		Partitions: parts, Cursor: cursor, Manifest: manifests,
		Retention: 30 * 24 * time.Hour, Grace: 7 * 24 * time.Hour,
		MaxPartitions: 1,
		Now:           func() time.Time { return now },
	}
	dropped, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(dropped) != 1 || dropped[0] != "audit_events_202601" {
		t.Fatalf("dropped = %v, want exactly one (the cap)", dropped)
	}
}

// TestRetainDryRun: a dry run reports the droppable partitions but never drops
// one (the manual `audit-prune --dry-run`).
func TestRetainDryRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	jan := month(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	parts := &fakePartitions{parts: []partitioning.Partition{
		{Name: "audit_events_202601", From: jan, Until: jan.AddDate(0, 1, 0)},
	}}
	r := &Retain{
		Table: "audit_events", Stream: "audit", SinkName: "s3",
		Partitions: parts, Cursor: &fakeCursor{at: now}, Manifest: &fakeManifest{have: map[time.Time]bool{jan: true}},
		Retention: 30 * 24 * time.Hour, MaxPartitions: -1, DryRun: true,
		Now: func() time.Time { return now },
	}
	names, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(names) != 1 || names[0] != "audit_events_202601" {
		t.Fatalf("names = %v, want the droppable partition", names)
	}
	if len(parts.dropped) != 0 {
		t.Fatalf("a dry run dropped %v", parts.dropped)
	}
}

// TestRetainCutoff: an explicit cutoff replaces now - retention - grace, so the
// manual `-before` month drives which partitions are candidates.
func TestRetainCutoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	jan := month(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	parts := &fakePartitions{parts: []partitioning.Partition{
		{Name: "audit_events_202601", From: jan, Until: jan.AddDate(0, 1, 0)},
	}}
	// A 400-day hot window keeps January; the explicit cutoff makes it eligible.
	r := &Retain{
		Table: "audit_events", Stream: "audit", SinkName: "s3",
		Partitions: parts, Cursor: &fakeCursor{at: now}, Manifest: &fakeManifest{have: map[time.Time]bool{jan: true}},
		Retention: 400 * 24 * time.Hour, Grace: 7 * 24 * time.Hour,
		Cutoff:        month(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)),
		MaxPartitions: -1,
		Now:           func() time.Time { return now },
	}
	names, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(names) != 1 || names[0] != "audit_events_202601" {
		t.Fatalf("names = %v, want January dropped by the explicit cutoff", names)
	}
}

// TestRetainPreDrop: the PreDrop hook runs after the gates pass, with the
// partition's bounds, and before the partition is dropped.
func TestRetainPreDrop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	jan := month(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	parts := &fakePartitions{parts: []partitioning.Partition{
		{Name: "query_statements_202601", From: jan, Until: jan.AddDate(0, 1, 0)},
	}}
	var preFrom, preUntil time.Time
	r := &Retain{
		Table: "query_statements", Stream: "query_history", SinkName: "s3",
		Partitions: parts, Cursor: &fakeCursor{at: now}, Manifest: &fakeManifest{have: map[time.Time]bool{jan: true}},
		Retention: 30 * 24 * time.Hour, MaxPartitions: -1,
		PreDrop: func(_ context.Context, from, until time.Time) error {
			preFrom, preUntil = from, until
			return nil
		},
		Now: func() time.Time { return now },
	}
	names, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("names = %v, want one", names)
	}
	if !preFrom.Equal(jan) || !preUntil.Equal(jan.AddDate(0, 1, 0)) {
		t.Fatalf("PreDrop got [%v, %v), want [%v, %v)", preFrom, preUntil, jan, jan.AddDate(0, 1, 0))
	}
}

// TestRetainDailyGates covers the daily layout: a day is droppable once it is
// past retention and grace, behind a cursor that has passed it and a manifest
// that validates; the cap counts days, and a manifest that fails validation
// holds only its own day.
func TestRetainDailyGates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 11, 15, 3, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }
	var all []partitioning.Partition
	have := map[time.Time]bool{}
	for d := 1; d <= 20; d++ { // Sep 1..20; the 45d+1d cutoff is Sep 30 03:00
		p := partitioning.Partition{Name: partitioning.PartitionName("audit_events", partitioning.Daily, day(d)), From: day(d), Until: day(d).AddDate(0, 0, 1)}
		all = append(all, p)
		have[day(d)] = true
	}
	// Today's partition and the horizon are never candidates.
	today := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	all = append(all, partitioning.Partition{Name: "audit_events_20261115", From: today, Until: today.AddDate(0, 0, 1)})
	have[today] = true

	newRetain := func(parts *fakePartitions, manifests *fakeManifest, cursor time.Time, limit int) (*Retain, *[]string) {
		skipped := &[]string{}
		return &Retain{
			Table: "audit_events", Stream: "audit", SinkName: "s3",
			Partitions: parts, Cursor: &fakeCursor{at: cursor}, Manifest: manifests,
			Retention: 45 * 24 * time.Hour, Grace: 24 * time.Hour,
			MaxPartitions: limit,
			Now:           func() time.Time { return now },
			OnSkip:        func(p, reason string) { *skipped = append(*skipped, p+":"+reason) },
		}, skipped
	}

	t.Run("cap counts days", func(t *testing.T) {
		t.Parallel()
		parts := &fakePartitions{parts: all}
		r, skipped := newRetain(parts, &fakeManifest{have: have}, now, 14)
		dropped, err := r.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// Sep 1..20 all ended before Sep 30 03:00? Only Sep 1..29 would; the
		// twenty days here are all eligible, so the cap of 14 stops the run.
		if len(dropped) != 14 || dropped[0] != "audit_events_20260901" || dropped[13] != "audit_events_20260914" || len(*skipped) != 0 {
			t.Fatalf("dropped = %v, skipped = %v", dropped, *skipped)
		}
	})
	t.Run("a day inside the window is kept", func(t *testing.T) {
		t.Parallel()
		inWindow := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC) // ends Oct 3, after the Sep 30 03:00 cutoff
		parts := &fakePartitions{parts: []partitioning.Partition{{Name: "audit_events_20261002", From: inWindow, Until: inWindow.AddDate(0, 0, 1)}}}
		r, _ := newRetain(parts, &fakeManifest{have: map[time.Time]bool{inWindow: true}}, now, 14)
		dropped, err := r.Run(ctx)
		if err != nil || len(dropped) != 0 {
			t.Fatalf("dropped = %v, %v; want none", dropped, err)
		}
	})
	t.Run("a manifest that fails validation holds only its day", func(t *testing.T) {
		t.Parallel()
		parts := &fakePartitions{parts: all[:3]}
		manifests := &fakeManifest{have: have, invalid: map[time.Time]string{day(2): "object x has 3 rows, manifest says 4"}}
		r, skipped := newRetain(parts, manifests, now, 14)
		dropped, err := r.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(dropped) != 2 || dropped[0] != "audit_events_20260901" || dropped[1] != "audit_events_20260903" {
			t.Fatalf("dropped = %v", dropped)
		}
		if len(*skipped) != 1 || (*skipped)[0] != "audit_events_20260902:object x has 3 rows, manifest says 4" {
			t.Fatalf("skipped = %v", *skipped)
		}
	})
	t.Run("a cursor behind the day holds it", func(t *testing.T) {
		t.Parallel()
		parts := &fakePartitions{parts: all[:3]}
		r, skipped := newRetain(parts, &fakeManifest{have: have}, day(2), 14) // passed Sep 1 only
		dropped, err := r.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(dropped) != 1 || dropped[0] != "audit_events_20260901" || len(*skipped) != 2 {
			t.Fatalf("dropped = %v, skipped = %v", dropped, *skipped)
		}
	})
	t.Run("a store failure stops the run", func(t *testing.T) {
		t.Parallel()
		parts := &fakePartitions{parts: all[:3]}
		r, _ := newRetain(parts, &fakeManifest{have: have, err: errors.New("s3 down")}, now, 14)
		if dropped, err := r.Run(ctx); err == nil || len(dropped) != 0 {
			t.Fatalf("dropped = %v, err = %v; want an error and no drop", dropped, err)
		}
	})
}
