// Package retention drops the partitions of a stream that are past their hot
// window, gated on the cold-sink cursor and the per-month manifest (ADR 0024).
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

// ManifestCheck reports whether a month's manifest is in the cold store.
type ManifestCheck interface {
	Exists(ctx context.Context, stream string, month time.Time) (bool, error)
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
	// Grace); the manual prune passes its -before month here.
	Cutoff time.Time
	// DryRun reports the droppable partitions without dropping them.
	DryRun bool
	Now    func() time.Time
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
		ok, err := r.Manifest.Exists(ctx, r.Stream, p.From)
		if err != nil {
			return dropped, err
		}
		if !ok {
			r.skip(p.Name, "manifest missing")
			continue
		}
		if !r.DryRun {
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
