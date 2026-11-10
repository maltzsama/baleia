package bigateway

import (
	"errors"
	"strings"

	"github.com/galileostudio/baleia/internal/dialect"
)

// ErrReadOnly is a statement the BI gateway refuses before it reaches Trino.
var ErrReadOnly = errors.New("bi gateway: read-only")

// ReadOnly reports whether sql is a read-only statement. The Trino catalog
// guard's baleia.mode=readonly is the security boundary; this gateway check is
// defense in depth and keeps DDL/DML out of the BI path entirely.
func ReadOnly(d dialect.Dialect, sql string) error {
	statements, err := d.SplitStatements(sql)
	if err != nil {
		return ErrReadOnly
	}
	if len(statements) == 0 {
		return ErrReadOnly
	}
	for _, statement := range statements {
		if !readOnlyStatement(statement) {
			return ErrReadOnly
		}
	}
	return nil
}

// readOnlyStatement allows query-shaped statements only. EXPLAIN is refused
// because EXPLAIN ANALYZE executes the statement it explains.
func readOnlyStatement(sql string) bool {
	fields := strings.Fields(trimLeadingComments(strings.TrimSpace(sql)))
	if len(fields) == 0 {
		return false
	}
	switch strings.ToUpper(fields[0]) {
	case "SELECT", "WITH", "SHOW", "DESCRIBE", "DESC", "TABLE", "VALUES":
		return true
	default:
		return false
	}
}

// trimLeadingComments removes whitespace, line comments and block comments
// before the first keyword. A statement that is only comments is empty.
func trimLeadingComments(s string) string {
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		switch {
		case strings.HasPrefix(s, "--"):
			i := strings.IndexByte(s, '\n')
			if i < 0 {
				return ""
			}
			s = s[i+1:]
		case strings.HasPrefix(s, "/*"):
			i := strings.Index(s, "*/")
			if i < 0 {
				return ""
			}
			s = s[i+2:]
		default:
			return s
		}
	}
}
