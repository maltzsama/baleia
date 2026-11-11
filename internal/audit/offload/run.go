package offload

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/galileostudio/baleia/internal/audit/export"
)

// MonthReader reads one month of a stream. The offloader owns the month loop,
// the manifest and the cursor; the reader only knows the stream's query and
// row shape (ADR 0024: the mechanism is shared, only the query and rows
// differ).
type MonthReader interface {
	// ReadMonth reads every row of month after pos (advancing pos past the
	// last one) and before the cutoff, returning the objects to write, keyed
	// by object name.
	ReadMonth(ctx context.Context, month, before time.Time, pos *export.Position) (map[string][]json.RawMessage, error)
	// EarliestMonth returns the month of the stream's first row, or the zero
	// time when it has none.
	EarliestMonth(ctx context.Context, before time.Time) (time.Time, error)
}

// Offloader writes a stream's rows to the cold sink month by month, finalizing
// each month with a manifest before advancing the cursor. A month is finalized
// only once it is entirely past the safety lag, so a concurrent insert of an
// earlier timestamp cannot arrive after the month is sealed.
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
// returns the number of rows offloaded.
func (o *Offloader) Run(ctx context.Context) (int, error) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	lag := o.Lag
	if lag < 0 {
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

		objects, err := o.Reader.ReadMonth(ctx, month, before, &pos)
		if err != nil {
			return total, err
		}
		rows := 0
		for _, r := range objects {
			rows += len(r)
		}
		if rows > 0 {
			if err := o.finalize(ctx, month, objects); err != nil {
				return total, err
			}
			total += rows
		}
		// Advance the cursor to the start of the next month, whether the month
		// had rows or not: the whole month is offloaded, and the retention gate
		// requires the cursor to be at or past a partition's upper bound. A row
		// at exactly the boundary is still read (the source is after-or-equal,
		// breaking ties by id).
		pos = export.Position{OccurredAt: nextMonth(month)}
		if err := o.Cursor.Set(ctx, o.Stream, o.SinkName, pos); err != nil {
			return total, err
		}
		month = nextMonth(month)
	}
}

// finalize writes the month's objects and their manifest, in deterministic
// object-name order.
func (o *Offloader) finalize(ctx context.Context, month time.Time, objects map[string][]json.RawMessage) error {
	names := make([]string, 0, len(objects))
	for name := range objects {
		names = append(names, name)
	}
	sort.Strings(names)

	m := Manifest{
		Stream:    o.Stream,
		Month:     month.Format("2006-01"),
		Counts:    make(map[string]int64, len(names)),
		Checksums: make(map[string]string, len(names)),
	}
	for _, name := range names {
		key, count, checksum, err := o.Sink.Write(ctx, month, name, objects[name])
		if err != nil {
			return err
		}
		m.Keys = append(m.Keys, key)
		m.Counts[key] = count
		m.Checksums[key] = checksum
	}
	return o.Sink.WriteManifest(ctx, month, m)
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func nextMonth(m time.Time) time.Time {
	return monthStart(m).AddDate(0, 1, 0)
}
