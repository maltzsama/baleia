package retention

import (
	"context"
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

type fakeManifest struct {
	have map[time.Time]bool
}

func (f *fakeManifest) Exists(_ context.Context, _ string, month time.Time) (bool, error) {
	return f.have[month], nil
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
