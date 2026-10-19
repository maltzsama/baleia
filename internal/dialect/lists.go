package dialect

import (
	"embed"
	"strings"
)

// Keyword and function lists per dialect, generated against real servers by
// scripts/gen-dialect-lists.sh and committed (never fetched at runtime).
// Lines starting with # are the header recording the server version.
//
//go:embed lists/*.txt
var listFiles embed.FS

type wordLists struct{ keywords, functions []string }

// allDialects is the list of dialects that ship word lists: it must match the
// registry (see TestAllDialectsMatchRegistry). Adding a dialect here requires
// regenerating lists/<name>.keywords.txt and lists/<name>.functions.txt.
var allDialects = []string{
	Trino, PostgreSQL, MySQL, MariaDB, TSQL, Oracle, SQLite, ClickHouse, FlightSQL,
}

var lists = loadLists()

func loadLists() map[string]wordLists {
	out := make(map[string]wordLists, len(allDialects))
	for _, name := range allDialects {
		out[name] = wordLists{
			keywords:  readWordList(name + ".keywords.txt"),
			functions: readWordList(name + ".functions.txt"),
		}
	}
	return out
}

func readWordList(file string) []string {
	b, err := listFiles.ReadFile("lists/" + file)
	if err != nil {
		// Embedded files are fixed at compile time, so a missing list means the
		// repo is out of sync with allDialects. Fail loudly at startup rather
		// than shipping silently empty autocomplete.
		panic("dialect: missing embedded word list " + file + ": " + err.Error())
	}
	lines := strings.Split(string(b), "\n")
	// len(lines) is a free upper bound: no reallocation while filtering.
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}
