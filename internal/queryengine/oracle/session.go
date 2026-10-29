package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/queryengine"
)

// batchSize is how many rows one Arrow record batch holds.
const batchSize = 1024

// session is one dedicated pool connection. Session state (time zone, current
// schema, client info) is set on that connection.
type session struct {
	engine *Engine
	conn   *sql.Conn
	spec   queryengine.SessionSpec
	sid    int
	serial int
}

func (s *session) setup(ctx context.Context) error {
	// A fixed session time zone keeps TIMESTAMP WITH LOCAL TIME ZONE values
	// deterministic. NLS formats are never relied upon: the driver reads the
	// types natively.
	if err := s.exec(ctx, "ALTER SESSION SET TIME_ZONE = '+00:00'"); err != nil {
		return err
	}
	if len(s.spec.Schema) > 0 {
		q, err := s.engine.Dialect().QuoteIdent(s.spec.Schema[len(s.spec.Schema)-1])
		if err != nil {
			return queryengine.NewError(queryengine.CodeNotFound, Kind, "", err.Error())
		}
		if err := s.exec(ctx, "ALTER SESSION SET CURRENT_SCHEMA = "+q); err != nil {
			return err
		}
	}
	if s.spec.Tags.Source != "" || s.spec.Tags.ExecutionID != "" {
		d := s.engine.Dialect()
		block := fmt.Sprintf("BEGIN DBMS_APPLICATION_INFO.SET_MODULE(%s, %s); DBMS_APPLICATION_INFO.SET_CLIENT_INFO(%s); END;",
			d.QuoteString("baleia"), d.QuoteString(s.spec.Tags.Source), d.QuoteString(s.spec.Tags.ExecutionID))
		if err := s.exec(ctx, block); err != nil {
			return err
		}
	}
	// The session's sid/serial, for a cross-process cancel when v$session is
	// readable. Best effort: a login without that grant keeps in-process cancel.
	if s.engine.cancelWithAlterSystem {
		_ = s.conn.QueryRowContext(ctx,
			"SELECT sid, serial# FROM v$session WHERE sid = SYS_CONTEXT('USERENV','SID')").Scan(&s.sid, &s.serial)
	}
	return nil
}

func (s *session) exec(ctx context.Context, sql string) error {
	if _, err := s.conn.ExecContext(ctx, sql); err != nil {
		return mapError(err)
	}
	return nil
}

// Close rolls back an open transaction and returns the connection to the pool.
func (s *session) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.conn.ExecContext(ctx, "ROLLBACK")
	return s.conn.Close()
}

// Execute runs one statement and streams its results as Arrow.
func (s *session) Execute(ctx context.Context, sql string, cb queryengine.Callbacks, sink queryengine.RecordSink) error {
	start := time.Now()
	extra := map[string]string{"sid": strconv.Itoa(s.sid), "serial": strconv.Itoa(s.serial)}
	// Oracle shares cursors by exact text, comments included: a unique tag
	// makes every statement a hard parse. Tag only when a cross-process
	// Cancel can use it.
	if s.engine.cancelWithAlterSystem {
		tag := queryengine.NewStatementTag()
		extra[queryengine.StatementTagKey] = tag
		sql = queryengine.TagStatement(sql, tag)
	}
	if cb.OnStart != nil {
		cb.OnStart(queryengine.QueryHandle{EngineID: s.engine.id, Extra: extra})
	}
	rows, err := s.conn.QueryContext(ctx, sql)
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()
	return s.stream(rows, sink, start)
}

// stream turns a sql.Rows result into Arrow batches and one result set. Begin is
// called with the first batch's schema, so a truncated LOB can be flagged in the
// schema before any record is written.
func (s *session) stream(rows *sql.Rows, sink queryengine.RecordSink, start time.Time) error {
	names, err := rows.Columns()
	if err != nil {
		return mapError(err)
	}
	cts, err := rows.ColumnTypes()
	if err != nil {
		return mapError(err)
	}
	base, cols := schemaOf(names, cts)
	var (
		buf       = make([][]any, 0, batchSize)
		began     bool
		truncated bool
		total     int64
	)
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		schema := base
		if truncated {
			schema = withLobTruncation(base, cols)
		}
		if !began {
			if err := sink.Begin(schema); err != nil {
				return err
			}
			began = true
		}
		rec, err := buildRecord(schema, cols, buf)
		if err != nil {
			return queryengine.NewError(queryengine.CodeInternal, Kind, "", err.Error())
		}
		if err := sink.Write(rec); err != nil {
			rec.Release()
			return err
		}
		buf = buf[:0]
		return nil
	}
	scan := make([]any, len(names))
	ptrs := make([]any, len(names))
	for i := range scan {
		ptrs[i] = &scan[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return mapError(err)
		}
		row := cloneRow(scan)
		if s.capLobs(cols, row) {
			truncated = true
		}
		buf = append(buf, row)
		total++
		if len(buf) >= batchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return mapError(err)
	}
	if err := flush(); err != nil {
		return err
	}
	if !began {
		if err := sink.Begin(base); err != nil {
			return err
		}
	}
	return sink.End(queryengine.Stats{RowCount: total, ElapsedMs: time.Since(start).Milliseconds()})
}

// capLobs truncates a LOB value to the byte limit and reports whether it did.
func (s *session) capLobs(cols []column, row []any) bool {
	truncated := false
	for i := range cols {
		if !cols[i].lob {
			continue
		}
		switch v := row[i].(type) {
		case string:
			if len(v) > s.engine.lobMaxBytes {
				row[i] = truncateUTF8(v, s.engine.lobMaxBytes)
				truncated = true
			}
		case []byte:
			if len(v) > s.engine.lobMaxBytes {
				row[i] = v[:s.engine.lobMaxBytes]
				truncated = true
			}
		}
	}
	return truncated
}

// cloneRow copies a scanned row so a driver byte slice reused by the next row
// cannot corrupt a buffered batch.
func cloneRow(row []any) []any {
	out := make([]any, len(row))
	for i, v := range row {
		if b, ok := v.([]byte); ok {
			out[i] = append([]byte(nil), b...)
			continue
		}
		out[i] = v
	}
	return out
}

// truncateUTF8 cuts s to at most n bytes on a rune boundary.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// sessionIDs reads the cancel handle's sid and serial.
func sessionIDs(h queryengine.QueryHandle) (int, int, error) {
	sid, err := strconv.Atoi(h.Extra["sid"])
	if err != nil {
		return 0, 0, queryengine.NewError(queryengine.CodeInternal, Kind, "", "handle has no sid")
	}
	serial, err := strconv.Atoi(h.Extra["serial"])
	if err != nil {
		return 0, 0, queryengine.NewError(queryengine.CodeInternal, Kind, "", "handle has no serial")
	}
	return sid, serial, nil
}
