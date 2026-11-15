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

// Statements parses sql with sqlglot and converts its rune spans to Baleia's
// byte-offset spans. The parser owns lexical details such as strings,
// comments, dialect-specific batch separators and PL/SQL blocks.
func (d *spec) Statements(sql string) ([]Span, error) {
	if err := rejectDelimiter(sql, d.name == MySQL || d.name == MariaDB); err != nil {
		return nil, err
	}
	if strings.TrimSpace(sql) == "" {
		return nil, nil
	}
	dialectName, err := d.sqlglotDialect()
	if err != nil {
		return nil, err
	}
	source := sql
	lexemes, err := sqlglot.Tokenize(source, dialectName)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	// Parse each batch separately because a procedural statement can contain a
	// client batch separator that the SQL grammar intentionally does not treat
	// as a normal statement terminator.
	batchSeparators := findBatchSeparators(sql, d.name)

	out := make([]Span, 0)
	segmentStart := 0
	for _, separator := range append(batchSeparators, batchSeparator{start: len([]rune(source)), end: len([]rune(source))}) {
		segmentEnd := separator.start
		segment, err := parseSQLSegment(source, sql, segmentStart, segmentEnd, lexemes, dialectName, d.name == Oracle && segmentEnd < len([]rune(source)))
		if err != nil {
			return nil, err
		}
		out = append(out, segment...)
		if segmentEnd < len([]rune(source)) {
			segmentStart = separator.end
		}
	}
	return out, nil
}

type batchSeparator struct{ start, end int }

func findBatchSeparators(sql, dialectName string) []batchSeparator {
	if dialectName != TSQL && dialectName != Oracle {
		return nil
	}
	separators := make([]batchSeparator, 0)
	runeOffset := 0
	for _, line := range strings.SplitAfter(sql, "\n") {
		trimmed := strings.TrimRight(line, "\r\n")
		trimmed = strings.Trim(trimmed, " \t")
		isSeparator := (dialectName == TSQL && strings.EqualFold(trimmed, "GO")) || (dialectName == Oracle && trimmed == "/")
		if isSeparator {
			leading := len([]rune(line)) - len([]rune(strings.TrimLeft(line, " \t")))
			separators = append(separators, batchSeparator{start: runeOffset + leading, end: runeOffset + len([]rune(line))})
		}
		runeOffset += len([]rune(line))
	}
	return separators
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

func rejectDelimiter(sql string, mysqlFamily bool) error {
	if !mysqlFamily {
		return nil
	}
	for _, line := range strings.Split(sql, "\n") {
		fields := strings.FieldsFunc(line, unicode.IsSpace)
		if len(fields) > 0 && strings.EqualFold(fields[0], "DELIMITER") {
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

func parseSQLSegment(parseSource, originalSource string, segmentStart, segmentEnd int, lexemes []tokens.Token, dialectName dialects.DialectType, includeFinalTerminator bool) ([]Span, error) {
	runes := []rune(parseSource)
	if segmentStart >= segmentEnd {
		return nil, nil
	}
	segment := string(runes[segmentStart:segmentEnd])
	if strings.TrimSpace(segment) == "" {
		return nil, nil
	}
	exprs, err := sqlglot.Parse(segment, dialectName)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	semicolons := make([][2]int, 0)
	for _, token := range lexemes {
		if token.TokenType == tokens.SEMICOLON && token.Start >= segmentStart && token.End+1 <= segmentEnd {
			semicolons = append(semicolons, [2]int{token.Start - segmentStart, token.End + 1 - segmentStart})
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
		start, end := 0, len([]rune(segment))
		for index, semicolon := range semicolons {
			if semicolon[1] <= exprStart {
				start = semicolon[1]
				continue
			}
			if semicolon[0] >= exprEnd {
				end = semicolon[0]
				if includeFinalTerminator && index == len(semicolons)-1 {
					end = semicolon[1]
				}
				break
			}
		}
		start, end = trimRuneSpan(segment, start, end)
		if start >= end {
			continue
		}
		startByte := runeOffsetToByteOffset(originalSource, segmentStart+start)
		endByte := runeOffsetToByteOffset(originalSource, segmentStart+end)
		out = append(out, Span{Text: originalSource[startByte:endByte], Start: startByte, End: endByte})
	}
	return out, nil
}
