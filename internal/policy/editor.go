// Package policy decides what users may do. It never asks what an engine can
// do: capabilities describe engines, policies describe users, and the editor
// gate below holds on every engine (docs/adr, #14).
package policy

import (
	"errors"
	"strings"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/maltzsama/sqlglot-go/expressions"
)

// ErrForbiddenInEditor is the error users see for a rejected script.
var ErrForbiddenInEditor = errors.New("catalog management, session authorization and dynamic SQL are not allowed in the SQL editor")

// ForbiddenInEditor reports whether a script holds a statement the SQL editor
// must reject. The script is split with the target's dialect, so a statement
// hidden after a separator of that engine (';', T-SQL GO, Oracle "/") is
// judged on its own; a script the dialect cannot split is judged whole.
func ForbiddenInEditor(d dialect.Dialect, sql string) bool {
	stmts, err := d.SplitStatements(sql)
	if err != nil {
		stmts = []string{sql}
	}
	for _, s := range stmts {
		if statementForbiddenWithDialect(d, s) {
			return true
		}
	}
	return false
}

// EditorStatements splits a script for execution: the statements, or an
// error when the script cannot be split or holds a forbidden statement.
func EditorStatements(d dialect.Dialect, sql string) ([]string, error) {
	stmts, err := d.SplitStatements(sql)
	if err != nil {
		return nil, err
	}
	for _, s := range stmts {
		if statementForbiddenWithDialect(d, s) {
			return nil, ErrForbiddenInEditor
		}
	}
	return stmts, nil
}

// IsCatalogDDL reports whether sql is a CREATE/DROP CATALOG statement.
// Used by:
//   - the Lens poller: catalog-management DDL is infrastructure, not user
//     observability, and is discarded instead of redacted (CR-014 §5.3(1)).
//   - the user-query validator: catalog management goes through the Baleia
//     UI, never the SQL editor (CR-014 §5.3(3)). Dynamic catalog.management
//     is global — without this check, any user with query access could
//     CREATE CATALOG pointed at arbitrary hosts (exfiltration) or DROP
//     another user's catalog.
func IsCatalogDDL(sql string) bool {
	expr, err := dialect.ParseStatement(genericDialect, sql)
	return err == nil && expr != nil && isCatalogDDLExpression(expr)
}

var genericDialect = dialect.NewRegistry().MustGet(dialect.FlightSQL)

// allowedStatementKinds are the AST node kinds the SQL editor permits. A
// statement whose root kind is not listed — an opaque Command, dynamic SQL
// (Execute) or a kind the fork does not structure — is rejected fail-closed.
var allowedStatementKinds = map[expressions.Kind]bool{
	expressions.KindSelect:        true,
	expressions.KindInsert:        true,
	expressions.KindUpdate:        true,
	expressions.KindDelete:        true,
	expressions.KindMerge:         true,
	expressions.KindCreate:        true,
	expressions.KindDrop:          true,
	expressions.KindAlter:         true,
	expressions.KindTruncateTable: true,
	expressions.KindDescribe:      true,
	expressions.KindShow:          true,
	expressions.KindUse:           true,
	expressions.KindCommit:        true,
	expressions.KindRollback:      true,
	expressions.KindTransaction:   true,
	expressions.KindSavepoint:     true,
	expressions.KindSet:           true,
	expressions.KindGrant:         true,
	expressions.KindRevoke:        true,
	expressions.KindReset:         true,
	expressions.KindValues:        true,
	expressions.KindUnion:         true,
	expressions.KindIntersect:     true,
	expressions.KindExcept:        true,
	expressions.KindWith:          true,
}

// allowedCommands are the opaque Command keywords the editor still permits:
// read-only introspection the fork does not structure under some dialects
// (SHOW, EXPLAIN, DESCRIBE) and a bare BEGIN (a transaction) under T-SQL.
// Everything else a Command can hide — catalog management, session
// authorization, dynamic SQL, maintenance — is rejected.
var allowedCommands = map[string]bool{
	"SHOW":     true,
	"EXPLAIN":  true,
	"DESCRIBE": true,
	"DESC":     true,
	"USE":      true,
	"BEGIN":    true,
}

