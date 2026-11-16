package bigateway

import (
	"errors"
	"strings"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/maltzsama/sqlglot-go/expressions"
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
		if !readOnlyStatement(d, statement) {
			return ErrReadOnly
		}
	}
	return nil
}

// readOnlyStatement allows query-shaped statements only, classified by AST node
// kind. EXPLAIN is refused because EXPLAIN ANALYZE executes the statement it
// explains; a statement the parser cannot read is not read-only.
func readOnlyStatement(d dialect.Dialect, sql string) bool {
	expr, err := dialect.ParseStatement(d, sql)
	if err != nil || expr == nil {
		return false
	}
	switch expr.Kind() {
	case expressions.KindSelect, expressions.KindValues, expressions.KindShow,
		expressions.KindUnion, expressions.KindIntersect, expressions.KindExcept:
		return true
	case expressions.KindDescribe:
		// EXPLAIN (and EXPLAIN ANALYZE) can execute the explained statement;
		// only a plain DESCRIBE is read-only. Under PostgreSQL both parse to a
		// Describe node, distinguished by its kind.
		return expr.Text("kind") != "EXPLAIN"
	case expressions.KindCommand:
		// SHOW is read-only under dialects that parse it as an opaque Command
		// (Trino, T-SQL, Oracle); EXPLAIN, CALL, PREPARE and EXECUTE are not.
		return commandKeyword(expr) == "SHOW"
	default:
		return false
	}
}

// commandKeyword is the leading keyword of a Command node.
func commandKeyword(expr expressions.Expression) string {
	if expr == nil || expr.Kind() != expressions.KindCommand {
		return ""
	}
	if v, ok := expr.Arg("this").(string); ok {
		return strings.ToUpper(v)
	}
	return ""
}
