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
	// Call is false for a keyword-like special form (current_date) that takes
	// no parentheses.
	Call bool `json:"call"`
}

// noCall lists the SQL special forms the lists carry as functions but that are
// written without parentheses.
var noCall = map[string]bool{
	"current_catalog": true, "current_date": true, "current_path": true,
	"current_role": true, "current_schema": true, "current_time": true,
	"current_timestamp": true, "current_user": true, "localtime": true,
	"localtimestamp": true, "session_user": true, "system_user": true,
	"user": true,
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
		out = append(out, FunctionReference{Name: word, Kind: "function", Call: !noCall[key]})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}
