package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/queryengine"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// batchSize is how many rows one Arrow record batch holds.
const batchSize = 1024

// session is one dedicated database/sql connection. Session state (a PRAGMA)
// lives until Close; a read-only session is read-only at the file level
// (mode=ro + query_only) and additionally refuses write statements.
type session struct {
	engine   *Engine
	conn     *sql.Conn
	spec     queryengine.SessionSpec
	readOnly bool
}

// setup caps the connection so a stray ATTACH cannot open a file outside the
// root, even though the statement is refused first.
func (s *session) setup(ctx context.Context) error {
	if _, err := sqlite.Limit(s.conn, sqlite3.SQLITE_LIMIT_ATTACHED, 0); err != nil {
		return mapError(err)
	}
	return nil
}

// Close rolls back an open transaction and resets the session's PRAGMA state
// (so a pooled connection can be reused), then returns the connection to the
// pool.
func (s *session) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.conn.ExecContext(ctx, "ROLLBACK")
	_, _ = s.conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF")
	return s.conn.Close()
}

// Execute runs one statement and streams its results as Arrow. SQLite is
// in-process: cancelling ctx interrupts the running statement.
func (s *session) Execute(ctx context.Context, sqlText string, cb queryengine.Callbacks, sink queryengine.RecordSink) error {
	start := time.Now()
	stmt, err := s.prepare(sqlText)
	if err != nil {
		return err
	}
	if cb.OnStart != nil {
		cb.OnStart(queryengine.QueryHandle{EngineID: s.engine.ID()})
	}
	if s.spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.spec.Timeout)
		defer cancel()
	}
	rows, err := s.conn.QueryContext(ctx, stmt)
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()
	return s.stream(rows, sink, start)
}

// prepare enforces a single statement and refuses the statements SQLite would
// use to escape confinement or a read-only session. The check is a
// leading-keyword test on the one statement, which is reliable in SQLite's
// grammar.
func (s *session) prepare(sqlText string) (string, error) {
	spans, err := s.engine.Dialect().Statements(sqlText)
	if err != nil || len(spans) != 1 {
		return "", queryengine.NewError(queryengine.CodeSyntax, Kind, "", "exactly one statement is allowed")
	}
	stmt := strings.TrimSpace(spans[0].Text)
	if stmt == "" {
		return "", queryengine.NewError(queryengine.CodeSyntax, Kind, "", "empty statement")
	}
	body := stripLeading(stmt)
	switch kw := leadingKeyword(body); kw {
	case "ATTACH", "DETACH", "VACUUM":
		return "", refused(kw)
	case "PRAGMA":
		if refusedPragma(body) {
			return "", refused("PRAGMA")
		}
	default:
		if s.readOnly && writeKeywords[kw] {
			return "", queryengine.NewError(queryengine.CodePermission, Kind, "", "read-only session: "+kw+" is not allowed")
		}
	}
	// load_extension is a function, not a statement keyword: refuse any
	// reference to it, fail-closed. The driver disables extension loading by
	// default as well.
	if strings.Contains(strings.ToLower(body), "load_extension") {
		return "", refused("load_extension")
	}
	return stmt, nil
}

func refused(what string) error {
	return queryengine.NewError(queryengine.CodePermission, Kind, "", what+" is not allowed on a SQLite engine session")
}

// writeKeywords are refused in a read-only session before SQLite sees them
// (defense in depth: mode=ro already refuses them).
var writeKeywords = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "REPLACE": true,
	"CREATE": true, "DROP": true, "ALTER": true, "REINDEX": true, "ANALYZE": true,
}

// refusedPragmas change the connection's safety posture. PRAGMA reads (e.g.
// foreign_keys) stay allowed.
var refusedPragmas = map[string]bool{
	"query_only": true, "writable_schema": true, "journal_mode": true, "locking_mode": true,
}

// stripLeading removes leading whitespace and comments so a keyword is the
// first token.
func stripLeading(stmt string) string {
	s := strings.TrimSpace(stmt)
	for {
		switch {
		case strings.HasPrefix(s, "--"):
			i := strings.IndexByte(s, '\n')
			if i < 0 {
				return ""
			}
			s = strings.TrimSpace(s[i+1:])
		case strings.HasPrefix(s, "/*"):
			i := strings.Index(s, "*/")
			if i < 0 {
				return ""
			}
			s = strings.TrimSpace(s[i+2:])
		default:
			return s
		}
	}
}

// leadingKeyword is the first bare word of a statement, uppercased.
func leadingKeyword(stmt string) string {
	s := stripLeading(stmt)
	end := 0
	for end < len(s) && isWordByte(s[end]) {
		end++
	}
	return strings.ToUpper(s[:end])
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// refusedPragma reports whether the statement changes a refused PRAGMA. The
// name is the token after PRAGMA, with any schema prefix stripped.
func refusedPragma(stmt string) bool {
	rest := strings.TrimSpace(stmt[len("PRAGMA"):])
	name := rest
	if i := strings.IndexAny(rest, " \t\r\n=(;"); i >= 0 {
		name = rest[:i]
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	return refusedPragmas[strings.ToLower(name)]
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
	decls := make([]string, len(cts))
	for i := range cts {
		decls[i] = cts[i].DatabaseTypeName()
	}

	// SQLite is dynamically typed: a column's Arrow type can only be fixed
	// once every row's storage class is known (#83), so the result is buffered
	// before the schema is committed to the sink.
	var buffered [][]any
	scan := make([]any, len(names))
	ptrs := make([]any, len(names))
	for i := range scan {
		ptrs[i] = &scan[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return mapError(err)
		}
		buffered = append(buffered, cloneRow(scan))
	}
	if err := rows.Err(); err != nil {
		return mapError(err)
	}

	schema, cols := schemaOf(names, decls, buffered)
	if err := sink.Begin(schema); err != nil {
		return err
	}
	var total int64
	for i := 0; i < len(buffered); i += batchSize {
		end := min(i+batchSize, len(buffered))
		rec, err := buildRecord(schema, cols, buffered[i:end])
		if err != nil {
			return queryengine.NewError(queryengine.CodeInternal, Kind, "", err.Error())
		}
		if err := sink.Write(rec); err != nil {
			rec.Release()
			return err
		}
		total += int64(end - i)
	}
	return sink.End(queryengine.Stats{RowCount: total, ElapsedMs: time.Since(start).Milliseconds()})
}
