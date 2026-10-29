package postgresql

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// batchSize is how many rows one Arrow record batch holds.
const batchSize = 1024

// session is one dedicated pool connection. Session state (search_path,
// statement_timeout, application_name) is set on that connection and lives
// until Close. A read-only session wraps each statement in BEGIN READ ONLY /
// ROLLBACK, so a user cannot escape by turning a setting off.
type session struct {
	engine   *Engine
	conn     *pgxpool.Conn
	spec     queryengine.SessionSpec
	readOnly bool
	pid      int32
}

func (s *session) setup(ctx context.Context) error {
	if err := s.conn.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&s.pid); err != nil {
		return mapError(err)
	}
	if s.spec.Timeout > 0 {
		if err := s.set(ctx, "statement_timeout", strconv.FormatInt(s.spec.Timeout.Milliseconds(), 10)); err != nil {
			return err
		}
	}
	if name := applicationName(s.spec.Tags); name != "" {
		if err := s.set(ctx, "application_name", name); err != nil {
			return err
		}
	}
	if len(s.spec.Schema) > 0 {
		path, err := s.searchPath(s.spec.Schema)
		if err != nil {
			return err
		}
		if err := s.set(ctx, "search_path", path); err != nil {
			return err
		}
	}
	return nil
}

func (s *session) set(ctx context.Context, name, value string) error {
	var out string
	if err := s.conn.QueryRow(ctx, "SELECT set_config($1, $2, false)", name, value).Scan(&out); err != nil {
		return mapError(err)
	}
	return nil
}

// searchPath quotes each schema and appends public, so an unqualified name
// resolves like a normal PostgreSQL session.
func (s *session) searchPath(parts []string) (string, error) {
	d := s.engine.Dialect()
	quoted := make([]string, 0, len(parts)+1)
	for _, p := range parts {
		q, err := d.QuoteIdent(p)
		if err != nil {
			return "", queryengine.NewError(queryengine.CodeNotFound, Kind, "", err.Error())
		}
		quoted = append(quoted, q)
	}
	quoted = append(quoted, "public")
	return strings.Join(quoted, ", "), nil
}

// Close rolls back an open transaction and discards the session state.
func (s *session) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.conn.Exec(ctx, "ROLLBACK")
	_, _ = s.conn.Exec(ctx, "DISCARD ALL")
	s.conn.Release()
	return nil
}

// Execute runs one statement and streams its results as Arrow.
func (s *session) Execute(ctx context.Context, sql string, cb queryengine.Callbacks, sink queryengine.RecordSink) error {
	start := time.Now()
	tag := queryengine.NewStatementTag()
	if cb.OnStart != nil {
		cb.OnStart(queryengine.QueryHandle{
			EngineID: s.engine.ID(),
			Extra: map[string]string{
				"backend_pid":               strconv.Itoa(int(s.pid)),
				queryengine.StatementTagKey: tag,
			},
		})
	}
	sql = queryengine.TagStatement(sql, tag)
	if s.readOnly {
		return s.executeReadOnly(ctx, sql, sink, start)
	}
	rows, err := s.conn.Query(ctx, sql)
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()
	return s.stream(rows, sink, start)
}

// executeReadOnly runs one statement in a read-only transaction and rolls it
// back, so no write can commit even if the connection's user could write.
func (s *session) executeReadOnly(ctx context.Context, sql string, sink queryengine.RecordSink, start time.Time) error {
	tx, err := s.conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return mapError(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return mapError(err)
	}
	err = s.stream(rows, sink, start)
	rows.Close()
	return err
}

// stream turns a pgx rows result into Arrow batches and one result set.
func (s *session) stream(rows pgx.Rows, sink queryengine.RecordSink, start time.Time) error {
	fds := rows.FieldDescriptions()
	schema, cols, err := schemaOf(fds)
	if err != nil {
		return queryengine.NewError(queryengine.CodeInternal, Kind, "", err.Error())
	}
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
	var total int64
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return mapError(err)
		}
		buf = append(buf, vals)
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
	var updated *int64
	if len(fds) == 0 {
		if n, ok := updateCount(rows.CommandTag()); ok {
			updated = &n
		}
	}
	return sink.End(queryengine.Stats{RowCount: total, UpdateCount: updated, ElapsedMs: time.Since(start).Milliseconds()})
}

// applicationName maps the session tags to application_name, truncated to the
// 63-byte identifier limit.
func applicationName(t queryengine.Tags) string {
	if t.ExecutionID == "" {
		return "baleia"
	}
	src := t.Source
	if src == "" {
		src = "query"
	}
	name := "baleia:" + src + ":" + t.ExecutionID
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// backendPID reads the cancel handle's backend pid.
func backendPID(h queryengine.QueryHandle) (int32, error) {
	raw := h.Extra["backend_pid"]
	if raw == "" {
		return 0, queryengine.NewError(queryengine.CodeInternal, Kind, "", "handle has no backend_pid")
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, queryengine.NewError(queryengine.CodeInternal, Kind, "", "invalid backend_pid")
	}
	return int32(n), nil
}
