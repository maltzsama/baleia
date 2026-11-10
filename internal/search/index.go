package search

// MaxTermLength bounds a stored term, in runes; longer words are cut, and a
// lookup cuts its words the same way.
const MaxTermLength = 64

// fuzzyPrefixLength is how many leading runes a vocabulary term must share
// with a query word to be compared by the fuzzy fallback.
const fuzzyPrefixLength = 2

// Terms are what an index stores for a row with texts: every folded word and
// its singular forms, cut to MaxTermLength runes, each once.
func Terms(texts ...string) []string {
	out := []string{}
	seen := map[string]struct{}{}
	for _, text := range texts {
		for _, word := range Tokens(text) {
			for _, form := range singulars(word) {
				term := cut(form)
				if _, ok := seen[term]; ok || term == "" {
					continue
				}
				seen[term] = struct{}{}
				out = append(out, term)
			}
		}
	}
	return out
}

// Lookup is what an index must find for the query: rows with one of exact,
// or with a term that starts with one of prefixes. A query word finds its
// own forms exactly and longer words by prefix.
func (p Pattern) Lookup() (exact, prefixes []string) {
	exact, prefixes = []string{}, []string{}
	for i, token := range p.tokens {
		prefixes = append(prefixes, cut(token))
		for _, form := range p.forms[i] {
			exact = append(exact, cut(form))
		}
	}
	return unique(exact), unique(prefixes)
}

// FuzzyPrefixes are the leading runes of each query word: the part of the
// vocabulary the fuzzy fallback compares with IsNear.
func (p Pattern) FuzzyPrefixes() []string {
	out := []string{}
	for _, token := range p.tokens {
		runes := []rune(token)
		if len(runes) >= fuzzyPrefixLength {
			out = append(out, string(runes[:fuzzyPrefixLength]))
		}
	}
	return unique(out)
}

// IsNear reports whether a vocabulary term is close enough to a query word,
// by trigram similarity, to be a fuzzy candidate. It uses the lowest of the
// scorer's fuzzy thresholds: a candidate only proposes a row, the scorer
// decides whether it matches.
func (p Pattern) IsNear(term string) bool {
	grams := trigrams(term)
	for _, word := range p.words {
		if similarity(grams, word) >= fuzzyText {
			return true
		}
	}
	return false
}

// cut keeps the first MaxTermLength runes of term.
func cut(term string) string {
	runes := []rune(term)
	if len(runes) > MaxTermLength {
		return string(runes[:MaxTermLength])
	}
	return term
}
