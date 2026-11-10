package search

import (
	"slices"
	"strings"
)

// MaxVariants bounds how many alternative queries a glossary adds to one
// search: each runs the search again.
const MaxVariants = 8

// Glossary is a workspace's sets of words that mean the same: searching for
// one member of a set also looks for the others.
type Glossary struct {
	// sets hold the folded members of each entry, term first.
	sets [][]string
}

// NewGlossary builds a glossary from entries, each a term and its synonyms.
// Members are folded; empty and repeated ones are dropped.
func NewGlossary(entries [][]string) Glossary {
	g := Glossary{}
	for _, entry := range entries {
		set := []string{}
		for _, member := range entry {
			if folded := Fold(member); folded != "" && !slices.Contains(set, folded) {
				set = append(set, folded)
			}
		}
		if len(set) > 1 {
			g.sets = append(g.sets, set)
		}
	}
	return g
}

// Variants are the alternative queries for q and the words they add. A set
// applies when q as a whole, or one of its words, is a member: the variant is
// q with that part replaced by another member. At most MaxVariants are
// returned. usable, when not nil, says whether a variant may be searched at all
// (a search has a length bounds a synonym can break); the others are dropped
// and their members are not reported. added lists the members used, in order,
// once each; both are empty when no set applies.
func (g Glossary) Variants(q string, usable func(string) bool) (variants, added []string) {
	variants, added = []string{}, []string{}
	whole := Fold(q)
	words := Tokens(q)
	add := func(query, member string) {
		if len(variants) >= MaxVariants || slices.Contains(variants, query) || (usable != nil && !usable(query)) {
			return
		}
		variants = append(variants, query)
		if !slices.Contains(added, member) {
			added = append(added, member)
		}
	}
	for _, set := range g.sets {
		if slices.Contains(set, whole) {
			for _, member := range set {
				if member != whole {
					add(member, member)
				}
			}
		}
		for i, word := range words {
			if !slices.Contains(set, word) {
				continue
			}
			for _, member := range set {
				if member == word {
					continue
				}
				replaced := slices.Clone(words)
				replaced[i] = member
				add(strings.Join(replaced, " "), member)
			}
		}
	}
	return variants, added
}
