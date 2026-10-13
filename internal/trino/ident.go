package trino

import (
	"errors"
	"regexp"
	"strings"
)

// QuoteIdent returns s as a Trino delimited identifier ("a""b" for a"b). Input
// is quoted, never sanitised: every byte except NUL survives. Empty names and
// names containing NUL are rejected.
func QuoteIdent(s string) (string, error) {
	if s == "" {
		return "", errors.New("empty identifier")
	}
	if strings.ContainsRune(s, 0) {
		return "", errors.New("identifier contains a NUL byte")
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`, nil
}

// QualifiedName quotes each part and joins them with ".".
func QualifiedName(parts ...string) (string, error) {
	quoted := make([]string, len(parts))
	for i, p := range parts {
		q, err := QuoteIdent(p)
		if err != nil {
			return "", err
		}
		quoted[i] = q
	}
	return strings.Join(quoted, "."), nil
}

// QuoteString returns s as a Trino string literal ('it''s').
func QuoteString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

var retentionRe = regexp.MustCompile(`^[1-9][0-9]{0,4}[dhm]$`)

// ValidateRetention accepts the retention_threshold values maintenance sends to
// Trino: 1 to 99999 followed by d, h or m.
func ValidateRetention(s string) error {
	if !retentionRe.MatchString(s) {
		return errors.New("retention must look like 7d, 12h or 30m")
	}
	return nil
}
