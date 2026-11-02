// Package sqlref extracts the tables a statement reads and its equi-join
// predicates (B0, #530). It is pure: no engine round-trip and no error the
// caller must treat as fatal. It mirrors web/src/lib/workbench/sqlContext.ts
// and only understands what FROM/JOIN and ON need, never the whole grammar.
package sqlref

import "strings"

// Table is one table named in FROM/JOIN/UPDATE/INTO.
type Table struct {
	// Path is the name as written, 1 to 3 parts (table, schema.table,
	// catalog.schema.table).
	Path  []string
	Alias string
	// Kind is "read" for FROM/JOIN and "write" for UPDATE/INTO.
	Kind string
}

// Join is an equi-join predicate between two aliases: left = right.
type Join struct {
	LeftAlias   string
	LeftColumn  string
	RightAlias  string
	RightColumn string
}

// Reference is what one statement reads.
type Reference struct {
	Tables []Table
	Joins  []Join
}

// Extract returns the tables and equi-joins of one statement. An unparsable
// statement yields an empty Reference, never an error.
func Extract(sql string) Reference {
	tokens := tokenize(sql)
	tables := statementTables(tokens)
	return Reference{Tables: tables, Joins: joinPredicates(tokens, tables)}
}

type tokenKind int

const (
	tokWord tokenKind = iota
	tokQuoted
	tokString
	tokComment
	tokNumber
	tokPunct
)

type token struct {
	kind  tokenKind
	value string
}

var quoteClose = map[rune]rune{'"': '"', '`': '`', '[': ']'}

