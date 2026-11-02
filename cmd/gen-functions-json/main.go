// Command gen-functions-json writes internal/dialect/lists/<dialect>.functions.json
// from the committed word lists (B2, #531). For PostgreSQL it enriches each
// function with the signature and description pg_proc exposes when
// BALEIA_TEST_POSTGRES_URL is set; the other engines keep their names until
// scripts/gen-dialect-lists.sh runs against them.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/jackc/pgx/v5"
)

func main() {
	dir := filepath.Join("internal", "dialect", "lists")
	entries, err := os.ReadDir(dir)
	if err != nil {
		panic(err)
	}
	pg := postgresFunctions(context.Background())
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".functions.txt")
		if !ok {
			continue
		}
		refs := readWordFunctions(filepath.Join(dir, entry.Name()))
		if name == dialect.PostgreSQL && pg != nil {
			for i := range refs {
				if fn, ok := pg[strings.ToLower(refs[i].Name)]; ok {
					refs[i].Signature, refs[i].Description = fn.signature, fn.description
					if fn.kind != "" {
						refs[i].Kind = fn.kind
					}
				}
			}
		}
		sort.Slice(refs, func(i, j int) bool {
			return strings.ToLower(refs[i].Name) < strings.ToLower(refs[j].Name)
		})
		raw, err := json.MarshalIndent(refs, "", "  ")
		if err != nil {
			panic(err)
		}
		out := filepath.Join(dir, name+".functions.json")
		if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil { //nolint:gosec // out is under the fixed lists directory
			panic(err)
		}
		fmt.Printf("wrote %s (%d functions)\n", out, len(refs))
	}
}

type pgFunction struct{ signature, description, kind string }

// readWordFunctions reads one committed word list into the reference shape,
// independent of the embedded JSON (which this command is writing).
func readWordFunctions(path string) []dialect.FunctionReference {
	raw, err := os.ReadFile(path) //nolint:gosec // path is under the fixed lists directory
	if err != nil {
		panic(err)
	}
	seen := map[string]bool{}
	var out []dialect.FunctionReference
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key := strings.ToLower(line)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, dialect.FunctionReference{Name: line, Kind: "function", Call: dialect.Callable(line)})
	}
	return out
}

func postgresFunctions(ctx context.Context) map[string]pgFunction {
	url := os.Getenv("BALEIA_TEST_POSTGRES_URL")
	if url == "" {
		fmt.Println("BALEIA_TEST_POSTGRES_URL unset: PostgreSQL signatures skipped")
		return nil
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		fmt.Println("postgres unavailable:", err)
		return nil
	}
	defer func() { _ = conn.Close(ctx) }()
	rows, err := conn.Query(ctx, `
		SELECT p.proname,
		       string_agg(p.proname || '(' || pg_get_function_arguments(p.oid) || ') → ' ||
		                  pg_get_function_result(p.oid), '; '),
		       max(coalesce(obj_description(p.oid, 'pg_proc'), '')),
		       CASE WHEN bool_or(p.prokind = 'a') THEN 'aggregate'
		            WHEN bool_or(p.prokind = 'w') THEN 'window' ELSE 'function' END
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'pg_catalog' AND p.prokind IN ('f', 'a', 'w')
		  AND p.proname !~ '^(_|pg_)'
		GROUP BY p.proname`)
	if err != nil {
		fmt.Println("postgres query:", err)
		return nil
	}
	defer rows.Close()
	out := make(map[string]pgFunction)
	for rows.Next() {
		var name, signature, description, kind string
		if err := rows.Scan(&name, &signature, &description, &kind); err != nil {
			panic(err)
		}
		out[strings.ToLower(name)] = pgFunction{signature: signature, description: description, kind: kind}
	}
	if err := rows.Err(); err != nil {
		panic(err)
	}
	return out
}
