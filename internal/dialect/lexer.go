package dialect

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// lexRules are the lexical rules a dialect's splitter follows.
type lexRules struct {
	identQuotes      string // characters that open quoted identifiers, closed by the same character
	brackets         bool   // [ident] (T-SQL, SQLite)
	backslashStrings bool   // '\'' escapes inside strings (MySQL, ClickHouse)
	doubleStrings    bool   // "..." is a string with the string rules (MySQL)
	hashComments     bool   // # line comments (MySQL)
	dollarQuotes     bool   // $tag$...$tag$ (PostgreSQL)
	escapeStrings    bool   // E'...' with backslash escapes (PostgreSQL)
	qQuotes          bool   // q'[...]' (Oracle)
	nestedComments   bool   // /* /* */ */ (PostgreSQL, SQLite does not)
	goSeparator      bool   // a line holding only GO ends a batch (T-SQL)
	delimiterError   bool   // DELIMITER is client syntax: reject it (MySQL)
	plsqlBlocks      bool   // PL/SQL blocks end at a line holding only "/" (Oracle)
}

// ErrDelimiter is returned for the MySQL client's DELIMITER command.
var ErrDelimiter = errors.New("DELIMITER is a mysql client command, not SQL; end each statement with ';' instead")

// split cuts sql into statements at top-level ';' (and the dialect's batch
// separators), outside strings, quoted identifiers and comments.
func (r lexRules) split(sql string) ([]Span, error) {
	var out []Span
	start := 0
	emit := func(end int) {
		text := sql[start:end]
		lead := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
		text = strings.TrimSpace(text)
		if text != "" && !onlyComments(text, r) {
			out = append(out, Span{Text: text, Start: start + lead, End: start + lead + len(text)})
		}
	}
	plsql := false // inside a PL/SQL block: only "/" on its own line ends it
	i := 0
	for i < len(sql) {
		if atLineStart(sql, i) {
			line, next := lineAt(sql, i)
			word := strings.TrimSpace(line)
			switch {
			case r.goSeparator && strings.EqualFold(word, "GO"):
				emit(i)
				start, i = next, next
				continue
			case r.plsqlBlocks && plsql && word == "/":
				emit(i)
				start, i, plsql = next, next, false
				continue
			case r.delimiterError && hasWordPrefix(word, "DELIMITER"):
				return nil, ErrDelimiter
			}
			if r.plsqlBlocks && !plsql && strings.TrimSpace(sql[start:i]) == "" && startsPLSQL(sql[i:]) {
				plsql = true
			}
		}
		c := sql[i]
		switch {
		case c == '-' && strings.HasPrefix(sql[i:], "--"),
			r.hashComments && c == '#':
			i = skipLine(sql, i)
		case c == '/' && strings.HasPrefix(sql[i:], "/*"):
			var err error
			if i, err = skipBlockComment(sql, i, r.nestedComments); err != nil {
				return nil, err
			}
		case r.qQuotes && (c == 'q' || c == 'Q') && i+2 < len(sql) && sql[i+1] == '\'' && !identChar(prev(sql, i)):
			var err error
			if i, err = skipQQuote(sql, i); err != nil {
				return nil, err
			}
		case r.escapeStrings && (c == 'e' || c == 'E') && i+1 < len(sql) && sql[i+1] == '\'' && !identChar(prev(sql, i)):
			var err error
			if i, err = skipQuoted(sql, i+1, '\'', true); err != nil {
				return nil, err
			}
		case c == '\'':
			var err error
			if i, err = skipQuoted(sql, i, '\'', r.backslashStrings); err != nil {
				return nil, err
			}
		case c == '"' && r.doubleStrings:
			var err error
			if i, err = skipQuoted(sql, i, '"', r.backslashStrings); err != nil {
				return nil, err
			}
		case strings.IndexByte(r.identQuotes, c) >= 0:
			var err error
			if i, err = skipQuoted(sql, i, c, false); err != nil {
				return nil, err
			}
		case r.brackets && c == '[':
			end := strings.IndexByte(sql[i+1:], ']')
			for end >= 0 && i+1+end+1 < len(sql) && sql[i+1+end+1] == ']' { // ]] escapes ]
				next := strings.IndexByte(sql[i+1+end+2:], ']')
				if next < 0 {
					end = -1
					break
				}
				end += 2 + next
			}
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated [identifier]", ErrInvalid)
			}
			i += end + 2
		case r.dollarQuotes && c == '$':
			tag, ok := dollarTag(sql[i:])
			if !ok {
				i++
				continue
			}
			end := strings.Index(sql[i+len(tag):], tag)
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated %s quoted body", ErrInvalid, tag)
			}
			i += len(tag) + end + len(tag)
		case c == ';' && !plsql:
			emit(i)
			i++
			start = i
		default:
			i++
		}
	}
	emit(len(sql))
	return out, nil
}

