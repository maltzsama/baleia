package trino

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// ValidatePartitionFilter accepts the WHERE clause of a table maintenance
// optimize (#500): comparisons of a column with literals, joined by AND/OR,
// with parentheses, IS [NOT] NULL, [NOT] IN (literals) and [NOT] BETWEEN.
// It rejects everything else, function calls and subqueries included: the
// statement runs as the engine's identity, so the filter must not be able to
// read other tables.
func ValidatePartitionFilter(s string) error {
	toks, err := tokenize(s)
	if err != nil {
		return err
	}
	if len(toks) == 0 {
		return errors.New("filter is empty")
	}
	p := &filterParser{toks: toks}
	if err := p.or(); err != nil {
		return err
	}
	if p.i != len(p.toks) {
		return fmt.Errorf("unexpected %q", p.toks[p.i].text)
	}
	return nil
}

type tokenKind int

const (
	tIdent tokenKind = iota
	tLiteral
	tOp
	tLParen
	tRParen
	tComma
	tKeyword
)

type token struct {
	kind tokenKind
	text string
}

var filterKeywords = map[string]bool{
	"AND": true, "OR": true, "NOT": true, "IS": true, "NULL": true, "IN": true,
	"BETWEEN": true, "TRUE": true, "FALSE": true,
}

// typedLiterals prefix a string literal: DATE '2026-01-01'.
var typedLiterals = map[string]bool{"DATE": true, "TIMESTAMP": true, "TIME": true}

func tokenize(s string) ([]token, error) {
	var out []token
	r := []rune(s)
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '(':
			out = append(out, token{tLParen, "("})
			i++
		case c == ')':
			out = append(out, token{tRParen, ")"})
			i++
		case c == ',':
			out = append(out, token{tComma, ","})
			i++
		case c == '\'':
			j := i + 1
			for {
				if j >= len(r) {
					return nil, errors.New("unterminated string literal")
				}
				if r[j] == '\'' {
					if j+1 < len(r) && r[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			out = append(out, token{tLiteral, string(r[i : j+1])})
			i = j + 1
		case c == '"':
			j := i + 1
			for j < len(r) && r[j] != '"' {
				j++
			}
			if j >= len(r) {
				return nil, errors.New("unterminated quoted identifier")
			}
			out = append(out, token{tIdent, string(r[i : j+1])})
			i = j + 1
		case c == '=' || c == '<' || c == '>' || c == '!':
			j := i + 1
			if j < len(r) && (r[j] == '=' || (c == '<' && r[j] == '>')) {
				j++
			}
			op := string(r[i:j])
			if op == "!" {
				return nil, errors.New("unexpected '!'")
			}
			out = append(out, token{tOp, op})
			i = j
		case unicode.IsDigit(c) || (c == '-' && i+1 < len(r) && unicode.IsDigit(r[i+1])):
			j := i + 1
			for j < len(r) && (unicode.IsDigit(r[j]) || r[j] == '.') {
				j++
			}
			out = append(out, token{tLiteral, string(r[i:j])})
			i = j
		case unicode.IsLetter(c) || c == '_':
			j := i + 1
			for j < len(r) && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j]) || r[j] == '_') {
				j++
			}
			word := string(r[i:j])
			upper := strings.ToUpper(word)
			switch {
			case typedLiterals[upper] && followedByQuote(r, j):
				// DATE '2026-01-01'; a column named date is an identifier.
				out = append(out, token{tKeyword, upper})
			case filterKeywords[upper]:
				out = append(out, token{tKeyword, upper})
			default:
				out = append(out, token{tIdent, word})
			}
			i = j
		default:
			return nil, fmt.Errorf("unexpected %q", string(c))
		}
	}
	return out, nil
}

func followedByQuote(r []rune, i int) bool {
	for i < len(r) && unicode.IsSpace(r[i]) {
		i++
	}
	return i < len(r) && r[i] == '\''
}

type filterParser struct {
	toks []token
	i    int
}

func (p *filterParser) peek() (token, bool) {
	if p.i >= len(p.toks) {
		return token{}, false
	}
	return p.toks[p.i], true
}

func (p *filterParser) keyword(k string) bool {
	if t, ok := p.peek(); ok && t.kind == tKeyword && t.text == k {
		p.i++
		return true
	}
	return false
}

func (p *filterParser) or() error {
	if err := p.and(); err != nil {
		return err
	}
	for p.keyword("OR") {
		if err := p.and(); err != nil {
			return err
		}
	}
	return nil
}

func (p *filterParser) and() error {
	if err := p.not(); err != nil {
		return err
	}
	for p.keyword("AND") {
		if err := p.not(); err != nil {
			return err
		}
	}
	return nil
}

func (p *filterParser) not() error {
	p.keyword("NOT")
	t, ok := p.peek()
	if ok && t.kind == tLParen {
		p.i++
		if err := p.or(); err != nil {
			return err
		}
		if t, ok := p.peek(); !ok || t.kind != tRParen {
			return errors.New("missing )")
		}
		p.i++
		return nil
	}
	return p.predicate()
}

// predicate is <column> followed by a comparison, IS [NOT] NULL, [NOT] IN or
// [NOT] BETWEEN.
func (p *filterParser) predicate() error {
	t, ok := p.peek()
	if !ok || t.kind != tIdent {
		return errors.New("a condition must start with a column")
	}
	p.i++
	if t, ok := p.peek(); ok && t.kind == tLParen {
		return errors.New("function calls are not allowed")
	}
	if p.keyword("IS") {
		p.keyword("NOT")
		if !p.keyword("NULL") {
			return errors.New("IS must be followed by [NOT] NULL")
		}
		return nil
	}
	negated := p.keyword("NOT")
	switch {
	case p.keyword("IN"):
		return p.list()
	case p.keyword("BETWEEN"):
		if err := p.literal(); err != nil {
			return err
		}
		if !p.keyword("AND") {
			return errors.New("BETWEEN needs AND")
		}
		return p.literal()
	case negated:
		return errors.New("NOT must be followed by IN or BETWEEN here")
	}
	op, ok := p.peek()
	if !ok || op.kind != tOp {
		return errors.New("expected a comparison")
	}
	p.i++
	return p.literal()
}

func (p *filterParser) list() error {
	if t, ok := p.peek(); !ok || t.kind != tLParen {
		return errors.New("IN needs a list")
	}
	p.i++
	for {
		if err := p.literal(); err != nil {
			return err
		}
		t, ok := p.peek()
		if !ok {
			return errors.New("missing )")
		}
		p.i++
		if t.kind == tRParen {
			return nil
		}
		if t.kind != tComma {
			return fmt.Errorf("unexpected %q in list", t.text)
		}
	}
}

func (p *filterParser) literal() error {
	t, ok := p.peek()
	if !ok {
		return errors.New("expected a literal")
	}
	switch {
	case t.kind == tLiteral:
		p.i++
		return nil
	case t.kind == tKeyword && (t.text == "TRUE" || t.text == "FALSE" || t.text == "NULL"):
		p.i++
		return nil
	case t.kind == tKeyword && typedLiterals[t.text]:
		p.i++
		if n, ok := p.peek(); !ok || n.kind != tLiteral || !strings.HasPrefix(n.text, "'") {
			return fmt.Errorf("%s needs a string literal", t.text)
		}
		p.i++
		return nil
	}
	return fmt.Errorf("expected a literal, got %q", t.text)
}
