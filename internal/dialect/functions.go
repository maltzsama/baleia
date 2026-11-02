package dialect

import (
	"embed"
	"encoding/json"
	"sort"
	"strings"
)

// FunctionReference is one function the editor knows about (B2, #531).
// Signature and Description are optional: the committed word lists carry the
// names, and the generated reference enriches them where the engine exposes it.
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

//go:embed lists/*.functions.json
var functionFiles embed.FS

// Callable reports whether a function in the dialect takes parentheses; the
// special forms (current_date) do not. MySQL/MariaDB expose USER(),
// SESSION_USER() and SYSTEM_USER() as callable functions.
func Callable(dialectName, name string) bool {
	key := strings.ToLower(name)
	if !noCall[key] {
		return true
	}
	if (dialectName == MySQL || dialectName == MariaDB) &&
		(key == "user" || key == "session_user" || key == "system_user") {
		return true
	}
	return false
}

// FunctionReferences returns the dialect's functions, sorted by name and
// deduplicated. The generated JSON is used when present; the embedded word
// list is the fallback. Unknown dialects return nil.
func FunctionReferences(name string) []FunctionReference {
	if raw, err := functionFiles.ReadFile("lists/" + name + ".functions.json"); err == nil {
		var out []FunctionReference
		if json.Unmarshal(raw, &out) == nil {
			return out
		}
	}
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
		out = append(out, FunctionReference{Name: word, Kind: "function", Call: Callable(name, word)})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}
