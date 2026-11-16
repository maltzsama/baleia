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
// comments, dialect-specific batch separators and PL/SQL blocks.
func (d *spec) Statements(sql string) ([]Span, error) {
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
	if err := rejectDelimiter(d.name, sql, lexemes); err != nil {
		return nil, err
	}

	// Parse each batch separately. T-SQL GO is handled by the fork's tokenizer,
	// which emits the GO line as a SEMICOLON token the alignment below already
	// splits on. Oracle's "/" line also ends a batch, but the fork's tokenizer
	// does not emit it as a token and the statement span it records drops the
	// ";" that a PL/SQL unit (BEGIN...END, CREATE PROCEDURE) needs before the
	// "/". Pre-splitting on "/" keeps that ";" with its unit.
	var separators []oracleTerminator
	if d.name == Oracle {
		separators = findOracleTerminators(sql, lexemes)
	}

	out := make([]Span, 0)
	segmentStart := 0
	for _, separator := range append(separators, oracleTerminator{start: len([]rune(source)), end: len([]rune(source))}) {
		segmentEnd := separator.start
		segment, err := parseSQLSegment(source, sql, segmentStart, segmentEnd, lexemes, dialectName, d.name == Oracle)
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

// oracleTerminator is an Oracle "/" line that ends a PL/SQL unit.
type oracleTerminator struct{ start, end int }

// findOracleTerminators returns the "/" lines of a script. The fork's tokenizer
// does not emit them as tokens, so the splitter locates them itself to keep a
// unit's trailing ";" with the unit instead of dropping it as a terminator. A
// "/" inside a string literal is a STRING token, not a separator, and is
// skipped.
func findOracleTerminators(sql string, lexemes []tokens.Token) []oracleTerminator {
	separators := make([]oracleTerminator, 0)
	runeOffset := 0
	for _, line := range strings.SplitAfter(sql, "\n") {
		if strings.Trim(strings.TrimRight(line, "\r\n"), " \t") != "/" {
			runeOffset += len([]rune(line))
			continue
		}
		leading := len([]rune(line)) - len([]rune(strings.TrimLeft(line, " \t")))
		slash := runeOffset + leading
		if !insideString(slash, lexemes) {
			separators = append(separators, oracleTerminator{start: slash, end: runeOffset + len([]rune(line))})
		}
		runeOffset += len([]rune(line))
	}
	return separators
}

// insideString reports whether a rune offset falls inside a string literal.
func insideString(pos int, lexemes []tokens.Token) bool {
	for _, token := range lexemes {
		if token.TokenType == tokens.STRING && pos >= token.Start && pos <= token.End {
			return true
		}
	}
	return false
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

func parseSQLSegment(parseSource, originalSource string, segmentStart, segmentEnd int, lexemes []tokens.Token, dialectName dialects.DialectType, oracle bool) ([]Span, error) {
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
				// Oracle: a PL/SQL unit (an anonymous block or a CREATE
				// PROCEDURE / FUNCTION / PACKAGE) keeps its trailing ";" — the
				// ";" is part of the unit, not its terminator. The parser's
				// span ends before it, so re-attach it here.
				if oracle && index == len(semicolons)-1 && (expr.Kind() == expressions.KindCommand || expr.Kind() == expressions.KindCreate) {
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
