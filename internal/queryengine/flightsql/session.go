package flightsql

import (
	"context"
	"strings"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/flight"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// session is one Baleia session. Flight SQL has no server-side session state,
// so SET/SHOW are tracked locally; everything else goes to the server.
type session struct {
	engine *Engine
	spec   queryengine.SessionSpec
	vars   map[string]string
}

func (s *session) Close() error { return nil }

// Execute runs one statement and streams its Arrow result into sink.
func (s *session) Execute(ctx context.Context, sql string, cb queryengine.Callbacks, sink queryengine.RecordSink) error {
	start := time.Now()
	stmt := strings.TrimSpace(sql)
	// Flight SQL servers have no session variables; Baleia owns SET/SHOW so a
	// script's session state is consistent across the session.
	if name, value, ok := parseSet(stmt); ok {
		s.vars[name] = value
		return noResult(sink)
	}
	if name, ok := parseShow(stmt); ok {
		return emitVar(sink, name, s.vars[name])
	}

	info, err := s.engine.c.client.Execute(s.engine.c.withAuth(ctx), sql)
	if err != nil {
		return mapError(err)
	}
	if cb.OnStart != nil {
		cb.OnStart(queryengine.QueryHandle{EngineID: s.engine.ID()})
	}
	if len(info.Endpoint) == 0 {
		if err := sink.Begin(arrow.NewSchema(nil, nil)); err != nil {
			return err
		}
		return sink.End(queryengine.Stats{ElapsedMs: time.Since(start).Milliseconds()})
	}
	begun := false
	var total int64
	for _, ep := range info.Endpoint {
		rdr, err := s.engine.c.client.DoGet(s.engine.c.withAuth(ctx), ep.Ticket)
		if err != nil {
			s.cancelOnContext(ctx, info)
			return mapError(err)
		}
		if !begun {
			if err := sink.Begin(rdr.Schema()); err != nil {
				rdr.Release()
				return err
			}
			begun = true
		}
		for rdr.Next() {
			rec := rdr.RecordBatch()
			// The sink owns the batch and releases it; retain so it survives
			// the reader's release on the next Next().
			rec.Retain()
			if err := sink.Write(rec); err != nil {
				rec.Release()
				rdr.Release()
				return err
			}
			total += rec.NumRows()
		}
		err = rdr.Err()
		rdr.Release()
		if err != nil {
			s.cancelOnContext(ctx, info)
			return mapError(err)
		}
	}
	return sink.End(queryengine.Stats{RowCount: total, ElapsedMs: time.Since(start).Milliseconds()})
}

// cancelOnContext tells the server to cancel a statement whose context ended,
// best effort (older servers lack CancelFlightInfo; tickets are not persisted).
func (s *session) cancelOnContext(ctx context.Context, info *flight.FlightInfo) {
	if ctx.Err() == nil || info == nil {
		return
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	// Best effort: the stream is already abandoned client-side.
	_, _ = s.engine.c.client.CancelFlightInfo(cctx, &flight.CancelFlightInfoRequest{Info: info})
}

// parseSet recognizes "SET name = value". The value may be quoted.
func parseSet(stmt string) (name, value string, ok bool) {
	rest, found := cutPrefixFold(stmt, "SET ")
	if !found {
		return "", "", false
	}
	name, value, found = strings.Cut(rest, "=")
	if !found {
		return "", "", false
	}
	return strings.ToLower(strings.TrimSpace(name)), strings.Trim(strings.TrimSpace(value), "'"), true
}

// parseShow recognizes "SHOW name".
func parseShow(stmt string) (name string, ok bool) {
	rest, found := cutPrefixFold(stmt, "SHOW ")
	if !found {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(rest)), true
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return s, false
	}
	return s[len(prefix):], true
}

// noResult is a statement that produces no result set.
func noResult(sink queryengine.RecordSink) error {
	if err := sink.Begin(arrow.NewSchema(nil, nil)); err != nil {
		return err
	}
	return sink.End(queryengine.Stats{})
}

// emitVar returns the value of a session variable: one row of one nullable
// text column, or no row when it is unset.
func emitVar(sink queryengine.RecordSink, name, value string) error {
	schema := arrow.NewSchema([]arrow.Field{{Name: name, Type: arrow.BinaryTypes.String, Nullable: true}}, nil)
	if err := sink.Begin(schema); err != nil {
		return err
	}
	b := array.NewRecordBuilder(memory.DefaultAllocator, schema)
	if value != "" {
		b.Field(0).(*array.StringBuilder).Append(value)
	}
	rec := b.NewRecordBatch()
	b.Release()
	rows := rec.NumRows()
	if err := sink.Write(rec); err != nil {
		return err
	}
	return sink.End(queryengine.Stats{RowCount: rows})
}
