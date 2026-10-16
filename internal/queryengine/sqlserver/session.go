package sqlserver

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/queryengine"
)

// batchSize is how many rows one Arrow record batch holds.
const batchSize = 1024

// session is one dedicated pool connection. SQL Server has no per-session
// default schema to set (unqualified names use the login's default schema), so
// the selected schema is applied by the dialect when it qualifies a name.
type session struct {
	engine *Engine
	conn   *sql.Conn
	spec   queryengine.SessionSpec
	spid   int
}

func (s *session) setup(ctx context.Context) error {
	if err := s.conn.QueryRowContext(ctx, "SELECT @@SPID").Scan(&s.spid); err != nil {
		return mapError(err)
	}
	// NoCOUNT keeps an UPDATE/INSERT from emitting a "rows affected" result
	// set next to the real one.
	if err := s.exec(ctx, "SET NOCOUNT ON"); err != nil {
		return err
	}
	if s.spec.Timeout > 0 {
		if err := s.exec(ctx, "SET LOCK_TIMEOUT "+strconv.FormatInt(s.spec.Timeout.Milliseconds(), 10)); err != nil {
			return err
		}
	}
	if catalog := s.spec.Catalog; catalog != "" && !strings.EqualFold(catalog, s.engine.database) {
		if err := s.exec(ctx, "USE "+quoteIdent(catalog)); err != nil {
			return err
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
	_, _ = s.conn.ExecContext(ctx, "IF @@TRANCOUNT > 0 ROLLBACK TRANSACTION")
	return s.conn.Close()
}

// Execute runs one batch and streams every result set it returns as Arrow.
func (s *session) Execute(ctx context.Context, sql string, cb queryengine.Callbacks, sink queryengine.RecordSink) error {
	start := time.Now()
	if cb.OnStart != nil {
		cb.OnStart(queryengine.QueryHandle{
			EngineID: s.engine.id,
			Extra:    map[string]string{"spid": strconv.Itoa(s.spid)},
		})
	}
	rows, err := s.conn.QueryContext(ctx, sql)
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()
	return s.stream(rows, sink, start)
}

// stream turns a sql.Rows result into one Begin/Write/End sequence per result
// set (a batch may return several).
func (s *session) stream(rows *sql.Rows, sink queryengine.RecordSink, start time.Time) error {
	for {
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
		if err := sink.End(queryengine.Stats{RowCount: total, ElapsedMs: time.Since(start).Milliseconds()}); err != nil {
			return err
		}
		if !rows.NextResultSet() {
			break
		}
	}
	return mapError(rows.Err())
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

// sessionID reads the cancel handle's spid.
func sessionID(h queryengine.QueryHandle) (int, error) {
	raw := h.Extra["spid"]
	if raw == "" {
		return 0, queryengine.NewError(queryengine.CodeInternal, Kind, "", "handle has no spid")
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, queryengine.NewError(queryengine.CodeInternal, Kind, "", "invalid spid")
	}
	return n, nil
}
