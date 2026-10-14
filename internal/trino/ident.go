package trino

import (
	"errors"
	"regexp"

	"github.com/galileostudio/baleia/internal/dialect"
)

// QuoteIdent returns s as a Trino delimited identifier ("a""b" for a"b). The
// Trino dialect owns quoting; this is its shorthand for the client package.
func QuoteIdent(s string) (string, error) { return trinoDialect.QuoteIdent(s) }

// QualifiedName quotes each part and joins them with ".".
func QualifiedName(parts ...string) (string, error) { return trinoDialect.QualifiedName(parts...) }

// QuoteString returns s as a Trino string literal ('it”s').
func QuoteString(s string) string { return trinoDialect.QuoteString(s) }

var trinoDialect = dialect.MustGet(dialect.Trino)

var retentionRe = regexp.MustCompile(`^[1-9][0-9]{0,4}[dhm]$`)

// ValidateRetention accepts the retention_threshold values maintenance sends to
// Trino: 1 to 99999 followed by d, h or m.
func ValidateRetention(s string) error {
	if !retentionRe.MatchString(s) {
		return errors.New("retention must look like 7d, 12h or 30m")
	}
	return nil
}
