package search

import (
	"slices"
	"testing"
)

func TestRank(t *testing.T) {
	t.Parallel()
	kinds := []string{"table", "column", "saved_query"}
	candidates := []Candidate{
		{Kind: "saved_query", Key: "b", Score: 0.9},
		{Kind: "column", Key: "t.id", Score: 1.0},
		{Kind: "table", Key: "a", Score: 0.9},
		{Kind: "table", Key: "z", Score: 0.5},
		{Kind: "saved_query", Key: "a", Score: 0.9},
	}
	chosen, facets := Rank(candidates, kinds, 3)
	keys := make([]string, len(chosen))
	for i, c := range chosen {
		keys[i] = candidates[c].Kind + ":" + candidates[c].Key
	}
	if want := []string{"column:t.id", "table:a", "saved_query:a"}; !slices.Equal(keys, want) {
		t.Errorf("ranked = %v, want %v (best first, ties by kind then key)", keys, want)
	}
	want := []Facet{{"table", 2}, {"column", 1}, {"saved_query", 2}}
	if !slices.Equal(facets, want) {
		t.Errorf("facets = %v, want %v, counted before the cut and in kind order", facets, want)
	}

	if got, f := Rank(nil, kinds, 10); len(got) != 0 || len(f) != 0 {
		t.Errorf("no candidates: %v %v", got, f)
	}
	if got, _ := Rank(candidates, kinds, 0); len(got) != 0 {
		t.Errorf("limit 0 keeps %v", got)
	}
}
