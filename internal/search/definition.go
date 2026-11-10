package search

import (
	"encoding/json"
	"maps"
	"slices"
)

// maxDefinitionWords bounds what DefinitionWords returns.
const maxDefinitionWords = 100

// definitionKeys are the keys of a metric or model definition whose string
// values name something a person may search for: fields, columns, tables.
var definitionKeys = map[string]struct{}{
	"name": {}, "label": {}, "column": {}, "table": {}, "description": {},
}

// DefinitionWords are the names inside a JSON definition (a semantic metric's
// or model's): the string values of the keys in definitionKeys, anywhere in
// the document, in document order and without repeats, at most 100. Invalid
// JSON has none.
func DefinitionWords(raw []byte) []string {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return []string{}
	}
	out := []string{}
	seen := map[string]struct{}{}
	var walk func(v any)
	walk = func(v any) {
		if len(out) >= maxDefinitionWords {
			return
		}
		switch x := v.(type) {
		case map[string]any:
			for _, key := range slices.Sorted(maps.Keys(x)) {
				if s, ok := x[key].(string); ok {
					if _, named := definitionKeys[key]; named && s != "" {
						if _, dup := seen[s]; !dup {
							seen[s] = struct{}{}
							out = append(out, s)
						}
					}
					continue
				}
				walk(x[key])
			}
		case []any:
			for _, item := range x {
				walk(item)
			}
		}
	}
	walk(doc)
	if len(out) > maxDefinitionWords {
		out = out[:maxDefinitionWords]
	}
	return out
}
