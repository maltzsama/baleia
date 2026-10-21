package mysql

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/galileostudio/baleia/internal/queryengine"
)

// batchSize is how many rows one Arrow record batch holds.
const batchSize = 1024

// session is one dedicated pool connection. Session state (time zone, statement
// timeout, default schema, tags) is set on that connection.
type session struct {
	engine *Engine
	conn   *sql.Conn
	spec   queryengine.SessionSpec
	connID uint64
}

func (s *session) setup(ctx context.Context) error {
	if err := s.conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&s.connID); err != nil {
		return mapError(err)
	}
	// A fixed session time zone keeps TIMESTAMP values deterministic.
	if err := s.exec(ctx, "SET SESSION time_zone = '+00:00'"); err != nil {
		return err
	}
	if s.spec.Timeout > 0 {
		if s.engine.mariadb {
			secs := int64((s.spec.Timeout + time.Second - 1) / time.Second)
			if err := s.exec(ctx, "SET SESSION max_statement_time = "+strconv.FormatInt(secs, 10)); err != nil {
				return err
			}
		} else if err := s.exec(ctx, "SET SESSION max_execution_time = "+strconv.FormatInt(s.spec.Timeout.Milliseconds(), 10)); err != nil {
			return err
		}
	}
	if len(s.spec.Schema) > 0 {
		q, err := s.engine.Dialect().QuoteIdent(s.spec.Schema[len(s.spec.Schema)-1])
		if err != nil {
			return queryengine.NewError(queryengine.CodeNotFound, s.engine.kind, "", err.Error())
		}
		if err := s.exec(ctx, "USE "+q); err != nil {
			return err
		}
	}
	// MySQL's recursive CTE depth defaults to 1000; raise it so a generated
	// series can be longer. MariaDB has no such variable.
	if !s.engine.mariadb {
		if err := s.exec(ctx, "SET SESSION cte_max_recursion_depth = 100000"); err != nil {
			return err
		}
	}
	if s.spec.Tags.Source != "" || s.spec.Tags.ExecutionID != "" {
		if _, err := s.conn.ExecContext(ctx, "SET @baleia_source = ?, @baleia_execution = ?",
			s.spec.Tags.Source, s.spec.Tags.ExecutionID); err != nil {
			return mapError(err)
		}
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
	if cb.OnStart != nil {
		cb.OnStart(queryengine.QueryHandle{
			EngineID: s.engine.id,
			Extra:    map[string]string{"connection_id": strconv.FormatUint(s.connID, 10)},
		})
	}
	rows, err := s.conn.QueryContext(ctx, sql)
	if err != nil {
		s.maybeKill(ctx)
		return mapError(err)
	}
	defer rows.Close()
	if err := s.stream(rows, sink, start); err != nil {
		s.maybeKill(ctx)
		return err
	}
	return nil
}

// maybeKill stops a statement whose context was cancelled: the driver closes
// the connection, but the server keeps the statement running, so it must be
// killed by connection id.
func (s *session) maybeKill(ctx context.Context) {
	if ctx.Err() == nil {
		return
	}
	kctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	// Best effort: the statement is already abandoned client-side.
	_ = s.engine.kill(kctx, s.connID)
}

// stream turns a sql.Rows result into Arrow batches and one result set.
func (s *session) stream(rows *sql.Rows, sink queryengine.RecordSink, start time.Time) error {
	names, err := rows.Columns()
	if err != nil {
		return mapError(err)
	}
	cts, err := rows.ColumnTypes()
	if err != nil {
		return mapError(err)
	}
	schema, cols := schemaOf(names, cts)
	if err := sink.Begin(schema); err != nil {
		return err
	}
	buf := make([][]any, 0, batchSize)
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		rec, err := buildRecord(schema, cols, buf)
		if err != nil {
			return queryengine.NewError(queryengine.CodeInternal, s.engine.kind, "", err.Error())
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
	var total int64
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return mapError(err)
		}
		buf = append(buf, cloneRow(scan))
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
	return sink.End(queryengine.Stats{RowCount: total, ElapsedMs: time.Since(start).Milliseconds()})
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

// connectionID reads the cancel handle's connection id.
func connectionID(h queryengine.QueryHandle) (uint64, error) {
	raw := h.Extra["connection_id"]
	if raw == "" {
		return 0, queryengine.NewError(queryengine.CodeInternal, KindMySQL, "", "handle has no connection_id")
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, queryengine.NewError(queryengine.CodeInternal, KindMySQL, "", "invalid connection_id")
	}
	return n, nil
}
