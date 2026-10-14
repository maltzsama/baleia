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

var lists = loadLists()

func loadLists() map[string]wordLists {
	read := func(name string) []string {
		b, err := listFiles.ReadFile("lists/" + name)
		if err != nil {
			return []string{}
		}
		out := []string{}
		for _, line := range strings.Split(string(b), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
				out = append(out, line)
			}
		}
		return out
	}
	out := map[string]wordLists{}
	for _, n := range []string{Trino, PostgreSQL, MySQL, MariaDB, TSQL, Oracle, SQLite, ClickHouse, FlightSQL} {
		out[n] = wordLists{keywords: read(n + ".keywords.txt"), functions: read(n + ".functions.txt")}
	}
	return out
}
