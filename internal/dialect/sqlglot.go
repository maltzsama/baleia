package dialect

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	sqlglot "github.com/maltzsama/sqlglot-go"
	"github.com/maltzsama/sqlglot-go/dialects"
	"github.com/maltzsama/sqlglot-go/expressions"
	"github.com/maltzsama/sqlglot-go/tokens"
)

// ErrDelimiter is returned for the MySQL client's DELIMITER command.
var ErrDelimiter = errors.New("DELIMITER is a mysql client command, not SQL; end each statement with ';' instead")

var sqlglotDialects = map[string]string{
	Trino:      "trino",
	PostgreSQL: "postgres",
	MySQL:      "mysql",
	MariaDB:    "mariadb",
	TSQL:       "tsql",
	Oracle:     "oracle",
	SQLite:     "sqlite",
	ClickHouse: "clickhouse",
	FlightSQL:  "",
}

func (d *spec) sqlglotDialect() (dialects.DialectType, error) {
	name, ok := sqlglotDialects[d.name]
	if !ok {
		return nil, fmt.Errorf("%w: sqlglot dialect %q", ErrUnsupported, d.name)
	}
	return name, nil
}

// SqlglotDialect returns the sqlglot dialect name for d so external packages
// (reference extraction, classification) can drive the fork's optimizer and
// scope passes without depending on the unexported spec type.
func SqlglotDialect(d Dialect) (dialects.DialectType, error) {
	specDialect, ok := d.(*spec)
	if !ok {
		return nil, fmt.Errorf("%w: parser is unavailable for dialect %q", ErrUnsupported, d.Name())
	}
	return specDialect.sqlglotDialect()
}

// Statements parses sql with sqlglot and converts its rune spans to Baleia's
// byte-offset spans. The parser owns lexical details such as strings,
// comments, dialect-specific batch separators (T-SQL GO, Oracle "/") and
// PL/SQL blocks.
func (d *spec) Statements(sql string) ([]Span, error) {
	if strings.TrimSpace(sql) == "" {
		return nil, nil
	}
	dialectName, err := d.sqlglotDialect()
	if err != nil {
		return nil, err
	}
	lexemes, err := sqlglot.Tokenize(sql, dialectName)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := rejectDelimiter(d.name, sql, lexemes); err != nil {
		return nil, err
	}
	exprs, err := sqlglot.Parse(sql, dialectName)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	runes := []rune(sql)

	// Statement boundaries are the SEMICOLON tokens: a real ";", a T-SQL GO
	// line, and the Oracle "/" line (the fork tokenizes all three). The parser
	// splits on them string- and comment-aware, and each statement's span already
	// covers its attached comments and an Oracle unit's trailing ";".
	type boundary struct{ start, end int }
	boundaries := make([]boundary, 0, len(lexemes))
	for _, token := range lexemes {
		if token.TokenType == tokens.SEMICOLON {
			boundaries = append(boundaries, boundary{token.Start, token.End + 1})
		}
	}

	out := make([]Span, 0, len(exprs))
	for _, expr := range exprs {
		if expr == nil || expr.Kind() == expressions.KindSemicolon {
			continue
		}
		exprStart, exprEnd, ok := expr.Span()
		if !ok {
			return nil, fmt.Errorf("parse SQL: statement %s has no source span", expressions.ClassName(expr.Kind()))
		}
		start, end := 0, len(runes)
		for _, b := range boundaries {
			if b.end <= exprStart {
				start = b.end
				continue
			}
			if b.start >= exprEnd {
				end = b.start
				break
			}
		}
		start, end = trimRuneSpan(sql, start, end)
		if start >= end {
			continue
		}
		startByte := runeOffsetToByteOffset(sql, start)
		endByte := runeOffsetToByteOffset(sql, end)
		out = append(out, Span{Text: sql[startByte:endByte], Start: startByte, End: endByte})
	}
	return out, nil
}

// ParseStatement parses one already-split statement and is the shared AST
// entry point for policy and SQL-reference extraction.
func (d *spec) ParseStatement(sql string) (expressions.Expression, error) {
	if strings.TrimSpace(sql) == "" {
		return nil, fmt.Errorf("%w: empty statement", ErrInvalid)
	}
	dialectName, err := d.sqlglotDialect()
	if err != nil {
		return nil, err
	}
	expr, err := sqlglot.ParseOne(sql, dialectName)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return expr, nil
}

// ParseStatement parses one statement with a registered Baleia dialect. It is
// intentionally a package function so Dialect's SQL-generation interface does
// not force test doubles or external dialect implementations to depend on the
// parser.
func ParseStatement(d Dialect, sql string) (expressions.Expression, error) {
	specDialect, ok := d.(*spec)
	if !ok {
		return nil, fmt.Errorf("%w: parser is unavailable for dialect %q", ErrUnsupported, d.Name())
	}
	return specDialect.ParseStatement(sql)
}

func (d *spec) SplitStatements(sql string) ([]string, error) {
	spans, err := d.Statements(sql)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(spans))
	for i, span := range spans {
		out[i] = span.Text
	}
	return out, nil
}

// rejectDelimiter returns ErrDelimiter for a MySQL/MariaDB client DELIMITER
// command — the DELIMITER keyword at the start of a line, outside any string
// or comment. A DELIMITER word inside a string is a STRING token, not a
// DELIMITER token, so it is not a command.
func rejectDelimiter(name, sql string, lexemes []tokens.Token) error {
	if name != MySQL && name != MariaDB {
		return nil
	}
	for _, token := range lexemes {
		if !strings.EqualFold(token.Text, "DELIMITER") {
			continue
		}
		start := runeOffsetToByteOffset(sql, token.Start)
		lineStart := strings.LastIndexByte(sql[:start], '\n') + 1
		if strings.TrimSpace(sql[lineStart:start]) == "" {
			return ErrDelimiter
		}
	}
	return nil
}

func trimRuneSpan(sql string, start, end int) (int, int) {
	runes := []rune(sql)
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}
	for start < end && unicode.IsSpace(runes[start]) {
		start++
	}
	for end > start && unicode.IsSpace(runes[end-1]) {
		end--
	}
	return start, end
}

func runeOffsetToByteOffset(s string, offset int) int {
	if offset <= 0 {
		return 0
	}
	runeOffset := 0
	for byteOffset := range s {
		if runeOffset == offset {
			return byteOffset
		}
		runeOffset++
	}
	return len(s)
}
