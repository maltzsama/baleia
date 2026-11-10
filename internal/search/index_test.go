package search

import (
	"slices"
	"strings"
	"testing"
)

func TestTerms(t *testing.T) {
	t.Parallel()
	got := Terms("sessões_web", "Sessões de navegação", "")
	for _, want := range []string{"sessoes", "sessao", "web", "de", "navegacao"} {
		if !slices.Contains(got, want) {
			t.Errorf("Terms = %q, want it to hold %q", got, want)
		}
	}
	seen := map[string]bool{}
	for _, term := range got {
		if seen[term] {
			t.Errorf("term %q appears twice in %q", term, got)
		}
		seen[term] = true
	}
	long := strings.Repeat("a", MaxTermLength+10)
	if got := Terms(long); len([]rune(got[0])) != MaxTermLength {
		t.Errorf("a long word is cut to %d runes: %d", MaxTermLength, len([]rune(got[0])))
	}
	if got := Terms(" _ "); len(got) != 0 {
		t.Errorf("no words, no terms: %q", got)
	}
}

func TestPattern_Lookup(t *testing.T) {
	t.Parallel()
	exact, prefixes := Compile("Categories ORD").Lookup()
	if !slices.Contains(exact, "category") || !slices.Contains(exact, "categories") || !slices.Contains(exact, "ord") {
		t.Errorf("exact = %q, want the words and their singular forms", exact)
	}
	if !slices.Equal(prefixes, []string{"categories", "ord"}) {
		t.Errorf("prefixes = %q, want the folded words", prefixes)
	}
	// What a query looks up is what the index stores for the same text.
	stored := Terms("product_category")
	if !slices.ContainsFunc(exact, func(term string) bool { return slices.Contains(stored, term) }) {
		t.Errorf("lookup %q misses the stored terms %q", exact, stored)
	}
}

func TestPattern_FuzzyPrefixesAndIsNear(t *testing.T) {
	t.Parallel()
	p := Compile("custmer x")
	if got := p.FuzzyPrefixes(); !slices.Equal(got, []string{"cu"}) {
		t.Errorf("FuzzyPrefixes = %q, want only the words long enough", got)
	}
	if !p.IsNear("customer") {
		t.Error("a misspelling is near the word")
	}
	if p.IsNear("cuisine") {
		t.Error("another word sharing the prefix is not near")
	}
}
