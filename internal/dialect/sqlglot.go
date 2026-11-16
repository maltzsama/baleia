package dialect

import (
	"errors"
	"fmt"
	"sort"
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

	// Statement boundaries are the SEMICOLON tokens (";" and T-SQL GO) plus the
	// Oracle "/" lines. A "/" separator sits between two consecutive statement
	// spans, because the parser split there string- and comment-aware — so a "/"
	// inside a string or a block comment is never one.
	type boundary struct{ start, end int }
	boundaries := make([]boundary, 0, len(lexemes))
	for _, token := range lexemes {
		if token.TokenType == tokens.SEMICOLON {
			boundaries = append(boundaries, boundary{token.Start, token.End + 1})
		}
	}
	if d.name == Oracle {
		for i := 1; i < len(exprs); i++ {
			if exprs[i-1] == nil || exprs[i] == nil {
				continue
			}
			_, prevEnd, ok1 := exprs[i-1].Span()
			nextStart, _, ok2 := exprs[i].Span()
			if !ok1 || !ok2 {
				continue
			}
			if slash, ok := oracleSlash(runes, prevEnd, nextStart); ok {
				boundaries = append(boundaries, boundary{slash, slash + 1})
			}
		}
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].start < boundaries[j].start })

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
		// Oracle: a PL/SQL unit keeps its trailing ";" — the ";" is part of the
		// unit, not its terminator, and the parser's span ends before it.
		if d.name == Oracle && end < len(runes) && runes[end] == ';' && oracleKeepsTerminator(expr) {
			end++
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

// oracleKeepsTerminator reports whether an Oracle statement's trailing ";" is
// part of the statement rather than its terminator: an anonymous block (a
// Command) or a CREATE that declares a PL/SQL unit.
func oracleKeepsTerminator(expr expressions.Expression) bool {
	if expr.Kind() == expressions.KindCommand {
		return true
	}
	if expr.Kind() != expressions.KindCreate {
		return false
	}
	switch expr.Text("kind") {
	case "PROCEDURE", "FUNCTION", "PACKAGE", "TRIGGER", "TYPE":
		return true
	}
	return false
}

// oracleSlash returns the rune offset of a "/" alone on its line within
// [from, to), or false when there is none.
func oracleSlash(runes []rune, from, to int) (int, bool) {
	for i := from; i < to; i++ {
		if runes[i] != '/' {
			continue
		}
		lineStart := i
		for lineStart > from && runes[lineStart-1] != '\n' {
			lineStart--
		}
		lineEnd := i + 1
		for lineEnd < to && runes[lineEnd] != '\n' {
			lineEnd++
		}
		if strings.TrimSpace(string(runes[lineStart:lineEnd])) == "/" {
			return i, true
		}
	}
	return 0, false
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
