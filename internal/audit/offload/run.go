package offload

import (
	"context"
	"encoding/json"
	"time"

	"github.com/galileostudio/baleia/internal/audit/export"
)

// Offloader writes a stream's rows to the cold sink month by month, finalizing
// each month with a manifest before advancing the cursor. A month is finalized
// only once it is fully past the safety lag, so a concurrent insert of an
// earlier timestamp cannot arrive after the month is sealed.
type Offloader struct {
	Stream   string // export.StreamAudit or export.StreamQueryHistory
	SinkName string // export.SinkS3
	Source   export.Source
	Cursor   export.Cursor
	Sink     *Sink
	// Document maps a row to its JSON document; nil means export.Document.
	Document func(export.Event) map[string]any
	Batch    int
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
			month = o.earliestMonth(ctx, before)
			if month.IsZero() {
				return total, nil // nothing to offload yet
			}
		}
		// A month is offloadable once it is entirely before the safety lag.
		if !nextMonth(month).Before(before) {
			return total, nil
		}

		rows, err := o.readMonth(ctx, month, before, &pos)
		if err != nil {
			return total, err
		}
		if len(rows) > 0 {
			if err := o.finalize(ctx, month, rows); err != nil {
				return total, err
			}
			total += len(rows)
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

// readMonth reads every row of month, in order, advancing pos past the last
// one.
func (o *Offloader) readMonth(ctx context.Context, month, before time.Time, pos *export.Position) ([]json.RawMessage, error) {
	doc := o.Document
	if doc == nil {
		doc = export.Document
	}
	var rows []json.RawMessage
	limit := nextMonth(month)
	for {
		events, err := o.Source.Events(ctx, *pos, before, o.batch())
		if err != nil {
			return nil, err
		}
		if len(events) == 0 {
			return rows, nil
		}
		for _, ev := range events {
			if !ev.OccurredAt.Before(limit) {
				return rows, nil // the month is done
			}
			data, err := json.Marshal(doc(ev))
			if err != nil {
				return nil, err
			}
			rows = append(rows, data)
			*pos = export.Position{OccurredAt: ev.OccurredAt, ID: ev.ID}
		}
		if len(events) < o.batch() {
			return rows, nil
		}
	}
}

// earliestMonth is the month of the first row, or the zero time when the stream
// has none.
func (o *Offloader) earliestMonth(ctx context.Context, before time.Time) time.Time {
	events, err := o.Source.Events(ctx, export.Position{}, before, 1)
	if err != nil || len(events) == 0 {
		return time.Time{}
	}
	return monthStart(events[0].OccurredAt)
}

// finalize writes the month's object and its manifest.
func (o *Offloader) finalize(ctx context.Context, month time.Time, rows []json.RawMessage) error {
	key, count, checksum, err := o.Sink.Write(ctx, month, rows)
	if err != nil {
		return err
	}
	m := Manifest{
		Stream:    o.Stream,
		Month:     month.Format("2006-01"),
		Keys:      []string{key},
		Counts:    map[string]int64{key: count},
		Checksums: map[string]string{key: checksum},
	}
	return o.Sink.WriteManifest(ctx, month, m)
}

func (o *Offloader) batch() int {
	if o.Batch <= 0 {
		return 500
	}
	return o.Batch
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func nextMonth(m time.Time) time.Time {
	return monthStart(m).AddDate(0, 1, 0)
}
