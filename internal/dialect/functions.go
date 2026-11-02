package dialect

import (
	"sort"
	"strings"
)

// FunctionReference is one function the editor knows about (B2, #531).
// Signature and Description are optional: the committed word lists carry the
// names, and a generated reference enriches them where the engine exposes it.
type FunctionReference struct {
	Name        string `json:"name"`
	Signature   string `json:"signature,omitempty"`
	Kind        string `json:"kind"`
	Description string `json:"description,omitempty"`
}

// FunctionReferences returns the dialect's functions, sorted by name and
// deduplicated. Unknown dialects return nil.
func FunctionReferences(name string) []FunctionReference {
	words, ok := lists[name]
	if !ok {
		return nil
	}
	seen := make(map[string]bool, len(words.functions))
	out := make([]FunctionReference, 0, len(words.functions))
	for _, word := range words.functions {
		key := strings.ToLower(word)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, FunctionReference{Name: word, Kind: "function"})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}
