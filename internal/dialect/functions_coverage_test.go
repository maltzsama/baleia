package dialect_test

// TestFunctionQueryCoverage is the gate before the committed function catalogs
// are removed (#788). On the dev cluster, each engine's runtime introspection
// (dialect.FunctionQuery) must cover everything the committed list offers,
// otherwise removing the list would regress the autocomplete.
//
// Run it per engine after regenerating the lists:
//
//	BALEIA_TEST_FUNCTION_COVERAGE=1 BALEIA_TEST_POSTGRES_URL=... \
//	  go test -run TestFunctionQueryCoverage -v ./internal/dialect/
//
// The test is skipped unless BALEIA_TEST_FUNCTION_COVERAGE is set, so CI does
// not depend on a live engine.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/dialect"
)

func TestFunctionQueryCoverage(t *testing.T) {
	t.Parallel()
	if os.Getenv("BALEIA_TEST_FUNCTION_COVERAGE") == "" {
		t.Skip("BALEIA_TEST_FUNCTION_COVERAGE not set")
	}
	sources := listSources()
	for name, src := range sources {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := dialect.NewRegistry().MustGet(name)
			query := d.FunctionQuery()
			if query == "" {
				t.Skip("no function catalog")
			}
			run := src.open(t) // skips when BALEIA_TEST_*_URL is unset
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			rows, err := run(ctx, query)
			if err != nil {
				t.Fatalf("function query: %v", err)
			}
			got := make(map[string]bool, len(rows))
			for _, r := range rows {
				if len(r) == 0 {
					continue
				}
				if w := strings.ToUpper(strings.TrimSpace(text(r[0]))); w != "" && w != "NULL" {
					got[w] = true
				}
			}
			if len(got) == 0 {
				t.Fatalf("%s: introspection returned no functions", name)
			}
			want := committedFunctions(t, name)
			covered := 0
			for w := range want {
				if got[w] {
					covered++
				}
			}
			t.Logf("%s: introspection=%d committed=%d covered=%d missing=%d extra=%d",
				name, len(got), len(want), covered, len(want)-covered, len(got)-covered)
			if covered < len(want) {
				t.Errorf("%s: introspection misses %d of %d committed functions", name, len(want)-covered, len(want))
			}
		})
	}
}

// committedFunctions reads the committed catalog (the JSON the endpoint serves,
// falling back to the word list) as an upper-cased name set.
func committedFunctions(t *testing.T, name string) map[string]bool {
	t.Helper()
	if raw, err := os.ReadFile(filepath.Join("lists", name+".functions.json")); err == nil {
		var refs []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &refs) == nil && len(refs) > 0 {
			out := make(map[string]bool, len(refs))
			for _, r := range refs {
				out[strings.ToUpper(r.Name)] = true
			}
			return out
		}
	}
	raw, err := os.ReadFile(filepath.Join("lists", name+".functions.txt"))
	if err != nil {
		t.Fatalf("read committed list: %v", err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[strings.ToUpper(line)] = true
	}
	return out
}
