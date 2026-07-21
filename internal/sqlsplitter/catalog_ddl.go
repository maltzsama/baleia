package sqlsplitter

import "strings"

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