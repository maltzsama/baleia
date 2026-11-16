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
var ErrForbiddenInEditor = errors.New("catalog management and session authorization are not allowed in the SQL editor")

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

// statementForbiddenWithDialect reports whether one statement must be rejected
// by the SQL editor. Classification is by AST node kind, fail-closed: a
// statement the parser cannot read is rejected rather than assumed safe.
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
		return commandForbidden(d, expr)
	case expressions.KindSet:
		// PostgreSQL parses SET SESSION AUTHORIZATION as a structured Set,
		// unlike the other engines where it is an opaque Command.
		return setSessionAuthorization(expr)
	case expressions.KindExecute:
		// T-SQL parses EXECUTE IMMEDIATE (dynamic SQL) as an Execute; a plain
		// EXECUTE <proc> call is not dynamic SQL and is allowed.
		return executeImmediateForbidden(d, expr)
	default:
		return false
	}
}

// commandForbidden classifies an opaque Command node by its leading keyword.
// The deny list is what a Command can hide that the structured grammar does
// not: catalog management, session-authorization escalation and dynamic SQL
// that smuggles catalog DDL.
func commandForbidden(d dialect.Dialect, expr expressions.Expression) bool {
	switch commandRoot(expr) {
	case "CREATE", "DROP":
		return isCatalogDDLExpression(expr)
	case "SET":
		return setSessionAuthorization(expr)
	case "EXECUTE":
		body := commandBody(expr)
		if !strings.HasPrefix(strings.ToUpper(body), "IMMEDIATE") {
			return false
		}
		return isCatalogDDLWithDialect(d, extractExecuteImmediateLiteral(body))
	default:
		return false
	}
}

// setSessionAuthorization reports whether a Set or Command node spells
// SET SESSION AUTHORIZATION, which lets a user impersonate an admin target the
// guard grants to. PostgreSQL parses it as a structured Set; the other engines
// leave it an opaque Command.
func setSessionAuthorization(expr expressions.Expression) bool {
	fields := strings.Fields(strings.ToUpper(spanText(expr)))
	return len(fields) >= 3 && fields[0] == "SET" && fields[1] == "SESSION" && fields[2] == "AUTHORIZATION"
}

// executeImmediateForbidden reports whether an Execute node is T-SQL's
// EXECUTE IMMEDIATE whose string literal is catalog DDL.
func executeImmediateForbidden(d dialect.Dialect, expr expressions.Expression) bool {
	if expr.This() == nil || !strings.EqualFold(expr.This().Name(), "IMMEDIATE") {
		return false
	}
	return isCatalogDDLWithDialect(d, extractExecuteImmediateLiteral(spanText(expr)))
}

// isCatalogDDLWithDialect parses one already-split statement and reports
// whether it is catalog DDL. A statement the grammar rejects (a fragment with
// an escaped quote) still counts when its leading words spell CREATE/DROP
// CATALOG.
func isCatalogDDLWithDialect(d dialect.Dialect, sql string) bool {
	if strings.TrimSpace(sql) == "" {
		return false
	}
	expr, err := dialect.ParseStatement(d, sql)
	return (err == nil && expr != nil && isCatalogDDLExpression(expr)) || isCatalogDDLWords(sql)
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
// EXECUTE, ...
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

// commandBody is the Command's source after its leading keyword.
func commandBody(expr expressions.Expression) string {
	span := spanText(expr)
	first := strings.IndexFunc(span, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	if first < 0 {
		return ""
	}
	return strings.TrimSpace(span[first:])
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

// extractExecuteImmediateLiteral pulls the first single-quoted string literal
// out of an EXECUTE IMMEDIATE statement, honoring ” as an escaped quote. The
// closing quote is consumed; the returned body has no surrounding quotes.
func extractExecuteImmediateLiteral(s string) string {
	i := strings.IndexByte(s, '\'')
	if i < 0 {
		return ""
	}
	var b strings.Builder
	i++ // skip the opening quote
	for i < len(s) {
		switch s[i] {
		case '\'':
			if i+1 < len(s) && s[i+1] == '\'' {
				b.WriteByte('\'')
				i += 2
				continue
			}
			return strings.TrimSpace(b.String())
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return strings.TrimSpace(b.String())
}

// isCatalogDDLWords reports whether a dynamic-SQL string is catalog DDL by its
// leading words, past any leading comments. It backs the EXECUTE IMMEDIATE
// check: the literal is data the outer parser does not see, and it may be a
// fragment the grammar rejects, so it is inspected as text, not re-parsed.
func isCatalogDDLWords(sql string) bool {
	fields := strings.Fields(collapseWhitespace(trimLeadingComments(sql)))
	return len(fields) >= 2 &&
		(strings.EqualFold(fields[0], "CREATE") || strings.EqualFold(fields[0], "DROP")) &&
		strings.EqualFold(fields[1], "CATALOG")
}

// collapseWhitespace replaces every run of whitespace with a single space and
// strips leading/trailing whitespace.
func collapseWhitespace(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}

// trimLeadingComments strips leading line comments (--) and block comments
// (/* ... */) before the first token of a dynamic-SQL string.
func trimLeadingComments(sql string) string {
	for {
		s := strings.TrimLeft(sql, " \t\n\r")
		switch {
		case strings.HasPrefix(s, "--"):
			if idx := strings.Index(s, "\n"); idx >= 0 {
				sql = s[idx+1:]
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if idx := strings.Index(s, "*/"); idx >= 0 {
				sql = s[idx+2:]
				continue
			}
			return ""
		default:
			return s
		}
	}
}
