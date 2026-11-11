package offload

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/audit/export"
)

// ChunkRows and ChunkBytes bound one object chunk: a month is streamed in
// chunks of at most these sizes, so a very large month never accumulates the
// whole month — or one whole gzip object — in memory.
var (
	ChunkRows  = 10_000
	ChunkBytes = 32 << 20
)

// ErrNotQuiescent reports that a stream's month is not ready to seal yet (its
// rows can still change). The offloader stops the pass without advancing the
// cursor and retries on the next run.
var ErrNotQuiescent = errors.New("offload: month not quiescent")

// ErrPassDone reports that the pass bound was reached mid-month. The chunks and
// manifest written so far are committed, the cursor advanced past them, and a
// later run resumes instead of re-reading the whole month.
var ErrPassDone = errors.New("offload: pass bound reached")

// Chunk is one bounded page of a month's object.
type Chunk struct {
	Object string
	First  export.Position
	Last   export.Position
	Rows   []json.RawMessage
}

// MonthReader streams one month of a stream as bounded chunks. The offloader
// owns the month loop, the manifest and the cursor; the reader only knows the
// stream's query and row shape (ADR 0024).
type MonthReader interface {
	// ReadMonth reads month after pos and before the cutoff, calling emit once
	// per bounded flush. A flush carries one or more chunks (an execution and
	// its statements travel together) and the commit position the cursor may
	// advance to only after every chunk in the flush is persisted. An emit error
	// stops the read.
	ReadMonth(ctx context.Context, month, before time.Time, pos *export.Position, emit func(chunks []Chunk, commitTo export.Position) error) error
	// EarliestMonth returns the month of the stream's first row, or the zero
	// time when it has none.
	EarliestMonth(ctx context.Context, before time.Time) (time.Time, error)
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

// Offloader writes a stream's rows to the cold sink month by month, finalizing
// each month with a manifest before advancing the cursor past it. A month is
// finalized only once it is entirely past the safety lag.
type Offloader struct {
	Stream   string
	SinkName string
	Cursor   export.Cursor
	Sink     *Sink
	Reader   MonthReader
	Lag      time.Duration
	Now      func() time.Time
	MaxPass  time.Duration
}

// Run offloads complete months until caught up or the pass bound elapses. It
// returns the number of rows offloaded. Within a month it writes bounded chunks
// and advances the cursor past each committed chunk, so an oversized month makes
// progress instead of exhausting memory and blocking retention.
//
// A stream's passes are serialized by its Temporal schedule (SKIP overlap plus a
// fixed workflow ID), so a month's manifest read-merge-write is never raced by a
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

	// The first month to process: the cursor's month, resolved lazily from the
	// earliest row when the cursor is unset.
	var month time.Time
	if pos.After() {
		month = monthStart(pos.OccurredAt)
	}

	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		if now().After(deadline) {
			return total, nil
		}
		before := now().Add(-lag)

		if month.IsZero() {
			month, err = o.Reader.EarliestMonth(ctx, before)
			if err != nil {
				return total, err
			}
			if month.IsZero() {
				return total, nil // nothing to offload yet
			}
		}
		// A month is offloadable once it is entirely before the safety lag.
		if !nextMonth(month).Before(before) {
			return total, nil
		}

		// Resume a month's manifest so a partial pass keeps its chunks.
		manifest, err := o.Sink.ReadManifest(ctx, month)
		if err != nil {
			return total, err
		}

		err = o.Reader.ReadMonth(ctx, month, before, &pos, func(chunks []Chunk, commitTo export.Position) error {
			rows := 0
			for _, c := range chunks {
				if len(c.Rows) == 0 {
					continue
				}
				key := o.Sink.ChunkKey(month, c.Object, c.First)
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
			if err := o.Sink.WriteManifest(ctx, month, manifest); err != nil {
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
			// The month is fully read.
		case errors.Is(err, ErrPassDone):
			return total, nil // chunks and cursor are committed; resume later
		case errors.Is(err, ErrNotQuiescent):
			return total, nil // retry on the next run; nothing is sealed
		default:
			return total, err
		}

		// Finalize every fully read month, even when it has no rows: the
		// manifest is the retention gate, so an empty month's partition still
		// needs one to become droppable later.
		if len(manifest.Keys) == 0 {
			if err := o.Sink.WriteManifest(ctx, month, manifest); err != nil {
				return total, err
			}
		}
		pos = export.Position{OccurredAt: nextMonth(month)}
		if err := o.Cursor.Set(ctx, o.Stream, o.SinkName, pos); err != nil {
			return total, err
		}
		month = nextMonth(month)
	}
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func nextMonth(m time.Time) time.Time {
	return monthStart(m).AddDate(0, 1, 0)
}
