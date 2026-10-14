// Package policy decides what users may do. It never asks what an engine can
// do: capabilities describe engines, policies describe users, and the editor
// gate below holds on every engine (docs/adr, #14).
package policy

import (
	"errors"
	"strings"

	"github.com/galileostudio/baleia/internal/dialect"
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
		if statementForbidden(s) {
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
		if statementForbidden(s) {
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
// The check is a normalized prefix match, not a parser. It strips leading
// whitespace/whitespace-comments (a leading -- comment shouldn't bypass it)
// but does not try to detect catalog DDL embedded inside a larger statement.
// Trino DDL for catalog management is always top-level.
//
// Robustness against common offensives:
//   - "  create  catalog " with extra spaces — handled by CollapseWhitespace.
//   - "CREATE CATALOG" with random case — handled by lowercase.
//   - "-- hi\nCREATE CATALOG ..."    — handled by TrimLeadingComments.
//   - "/* */CREATE CATALOG ..."      — handled by TrimLeadingComments.
//
// A multi-statement body that smuggles CREATE CATALOG after a SELECT will
// pass this check on the SELECT — but the splitter separates the statements
// before validating, and the validator is called per statement. So a
// smuggled CREATE CATALOG is caught by the validator at its own statement.
func IsCatalogDDL(sql string) bool {
	if sql == "" {
		return false
	}
	s := trimLeadingComments(sql)
	s = collapseWhitespace(s)
	s = strings.ToLower(s)
	return strings.HasPrefix(s, "create catalog ") || s == "create catalog" ||
		strings.HasPrefix(s, "drop catalog ") || s == "drop catalog"
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
	if sql == "" {
		return false
	}
	s := trimLeadingComments(sql)
	lower := strings.ToLower(collapseWhitespace(s))

	if lower == "set session authorization" || strings.HasPrefix(lower, "set session authorization ") {
		return true
	}
	if IsCatalogDDL(s) {
		return true
	}
	if strings.HasPrefix(lower, "execute immediate") {
		body := extractExecuteImmediateLiteral(s)
		if body != "" && IsCatalogDDL(body) {
			return true
		}
	}
	return false
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
