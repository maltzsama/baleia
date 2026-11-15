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
//
// The normal path parses the statement and inspects the command node. A
// narrow token fallback is retained only for malformed SQL where no AST can
// exist; this keeps a rejected prefix from being hidden behind a later parse
// error while preserving the parser as the source of truth for valid SQL.
func IsCatalogDDL(sql string) bool {
	return isCatalogDDLWithDialect(genericDialect, sql)
}

// statementForbidden reports whether one statement must be rejected by the
// SQL editor:
//   - catalog DDL (CREATE/DROP CATALOG, handled by IsCatalogDDL);
//   - EXECUTE IMMEDIATE whose string literal body is catalog DDL (the parser
//     would otherwise execute the mismatched grammar without us touching it);
//   - SET SESSION AUTHORIZATION, which would let a user impersonate an admin
//     target (e.g. baleia_admin) the guard grants to.
//
// The Trino catalog guard is the real control; this is UX and defense in depth.
func statementForbidden(sql string) bool {
	return statementForbiddenWithDialect(genericDialect, sql)
}

var genericDialect = dialect.NewRegistry().MustGet(dialect.FlightSQL)

func statementForbiddenWithDialect(d dialect.Dialect, sql string) bool {
	if strings.TrimSpace(sql) == "" {
		return false
	}
	expr, err := dialect.ParseStatement(d, sql)
	if err != nil || expr == nil {
		return fallbackForbidden(sql)
	}
	if isCatalogDDLExpression(expr) {
		return true
	}
	switch commandRoot(expr) {
	case "SET":
		return strings.HasPrefix(strings.ToUpper(collapseWhitespace(commandBody(expr))), "SESSION AUTHORIZATION")
	case "EXECUTE":
		body := commandBody(expr)
		if !strings.HasPrefix(strings.ToUpper(body), "IMMEDIATE") {
			return false
		}
		literal := extractExecuteImmediateLiteral(body)
		return isCatalogDDLWithDialect(d, literal) || isCatalogDDLWords(literal)
	default:
		return false
	}
}

func isCatalogDDLWithDialect(d dialect.Dialect, sql string) bool {
	if strings.TrimSpace(sql) == "" {
		return false
	}
	expr, err := dialect.ParseStatement(d, sql)
	return (err == nil && expr != nil && isCatalogDDLExpression(expr)) || isCatalogDDLWords(sql)
}

func fallbackForbidden(sql string) bool {
	if isCatalogDDLWords(sql) {
		return true
	}
	clean := strings.ToUpper(collapseWhitespace(trimLeadingComments(sql)))
	if strings.HasPrefix(clean, "SET SESSION AUTHORIZATION") {
		return true
	}
	if strings.HasPrefix(clean, "EXECUTE IMMEDIATE") {
		return isCatalogDDLWords(extractExecuteImmediateLiteral(clean))
	}
	return false
}

func isCatalogDDLWords(sql string) bool {
	fields := strings.Fields(collapseWhitespace(trimLeadingComments(sql)))
	return len(fields) >= 2 && (strings.EqualFold(fields[0], "CREATE") || strings.EqualFold(fields[0], "DROP")) && strings.EqualFold(fields[1], "CATALOG")
}

func isCatalogDDLExpression(expr expressions.Expression) bool {
	root := commandRoot(expr)
	if root != "CREATE" && root != "DROP" {
		return false
	}
	body := strings.Fields(commandBody(expr))
	return len(body) > 0 && strings.EqualFold(body[0], "CATALOG")
}

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

func commandBody(expr expressions.Expression) string {
	span, ok := expr.SpanText()
	if !ok {
		return ""
	}
	first := strings.IndexFunc(span, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	if first < 0 {
		return ""
	}
	return strings.TrimSpace(span[first:])
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

// collapseWhitespace replaces every run of whitespace with a single space
// and strips leading/trailing whitespace.
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
// (/* ... */) before the first token. This handles the case where a user
// tries to smuggle a DDL past the prefix check by prepending a comment.
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
