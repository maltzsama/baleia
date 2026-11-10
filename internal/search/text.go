// Package search is Baleia's portable text matching (#798): folding,
// tokenizing and scoring a query against stored names, comments and
// descriptions in Go, so search needs no database extension (ADR 0020).
package search

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// special folds letters that carry no combining mark to decompose.
var special = map[rune]string{
	'ß': "ss", 'ẞ': "ss", 'æ': "ae", 'Æ': "ae", 'œ': "oe", 'Œ': "oe",
	'ø': "o", 'Ø': "o", 'đ': "d", 'Đ': "d", 'ł': "l", 'Ł': "l", 'ı': "i",
}

// Fold lowercases s and removes its diacritics, so "Região" and "regiao"
// compare equal. It trims the ends and collapses inner whitespace.
func Fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range norm.NFD.String(strings.TrimSpace(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue
		case unicode.IsSpace(r):
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		if s, ok := special[r]; ok {
			b.WriteString(s)
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// Tokens splits s into folded words: at every rune that is not a letter or
// a digit (spaces, "_", ".", "-"), and at camelCase boundaries, so
// "customerLifetimeValue", "customer_lifetime_value" and
// "HTTPServer" give "customer lifetime value" and "http server".
func Tokens(s string) []string {
	runes := []rune(norm.NFC.String(s))
	out := []string{}
	start := -1
	flush := func(end int) {
		if start >= 0 && end > start {
			out = append(out, Fold(string(runes[start:end])))
		}
		start = -1
	}
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.Is(unicode.Mn, r) {
			flush(i)
			continue
		}
		if start >= 0 && isCaseBoundary(runes, i) {
			flush(i)
		}
		if start < 0 {
			start = i
		}
	}
	flush(len(runes))
	return out
}

// isCaseBoundary reports whether a camelCase word starts at runes[i]: an
// upper-case letter after a lower-case one ("lifeTime"), or the last upper-case
// letter of an acronym followed by a lower-case one ("HTTPServer").
func isCaseBoundary(runes []rune, i int) bool {
	r, prev := runes[i], runes[i-1]
	if !unicode.IsUpper(r) {
		return false
	}
	if unicode.IsLower(prev) {
		return true
	}
	next := i + 1
	return unicode.IsUpper(prev) && next < len(runes) && unicode.IsLower(runes[next])
}