// tokenize is the small lexer of sqlContext.ts: words, quoted identifiers,
// strings, comments, numbers and single-character punctuation.
func tokenize(text string) []token {
	runes := []rune(text)
	out := make([]token, 0, len(runes)/4)
	for i := 0; i < len(runes); {
		ch := runes[i]
		switch {
		case isSpace(ch):
			i++
		case ch == '-' && i+1 < len(runes) && runes[i+1] == '-':
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			out = append(out, token{kind: tokComment})
		case ch == '/' && i+1 < len(runes) && runes[i+1] == '*':
			i += 2
			for i+1 < len(runes) && !(runes[i] == '*' && runes[i+1] == '/') {
				i++
			}
			if i+1 < len(runes) {
				i += 2
			} else {
				i = len(runes)
			}
			out = append(out, token{kind: tokComment})
		case ch == '\'':
			i++
			for i < len(runes) {
				if runes[i] == '\'' && i+1 < len(runes) && runes[i+1] == '\'' {
					i += 2
				} else if runes[i] == '\'' {
					i++
					break
				} else {
					i++
				}
			}
			out = append(out, token{kind: tokString})
		case quoteClose[ch] != 0:
			closer := quoteClose[ch]
			i++
			var value []rune
			for i < len(runes) {
				if runes[i] == closer && i+1 < len(runes) && runes[i+1] == closer {
					value = append(value, closer)
					i += 2
				} else if runes[i] == closer {
					i++
					break
				} else {
					value = append(value, runes[i])
					i++
				}
			}
			out = append(out, token{kind: tokQuoted, value: string(value)})
		case isWordStart(ch):
			start := i
			for i < len(runes) && isWordPart(runes[i]) {
				i++
			}
			out = append(out, token{kind: tokWord, value: string(runes[start:i])})
		case isDigit(ch):
			start := i
			for i < len(runes) && (isWordPart(runes[i]) || runes[i] == '.') {
				i++
			}
			out = append(out, token{kind: tokNumber, value: string(runes[start:i])})
		default:
			out = append(out, token{kind: tokPunct, value: string(ch)})
			i++
		}
	}
	return out
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\n' }
func isDigit(r rune) bool { return r >= '0' && r <= '9' }
func isWordStart(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}
func isWordPart(r rune) bool { return isWordStart(r) || isDigit(r) || r == '$' }

func isIdent(t token) bool { return t.kind == tokWord || t.kind == tokQuoted }
func upper(t token) string {
	if t.kind == tokWord {
		return strings.ToUpper(t.value)
	}
	return ""
}
func isPunct(t token, v string) bool { return t.kind == tokPunct && t.value == v }

// clause is what a keyword opens.
type clause int

const (
	clauseNone clause = iota
	clauseSelect
	clauseFrom
	clauseColumn
	clauseKeyword
)

var clauses = map[string]clause{
	"SELECT": clauseSelect, "FROM": clauseFrom, "JOIN": clauseFrom,
	"UPDATE": clauseFrom, "INTO": clauseFrom, "TABLE": clauseFrom,
	"WHERE": clauseColumn, "ON": clauseColumn, "HAVING": clauseColumn,
	"BY": clauseColumn, "SET": clauseColumn, "AND": clauseColumn,
	"OR": clauseColumn, "WHEN": clauseColumn, "THEN": clauseColumn,
	"ELSE": clauseColumn, "LIMIT": clauseKeyword, "OFFSET": clauseKeyword,
	"FETCH": clauseKeyword, "VALUES": clauseKeyword, "UNION": clauseNone,
	"INTERSECT": clauseNone, "EXCEPT": clauseNone, "WITH": clauseNone,
}

// notAlias are the words that continue the statement instead of naming a table.
var notAlias = map[string]bool{
	"WHERE": true, "JOIN": true, "ON": true, "USING": true, "LEFT": true,
	"RIGHT": true, "INNER": true, "OUTER": true, "FULL": true, "CROSS": true,
	"NATURAL": true, "LATERAL": true, "GROUP": true, "ORDER": true,
	"HAVING": true, "LIMIT": true, "OFFSET": true, "FETCH": true,
	"UNION": true, "INTERSECT": true, "EXCEPT": true, "WINDOW": true,
	"SET": true, "VALUES": true, "SELECT": true, "TABLESAMPLE": true,
	"FOR": true, "QUALIFY": true,
}

var joinCondition = map[string]bool{
	"ON": true, "AND": true, "OR": true, "WHEN": true, "THEN": true, "ELSE": true,
}

// statementTables reads FROM/JOIN/UPDATE/INTO at any depth and pairs each
// table with its alias.
func statementTables(tokens []token) []Table {
	var tables []Table
	inFrom := []bool{false}
	for i := 0; i < len(tokens); {
		t := tokens[i]
		word := upper(t)
		if isPunct(t, "(") {
			inFrom = append(inFrom, false)
			i++
			continue
		}
		if isPunct(t, ")") {
			if len(inFrom) > 1 {
				inFrom = inFrom[:len(inFrom)-1]
			}
			i++
			// A derived table: FROM (...) AS x.
			if inFrom[len(inFrom)-1] {
				if alias, next := readAlias(tokens, i); alias != "" {
					i = next
				}
			}
			continue
		}
		opens := word == "FROM" || word == "JOIN" || word == "UPDATE" || word == "INTO" ||
			(isPunct(t, ",") && inFrom[len(inFrom)-1])
		if word != "" && clauses[word] != clauseNone && !joinCondition[word] {
			inFrom[len(inFrom)-1] = clauses[word] == clauseFrom
		}
		if word == "TABLE" {
			inFrom[len(inFrom)-1] = false
		}
		if !opens {
			i++
			continue
		}
		i++
		path, next := readPath(tokens, i)
		if len(path) == 0 {
			continue
		}
		alias, after := readAlias(tokens, next)
		if alias == "" {
			alias = path[len(path)-1]
		}
		kind := "read"
		if word == "UPDATE" || word == "INTO" {
			kind = "write"
		}
		tables = append(tables, Table{Path: path, Alias: alias, Kind: kind})
		i = after
	}
	return tables
}

// readPath reads a.b.c and returns the parts and the next token index.
func readPath(tokens []token, i int) ([]string, int) {
	var path []string
	for i < len(tokens) && isIdent(tokens[i]) {
		path = append(path, tokens[i].value)
		if i+1 < len(tokens) && isPunct(tokens[i+1], ".") {
			i += 2
			continue
		}
		return path, i + 1
	}
	return path, i
}

// readAlias reads [AS] alias, rejecting the words that continue the statement.
func readAlias(tokens []token, i int) (string, int) {
	if i < len(tokens) && upper(tokens[i]) == "AS" {
		i++
	}
	if i >= len(tokens) || !isIdent(tokens[i]) || notAlias[upper(tokens[i])] {
		return "", i
	}
	return tokens[i].value, i + 1
}

// joinPredicates finds `alias.column = alias.column` pairs and resolves both
// aliases against the statement's tables.
func joinPredicates(tokens []token, tables []Table) []Join {
	byAlias := make(map[string]string, len(tables))
	for _, t := range tables {
		byAlias[strings.ToLower(t.Alias)] = t.Alias
	}
	var joins []Join
	seen := make(map[Join]bool)
	for i := 0; i+6 < len(tokens); i++ {
		la, lc, ok := qualified(tokens, i)
		if !ok || !isPunct(tokens[i+3], "=") {
			continue
		}
		ra, rc, ok := qualified(tokens, i+4)
		if !ok {
			continue
		}
		left, lok := byAlias[strings.ToLower(la)]
		right, rok := byAlias[strings.ToLower(ra)]
		if !lok || !rok || left == right {
			continue // a literal, an expression or a self-comparison
		}
		j := Join{LeftAlias: left, LeftColumn: lc, RightAlias: right, RightColumn: rc}
		if seen[j] {
			continue
		}
		seen[j] = true
		joins = append(joins, j)
		i += 6
	}
	return joins
}

// qualified reads alias.column at i.
func qualified(tokens []token, i int) (alias, column string, ok bool) {
	if i+2 >= len(tokens) || !isIdent(tokens[i]) || !isPunct(tokens[i+1], ".") || !isIdent(tokens[i+2]) {
		return "", "", false
	}
	return tokens[i].value, tokens[i+2].value, true
}