// statementForbiddenWithDialect reports whether one statement must be rejected
// by the SQL editor. Classification is an allow-list over AST node kinds,
// fail-closed: a statement the parser cannot read, or whose kind is not
// allowed, is rejected rather than assumed safe.
func statementForbiddenWithDialect(d dialect.Dialect, sql string) bool {
	if strings.TrimSpace(sql) == "" {
		return false
	}
	expr, err := dialect.ParseStatement(d, sql)
	if err != nil || expr == nil {
		return true // fail-closed: an unreadable statement cannot be judged safe
	}
	switch expr.Kind() {
	case expressions.KindCommand:
		// SET ROLE is a session switch; SET SESSION AUTHORIZATION is not. The
		// two are both opaque Commands outside PostgreSQL, so the SET command is
		// judged by its body rather than denied whole.
		if commandRoot(expr) == "SET" {
			return setSessionAuthorization(expr)
		}
		return !allowedCommands[commandRoot(expr)]
	case expressions.KindSet:
		// PostgreSQL parses SET SESSION AUTHORIZATION as a structured Set,
		// unlike the other engines where it is an opaque Command.
		return setSessionAuthorization(expr)
	case expressions.KindExecute:
		// T-SQL parses EXECUTE IMMEDIATE (dynamic SQL) as an Execute; a plain
		// EXECUTE <proc> call is a stored-procedure invocation and is allowed.
		return executeImmediate(expr)
	default:
		return !allowedStatementKinds[expr.Kind()]
	}
}

// setSessionAuthorization reports whether a Set node spells
// SET SESSION AUTHORIZATION, which lets a user impersonate an admin target the
// guard grants to.
func setSessionAuthorization(expr expressions.Expression) bool {
	fields := strings.Fields(strings.ToUpper(stripLeadingComments(spanText(expr))))
	return len(fields) >= 3 && fields[0] == "SET" && fields[1] == "SESSION" && fields[2] == "AUTHORIZATION"
}

// executeImmediate reports whether an Execute node is T-SQL's dynamic
// EXECUTE IMMEDIATE, which can run arbitrary SQL.
func executeImmediate(expr expressions.Expression) bool {
	return expr.This() != nil && strings.EqualFold(expr.This().Name(), "IMMEDIATE")
}

// isCatalogDDLExpression reports whether a parsed statement is CREATE/DROP
// CATALOG. The command's object is the first word of its body.
func isCatalogDDLExpression(expr expressions.Expression) bool {
	root := commandRoot(expr)
	if root != "CREATE" && root != "DROP" {
		return false
	}
	fields := strings.Fields(commandBody(expr))
	return len(fields) > 0 && strings.EqualFold(fields[0], "CATALOG")
}

// commandRoot is the leading keyword of a Command node: CREATE, DROP, SET,
// EXECUTE, SHOW, ...
func commandRoot(expr expressions.Expression) string {
	if expr == nil || expr.Kind() != expressions.KindCommand {
		return ""
	}
	switch part := expr.Arg("this").(type) {
	case string:
		return strings.ToUpper(part)
	case expressions.Expression:
		if part != nil {
			return strings.ToUpper(part.Name())
		}
	}
	return ""
}

// commandBody is the Command's body after its leading keyword. The command's
// `expression` arg is the body without any leading comment, which the v0.42
// spans otherwise include.
func commandBody(expr expressions.Expression) string {
	if body, ok := expr.Arg("expression").(string); ok {
		return strings.TrimSpace(body)
	}
	return stripLeadingComments(spanText(expr))
}

// stripLeadingComments removes leading whitespace and comments from a
// statement's source text, so keyword inspection reads the statement, not a
// leading comment the span now covers.
func stripLeadingComments(s string) string {
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = s[i+2:]
				continue
			}
			return ""
		default:
			return s
		}
	}
}

// spanText is the verbatim source the parser recorded for the node, or "" when
// the node has none.
func spanText(expr expressions.Expression) string {
	if expr == nil {
		return ""
	}
	span, _ := expr.SpanText()
	return span
}
