// Package retention drops the partitions of a stream that are past their hot
// window, gated on the cold-sink cursor and the per-day manifest (ADR 0024,
// ADR 0026).
package retention

import (
	"context"
	"math"
	"time"

	"github.com/galileostudio/baleia/internal/persistence/partitioning"
)

// Partitions is the partition surface the retention needs.
type Partitions interface {
	ListPartitions(ctx context.Context, table string) ([]partitioning.Partition, error)
	DropPartition(ctx context.Context, table string, from, until time.Time) error
}

// Cursor reads a cold-sink cursor.
type Cursor interface {
	// OccurredAt returns the cursor's timestamp, or the zero time when none.
	OccurredAt(ctx context.Context, stream, sink string) (time.Time, error)
}

// ManifestCheck verifies a day's manifest against the cold store.
type ManifestCheck interface {
	// Verify returns why the day's manifest cannot gate a drop, or "" when it
	// exists and validates (every listed object is present, with the recorded
	// checksum and row count). A failure to read the store is an error.
	Verify(ctx context.Context, day time.Time) (reason string, err error)
}

// Retain drops the partitions of one table that are past their hot window.
type Retain struct {
	Table         string
	Stream        string
	SinkName      string
	Partitions    Partitions
	Cursor        Cursor
	Manifest      ManifestCheck
	Retention     time.Duration
	Grace         time.Duration
	MaxPartitions int
	// Cutoff, when set, replaces the hot-window cutoff (now - Retention -
	// Grace); the manual prune passes its -before day here.
	Cutoff time.Time
	// DryRun reports the droppable partitions without dropping them.
	DryRun bool
	// PreDrop, when set, runs after the gates pass and before the partition is
	// dropped, with the partition's [from, until). It removes dependent rows
	// (references, joins, snapshots) that would otherwise block the detach.
	PreDrop func(ctx context.Context, from, until time.Time) error
	Now     func() time.Time
	// OnSkip observes a skipped partition with its reason.
	OnSkip func(partition, reason string)
}

// Run drops the droppable partitions, at most MaxPartitions per run, and
// returns the names it dropped. A partition is droppable once it is entirely
// past the hot window and both the cursor and the manifest gates pass; it is
// skipped (never dropped) otherwise.
func (r *Retain) Run(ctx context.Context) ([]string, error) {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	limit := r.MaxPartitions
	switch {
	case limit == 0:
		limit = 1
	case limit < 0:
		limit = math.MaxInt // the manual prune drains every eligible partition
	}
	cutoff := r.Cutoff
	if cutoff.IsZero() {
		cutoff = now().Add(-r.Retention).Add(-r.Grace)
	}

	parts, err := r.Partitions.ListPartitions(ctx, r.Table)
	if err != nil {
		return nil, err
	}

	var dropped []string
	for _, p := range parts {
		if p.Until.After(cutoff) {
			continue // still in the hot window
		}
		cursor, err := r.Cursor.OccurredAt(ctx, r.Stream, r.SinkName)
		if err != nil {
			return dropped, err
		}
		if cursor.Before(p.Until) {
			r.skip(p.Name, "cursor not advanced")
			continue
		}
		reason, err := r.Manifest.Verify(ctx, p.From)
		if err != nil {
			return dropped, err
		}
		if reason != "" {
			r.skip(p.Name, reason)
			continue
		}
		if !r.DryRun {
			// Remove rows that reference the partition before detaching: the
			// detach fails while a non-partitioned table still points into it
			// (query history's references, joins and snapshots). If the cleanup
			// fails, the drop does not happen (fail-closed).
			if r.PreDrop != nil {
				if err := r.PreDrop(ctx, p.From, p.Until); err != nil {
					return dropped, err
				}
			}
			if err := r.Partitions.DropPartition(ctx, r.Table, p.From, p.Until); err != nil {
				return dropped, err
			}
		}
		dropped = append(dropped, p.Name)
		if len(dropped) >= limit {
			break
		}
	}
	return dropped, nil
}

func (r *Retain) skip(partition, reason string) {
	if r.OnSkip != nil {
		r.OnSkip(partition, reason)
	}
}
