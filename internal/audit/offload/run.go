package offload

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/audit/export"
	"github.com/galileostudio/baleia/internal/persistence/partitioning"
)

// period is the width of one offload unit (ADR 0026): the offloader writes, and
// retention gates on, one UTC day at a time.
const period = partitioning.Daily

// ChunkRows and ChunkBytes bound one object chunk: a day is streamed in chunks
// of at most these sizes, so a very large day never accumulates the whole day —
// or one whole gzip object — in memory.
var (
	ChunkRows  = 10_000
	ChunkBytes = 32 << 20
)

// ErrNotQuiescent reports that a stream's day is not ready to seal yet (its
// rows can still change). The offloader stops the pass without advancing the
// cursor and retries on the next run.
var ErrNotQuiescent = errors.New("offload: day not quiescent")

// ErrPassDone reports that the pass bound was reached mid-day. The chunks and
// manifest written so far are committed, the cursor advanced past them, and a
// later run resumes instead of re-reading the whole day.
var ErrPassDone = errors.New("offload: pass bound reached")

// Chunk is one bounded page of a day's object.
type Chunk struct {
	Object string
	First  export.Position
	Last   export.Position
	Rows   []json.RawMessage
}

// PeriodReader streams one day of a stream as bounded chunks. The offloader
// owns the day loop, the manifest and the cursor; the reader only knows the
// stream's query and row shape (ADR 0024, ADR 0026).
type PeriodReader interface {
	// ReadPeriod reads day after pos and before the cutoff, calling emit once
	// per bounded flush. A flush carries one or more chunks (an execution and
	// its statements travel together) and the commit position the cursor may
	// advance to only after every chunk in the flush is persisted. An emit error
	// stops the read.
	ReadPeriod(ctx context.Context, day, before time.Time, pos *export.Position, emit func(chunks []Chunk, commitTo export.Position) error) error
	// EarliestPeriod returns the day of the stream's first row, or the zero
	// time when it has none.
	EarliestPeriod(ctx context.Context, before time.Time) (time.Time, error)
}

// chunker accumulates rows into one bounded chunk. take drains it.
type chunker struct {
	object string
	first  export.Position
	last   export.Position
	rows   []json.RawMessage
	size   int
}

func (c *chunker) add(pos export.Position, row json.RawMessage) {
	if len(c.rows) == 0 {
		c.first = pos
	}
	c.rows = append(c.rows, row)
	c.size += len(row)
	c.last = pos
}

func (c *chunker) full() bool { return len(c.rows) >= ChunkRows || c.size >= ChunkBytes }

func (c *chunker) take() (Chunk, bool) {
	if len(c.rows) == 0 {
		return Chunk{}, false
	}
	ch := Chunk{Object: c.object, First: c.first, Last: c.last, Rows: c.rows}
	c.rows, c.size = nil, 0
	return ch, true
}

// Offloader writes a stream's rows to the cold sink day by day, finalizing each
// day with a manifest before advancing the cursor past it. A day is finalized
// only once it is entirely past the safety lag.
type Offloader struct {
	Stream   string
	SinkName string
	Cursor   export.Cursor
	Sink     *Sink
	Reader   PeriodReader
	Lag      time.Duration
	Now      func() time.Time
	MaxPass  time.Duration
}

// Run offloads complete days until caught up or the pass bound elapses. It
// returns the number of rows offloaded. Within a day it writes bounded chunks
// and advances the cursor past each committed chunk, so an oversized day makes
// progress instead of exhausting memory and blocking retention.
//
// A stream's passes are serialized by its Temporal schedule (SKIP overlap plus a
// fixed workflow ID), so a day's manifest read-merge-write is never raced by a
// second scheduled run. Starting an offload workflow by hand with a different
// workflow ID is a debug-only action and must not overlap the schedule.
func (o *Offloader) Run(ctx context.Context) (int, error) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	lag := o.Lag
	if lag <= 0 {
		lag = export.DefaultSafetyLag
	}
	pass := o.MaxPass
	if pass <= 0 {
		pass = 5 * time.Minute
	}
	deadline := now().Add(pass)

	pos, err := o.Cursor.Get(ctx, o.Stream, o.SinkName)
	if err != nil {
		return 0, err
	}
	total := 0

	// The first day to process: the cursor's day, resolved lazily from the
	// earliest row when the cursor is unset.
	var day time.Time
	if pos.After() {
		day = period.Start(pos.OccurredAt)
	}

	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		if now().After(deadline) {
			return total, nil
		}
		before := now().Add(-lag)

		if day.IsZero() {
			day, err = o.Reader.EarliestPeriod(ctx, before)
			if err != nil {
				return total, err
			}
			if day.IsZero() {
				return total, nil // nothing to offload yet
			}
		}
		// A day is offloadable once it is entirely before the safety lag.
		if !period.Next(day).Before(before) {
			return total, nil
		}

		// Resume a day's manifest so a partial pass keeps its chunks.
		manifest, err := o.Sink.ReadManifest(ctx, day)
		if err != nil {
			return total, err
		}

		err = o.Reader.ReadPeriod(ctx, day, before, &pos, func(chunks []Chunk, commitTo export.Position) error {
			rows := 0
			for _, c := range chunks {
				if len(c.Rows) == 0 {
					continue
				}
				key := o.Sink.ChunkKey(day, c.Object, c.First)
				count, checksum, err := o.Sink.WriteChunk(ctx, key, c.Rows)
				if err != nil {
					return err
				}
				manifest.add(key, count, checksum)
				rows += int(count)
			}
			if rows == 0 {
				return nil
			}
			if err := o.Sink.WriteManifest(ctx, day, manifest); err != nil {
				return err
			}
			// Advance the cursor only after every chunk of this flush is
			// persisted, so an execution and its statements commit together.
			if err := o.Cursor.Set(ctx, o.Stream, o.SinkName, commitTo); err != nil {
				return err
			}
			total += rows
			if now().After(deadline) {
				return ErrPassDone
			}
			return nil
		})
		switch {
		case err == nil:
			// The day is fully read.
		case errors.Is(err, ErrPassDone):
			return total, nil // chunks and cursor are committed; resume later
		case errors.Is(err, ErrNotQuiescent):
			return total, nil // retry on the next run; nothing is sealed
		default:
			return total, err
		}

		// Finalize every fully read day, even when it has no rows: the manifest
		// is the retention gate, so an empty day's partition still needs one to
		// become droppable later.
		if len(manifest.Keys) == 0 {
			if err := o.Sink.WriteManifest(ctx, day, manifest); err != nil {
				return total, err
			}
		}
		pos = export.Position{OccurredAt: period.Next(day)}
		if err := o.Cursor.Set(ctx, o.Stream, o.SinkName, pos); err != nil {
			return total, err
		}
		day = period.Next(day)
	}
}
