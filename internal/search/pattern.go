package search

import (
	"slices"
	"strings"
)

// Match tiers, best first. A score is a tier plus at most tieBreak of
// trigram similarity, so a better tier always ranks above a worse one and
// closer texts rank first inside a tier.
const (
	TierExact       = 1.0  // the whole text equals the query
	TierPrefix      = 0.9  // the text starts with the query
	TierTokens      = 0.8  // every query word is a word of the text
	TierTokenPrefix = 0.75 // every query word starts a word of the text
	TierContains    = 0.7  // the text contains the query
	tierPartial     = 0.6  // ceiling of the partial-words tier
	tierFuzzy       = 0.5  // ceiling of the fuzzy tier

	tieBreak = 0.04

	// fuzzyText and fuzzyWord are the trigram similarities from which the
	// whole text, or each query word against its closest word, is a fuzzy
	// match.
	fuzzyText = 0.3
	fuzzyWord = 0.5
)

// Field is one searchable text of a result and its weight: 1 for names, less
// for prose such as comments and descriptions.
type Field struct {
	Text   string
	Weight float64
}

// Pattern is a compiled query.
type Pattern struct {
	text   string
	tokens []string
	forms  [][]string // singulars of each token
	grams  map[string]struct{}
	words  []map[string]struct{}
}

// Compile folds and tokenizes q once for scoring many texts.
func Compile(q string) Pattern {
	p := Pattern{text: Fold(q), tokens: unique(Tokens(q))}
	p.grams = trigrams(p.text)
	p.forms = make([][]string, len(p.tokens))
	p.words = make([]map[string]struct{}, len(p.tokens))
	for i, token := range p.tokens {
		p.forms[i] = singulars(token)
		p.words[i] = trigrams(token)
	}
	return p
}

// IsEmpty reports whether the query has nothing to match.
func (p Pattern) IsEmpty() bool { return p.text == "" }

// Score is how well text matches the query, in (0, TierExact+tieBreak], or
// 0 when it does not match. An empty query matches nothing.
func (p Pattern) Score(text string) float64 {
	if p.IsEmpty() {
		return 0
	}
	folded := Fold(text)
	if folded == "" {
		return 0
	}
	closeness := similarity(trigrams(folded), p.grams)
	tier := p.tier(folded, Tokens(text), closeness)
	if tier == 0 {
		return 0
	}
	return tier + tieBreak*closeness
}

// Best is the best weighted score of fields, or 0 when none matches.
func (p Pattern) Best(fields ...Field) float64 {
	best := 0.0
	for _, f := range fields {
		if s := p.Score(f.Text) * f.Weight; s > best {
			best = s
		}
	}
	return best
}

func (p Pattern) tier(folded string, words []string, closeness float64) float64 {
	switch {
	case folded == p.text:
		return TierExact
	case strings.HasPrefix(folded, p.text):
		return TierPrefix
	}
	forms := make([][]string, len(words))
	for i, w := range words {
		forms[i] = singulars(w)
	}
	exact, prefix := 0, 0
	for i, token := range p.tokens {
		switch wordMatch(token, p.forms[i], words, forms) {
		case matchExact:
			exact++
			prefix++
		case matchPrefix:
			prefix++
		}
	}
	all := len(p.tokens)
	switch {
	case all > 0 && exact == all:
		return TierTokens
	case all > 0 && prefix == all:
		return TierTokenPrefix
	case strings.Contains(folded, p.text):
		return TierContains
	case all > 1 && 2*prefix >= all:
		return tierPartial * float64(prefix) / float64(all)
	}
	if fuzzy := p.fuzzy(words, closeness); fuzzy > 0 {
		return tierFuzzy * fuzzy
	}
	return 0
}

// fuzzy is the trigram closeness of a misspelled query: the whole text's when
// it reaches fuzzyText, else the mean of each query word's closest word when
// every word reaches fuzzyWord; 0 otherwise.
func (p Pattern) fuzzy(words []string, closeness float64) float64 {
	if closeness >= fuzzyText {
		return closeness
	}
	if len(p.words) == 0 || len(words) == 0 {
		return 0
	}
	grams := make([]map[string]struct{}, len(words))
	for i, w := range words {
		grams[i] = trigrams(w)
	}
	total := 0.0
	for _, q := range p.words {
		best := 0.0
		for _, w := range grams {
			if s := similarity(q, w); s > best {
				best = s
			}
		}
		if best < fuzzyWord {
			return 0
		}
		total += best
	}
	return total / float64(len(p.words))
}

type wordResult int

const (
	matchNone wordResult = iota
	matchPrefix
	matchExact
)

// wordMatch is how token, with its singular forms, matches the best of
// words: the same word or a shared singular is exact, a word that starts with
// token a prefix.
func wordMatch(token string, tokenForms, words []string, wordForms [][]string) wordResult {
	out := matchNone
	for i, w := range words {
		switch {
		case w == token || sharesForm(tokenForms, wordForms[i]):
			return matchExact
		case strings.HasPrefix(w, token):
			out = matchPrefix
		}
	}
	return out
}

func sharesForm(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok || v == "" {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
