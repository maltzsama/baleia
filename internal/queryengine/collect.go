package queryengine

import (
	"context"
	"errors"
	"fmt"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
)

// Collect runs one statement in a fresh session and returns its first result
// set as an in-memory table. Past maxRows the statement is cancelled and the
// error is resource_exhausted. The caller releases the table.
func Collect(ctx context.Context, e Engine, spec SessionSpec, sql string, maxRows int64) (arrow.Table, error) {
	return CollectWith(ctx, e, spec, sql, maxRows, Callbacks{})
}

// CollectWith is Collect with callbacks (heartbeats, handles).
func CollectWith(ctx context.Context, e Engine, spec SessionSpec, sql string, maxRows int64, cb Callbacks) (arrow.Table, error) {
	sess, err := e.OpenSession(ctx, spec)
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sink := &collectSink{maxRows: maxRows, cancel: cancel}
	defer sink.release()

	err = sess.Execute(ctx, sql, cb, sink)
	if sink.exceeded {
		return nil, NewError(CodeResource, e.Kind(), "", fmt.Sprintf("result exceeds %d rows", maxRows))
	}
	if err != nil {
		return nil, err
	}
	if sink.schema == nil {
		return nil, errors.New("queryengine: statement produced no result set")
	}
	return array.NewTableFromRecords(sink.schema, sink.records), nil
}

// collectSink keeps the first result set and ignores the others.
type collectSink struct {
	maxRows  int64
	cancel   context.CancelFunc
	schema   *arrow.Schema
	records  []arrow.RecordBatch
	rows     int64
	sets     int
	exceeded bool
}

func (s *collectSink) Begin(schema *arrow.Schema) error {
	s.sets++
	if s.sets == 1 {
		s.schema = schema
	}
	return nil
}

func (s *collectSink) Write(rec arrow.RecordBatch) error {
	if s.sets != 1 {
		rec.Release()
		return nil
	}
	s.rows += rec.NumRows()
	if s.rows > s.maxRows {
		rec.Release()
		s.exceeded = true
		s.cancel()
		return NewError(CodeResource, "", "", "row limit reached")
	}
	s.records = append(s.records, rec)
	return nil
}

func (s *collectSink) End(Stats) error { return nil }

func (s *collectSink) release() {
	for _, r := range s.records {
		r.Release()
	}
	s.records = nil
}

// CollectStrings is Collect rendered as text, for listings: one row per
// result row, NULL as "".
func CollectStrings(ctx context.Context, e Engine, spec SessionSpec, sql string, maxRows int64) ([][]string, error) {
	return CollectStringsWith(ctx, e, spec, sql, maxRows, Callbacks{})
}

// CollectStringsWith is CollectStrings with callbacks.
func CollectStringsWith(ctx context.Context, e Engine, spec SessionSpec, sql string, maxRows int64, cb Callbacks) ([][]string, error) {
	tbl, err := CollectWith(ctx, e, spec, sql, maxRows, cb)
	if err != nil {
		return nil, err
	}
	defer tbl.Release()
	rows := make([][]string, tbl.NumRows())
	for i := range rows {
		rows[i] = make([]string, tbl.NumCols())
	}
	for c := 0; c < int(tbl.NumCols()); c++ {
		offset := 0
		for _, chunk := range tbl.Column(c).Data().Chunks() {
			for i := 0; i < chunk.Len(); i++ {
				if !chunk.IsNull(i) {
					rows[offset+i][c] = chunk.ValueStr(i)
				}
			}
			offset += chunk.Len()
		}
	}
	return rows, nil
}