func prev(s string, i int) byte {
	if i == 0 {
		return ' '
	}
	return s[i-1]
}

func identChar(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func atLineStart(s string, i int) bool { return i == 0 || s[i-1] == '\n' }

func lineAt(s string, i int) (line string, next int) {
	end := strings.IndexByte(s[i:], '\n')
	if end < 0 {
		return s[i:], len(s)
	}
	return s[i : i+end], i + end + 1
}

func hasWordPrefix(s, word string) bool {
	return len(s) >= len(word) && strings.EqualFold(s[:len(word)], word) &&
		(len(s) == len(word) || unicode.IsSpace(rune(s[len(word)])))
}

func skipLine(s string, i int) int {
	end := strings.IndexByte(s[i:], '\n')
	if end < 0 {
		return len(s)
	}
	return i + end + 1
}

func skipBlockComment(s string, i int, nested bool) (int, error) {
	depth := 0
	for j := i; j+1 < len(s); j++ {
		switch {
		case s[j] == '/' && s[j+1] == '*':
			if nested || depth == 0 {
				depth++
			}
			j++
		case s[j] == '*' && s[j+1] == '/':
			depth--
			j++
			if depth == 0 {
				return j + 1, nil
			}
		}
	}
	return 0, fmt.Errorf("%w: unterminated /* comment", ErrInvalid)
}

// skipQuoted skips a quoted run opened at s[i]; a doubled quote is an
// escaped quote, and so is \<quote> when backslash is set.
func skipQuoted(s string, i int, q byte, backslash bool) (int, error) {
	for j := i + 1; j < len(s); j++ {
		switch {
		case backslash && s[j] == '\\':
			j++
		case s[j] == q:
			if j+1 < len(s) && s[j+1] == q {
				j++
				continue
			}
			return j + 1, nil
		}
	}
	return 0, fmt.Errorf("%w: unterminated %c quote", ErrInvalid, q)
}

// skipQQuote skips Oracle's q'<d>...<d>' literal.
func skipQQuote(s string, i int) (int, error) {
	open := s[i+2]
	closeBy := map[byte]byte{'[': ']', '{': '}', '(': ')', '<': '>'}
	cl, ok := closeBy[open]
	if !ok {
		cl = open
	}
	end := strings.Index(s[i+3:], string(cl)+"'")
	if end < 0 {
		return 0, fmt.Errorf("%w: unterminated q'%c literal", ErrInvalid, open)
	}
	return i + 3 + end + 2, nil
}

// dollarTag returns $tag$ at the start of s.
func dollarTag(s string) (string, bool) {
	for j := 1; j < len(s); j++ {
		c := s[j]
		if c == '$' {
			return s[:j+1], true
		}
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || j > 1 && c >= '0' && c <= '9') {
			return "", false
		}
	}
	return "", false
}

// startsPLSQL reports whether s opens an Oracle PL/SQL unit, whose own ';'
// do not end the statement.
func startsPLSQL(s string) bool {
	f := strings.Fields(strings.ToUpper(trimLeadingComments(s)))
	if len(f) == 0 {
		return false
	}
	switch f[0] {
	case "BEGIN", "DECLARE":
		return true
	case "CREATE":
		f = f[1:]
		if len(f) >= 2 && f[0] == "OR" && f[1] == "REPLACE" {
			f = f[2:]
		}
		for len(f) > 0 && (f[0] == "EDITIONABLE" || f[0] == "NONEDITIONABLE") {
			f = f[1:]
		}
		if len(f) > 0 {
			switch f[0] {
			case "PROCEDURE", "FUNCTION", "PACKAGE", "TRIGGER", "TYPE", "LIBRARY":
				return true
			}
		}
	}
	return false
}

// onlyComments reports whether a statement is nothing but comments.
func onlyComments(s string, r lexRules) bool {
	t := trimLeadingComments(s)
	if r.hashComments {
		for strings.HasPrefix(t, "#") {
			t = trimLeadingComments(t[skipLine(t, 0):])
		}
	}
	return strings.TrimSpace(t) == ""
}

// trimLeadingComments strips leading -- and /* */ comments and whitespace.
func trimLeadingComments(sql string) string {
	for {
		s := strings.TrimLeftFunc(sql, unicode.IsSpace)
		switch {
		case strings.HasPrefix(s, "--"):
			sql = s[skipLine(s, 0):]
		case strings.HasPrefix(s, "/*"):
			end := strings.Index(s, "*/")
			if end < 0 {
				return ""
			}
			sql = s[end+2:]
		default:
			return s
		}
	}
}
