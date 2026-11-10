package search

import (
	"slices"
	"sort"
)

// Candidate is one result of one surface, for ranking across surfaces.
type Candidate struct {
	// Kind is what the result is; Key tells results of a kind apart.
	Kind  string
	Key   string
	Score float64
}

// Facet is how many candidates of a kind a search considered.
type Facet struct {
	Kind  string
	Count int
}

// Rank orders candidates best first and cuts them to limit. Scores of
// different surfaces are comparable (they come from the same matcher), so
// they are merged as they are: ties go to the kind that comes first in
// kindOrder, then to the smaller key. It returns the indexes of the chosen
// candidates, in rank order, and a facet for each kind of kindOrder that has
// candidates, counted before the cut.
func Rank(candidates []Candidate, kindOrder []string, limit int) (chosen []int, facets []Facet) {
	order := make([]int, len(candidates))
	for i := range order {
		order[i] = i
	}
	kindRank := func(kind string) int {
		if i := slices.Index(kindOrder, kind); i >= 0 {
			return i
		}
		return len(kindOrder)
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := candidates[order[a]], candidates[order[b]]
		if x.Score != y.Score {
			return x.Score > y.Score
		}
		if kx, ky := kindRank(x.Kind), kindRank(y.Kind); kx != ky {
			return kx < ky
		}
		return x.Key < y.Key
	})
	if limit >= 0 && len(order) > limit {
		order = order[:limit]
	}
	counts := map[string]int{}
	for _, c := range candidates {
		counts[c.Kind]++
	}
	facets = []Facet{}
	for _, kind := range kindOrder {
		if n := counts[kind]; n > 0 {
			facets = append(facets, Facet{Kind: kind, Count: n})
		}
	}
	return order, facets
}
