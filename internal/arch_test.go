// Package internal holds module-wide architecture tests (docs/adr/0007).
package internal

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// guardedConstructor is a function that opens an engine, driver or database
// connection and may only be called from the files a rule allows.
type guardedConstructor struct {
	importPath string
	name       string
}

type constructionRule struct {
	constructors []guardedConstructor
	// allowed lists module-relative path prefixes (a directory ends in "/").
	allowed []string
	// offenders lists files that still break the rule, each with the issue
	// that removes it. The list may only shrink: an entry that no longer
	// offends fails the test so it gets deleted.
	offenders map[string]string
}

// wiringSites are the only places that construct adapters (ADR 0007).
var wiringSites = []string{"internal/server/server.go", "cmd/worker/main.go"}

var constructionRules = []constructionRule{
	{
		constructors: []guardedConstructor{
			{"github.com/galileostudio/baleia/internal/trino", "New"},
			{"github.com/galileostudio/baleia/internal/trino", "NewWithHTTPClient"},
		},
		allowed: append([]string{"internal/queryengine/", "internal/bindings/"}, wiringSites...),
	},
	{
		// Baleia's own database is opened by the binaries and the migrator;
		// engine and provider packages open user databases.
		constructors: []guardedConstructor{
			{"database/sql", "Open"},
			{"database/sql", "OpenDB"},
			{"github.com/jackc/pgx/v5", "Connect"},
			{"github.com/jackc/pgx/v5", "ConnectConfig"},
			{"github.com/jackc/pgx/v5/pgxpool", "New"},
			{"github.com/jackc/pgx/v5/pgxpool", "NewWithConfig"},
		},
		allowed: []string{"cmd/", "internal/migrate/", "internal/queryengine/", "internal/connections/"},
	},
}

// skippedDirs are not part of the Go module's production code.
var skippedDirs = map[string]bool{
	".git": true, ".plugin": true, "node_modules": true, "tmp": true, "web": true, "testdata": true,
}

func TestAdapterConstructionOnlyInWiring(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}

	calls := map[guardedConstructor][]string{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, c := range guardedCalls(t, path) {
			calls[c] = append(calls[c], rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, rule := range constructionRules {
		offending := map[string]bool{}
		for _, c := range rule.constructors {
			for _, file := range calls[c] {
				if isAllowed(file, rule.allowed) {
					continue
				}
				offending[file] = true
				if _, known := rule.offenders[file]; !known {
					t.Errorf("%s calls %s.%s; construct it at a wiring site (%s) and inject it (docs/adr/0007-dispatch-and-wiring.md)",
						file, c.importPath, c.name, strings.Join(wiringSites, ", "))
				}
			}
		}
		stale := make([]string, 0, len(rule.offenders))
		for file := range rule.offenders {
			if !offending[file] {
				stale = append(stale, file)
			}
		}
		sort.Strings(stale)
		for _, file := range stale {
			t.Errorf("%s no longer constructs a guarded adapter; remove it from the offenders list (%s)", file, rule.offenders[file])
		}
	}
}

// guardedCalls returns every guarded constructor called in the file, resolved
// through the file's imports so aliases are caught.
func guardedCalls(t *testing.T, path string) []guardedConstructor {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	imports := map[string]string{}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatalf("%s: import %s: %v", path, imp.Path.Value, err)
		}
		name := p[strings.LastIndex(p, "/")+1:]
		if strings.HasPrefix(name, "v") && strings.Count(p, "/") > 0 {
			// Major-version suffix: github.com/jackc/pgx/v5 is package pgx.
			if _, err := strconv.Atoi(name[1:]); err == nil {
				trimmed := p[:strings.LastIndex(p, "/")]
				name = trimmed[strings.LastIndex(trimmed, "/")+1:]
			}
		}
		if imp.Name != nil {
			name = imp.Name.Name
		}
		imports[name] = p
	}

	var found []guardedConstructor
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if p, ok := imports[pkg.Name]; ok {
			found = append(found, guardedConstructor{p, sel.Sel.Name})
		}
		return true
	})
	return found
}

func isAllowed(file string, allowed []string) bool {
	for _, a := range allowed {
		if file == a || (strings.HasSuffix(a, "/") && strings.HasPrefix(file, a)) {
			return true
		}
	}
	return false
}

// moduleImports maps every production .go file (module-relative) to its
// import paths.
func moduleImports(t *testing.T) map[string][]string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			out[filepath.ToSlash(rel)] = append(out[filepath.ToSlash(rel)], p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestNoDriversOutsideAdapters: database and engine drivers are imported only
// by the adapter and provider packages that own them (docs/adr/0002, #20).
// pgx is also Baleia's own database driver and is guarded by
// TestAdapterConstructionOnlyInWiring instead.
func TestNoDriversOutsideAdapters(t *testing.T) {
	drivers := []string{
		"github.com/go-sql-driver/mysql",
		"github.com/microsoft/go-mssqldb",
		"github.com/sijms/go-ora",
		"modernc.org/sqlite",
		"github.com/ClickHouse/",
		"github.com/apache/arrow-go/v18/arrow/flight",
	}
	adapter := func(file string) bool {
		for _, root := range []string{"internal/queryengine/", "internal/connections/"} {
			rest, ok := strings.CutPrefix(file, root)
			if ok && strings.Contains(rest, "/") { // a kind's own package, not the contract
				return true
			}
		}
		return false
	}
	for file, imports := range moduleImports(t) {
		if adapter(file) {
			continue
		}
		for _, imp := range imports {
			for _, drv := range drivers {
				if strings.HasPrefix(imp, drv) {
					t.Errorf("%s imports the driver %s; only internal/queryengine/<kind> and internal/connections/<kind> may", file, imp)
				}
			}
		}
	}
}

// trinoInDomain lists domain files allowed to import internal/trino. It is
// empty: the domain runs SQL through internal/queryengine, catalog DDL
// through internal/bindings/trino and monitoring through
// internal/trino/monitor. Keep it empty.
var trinoInDomain = map[string]string{}

// TestNoTrinoImportsInDomain: internal/api and internal/temporal run SQL
// through internal/queryengine, never through the Trino client.
func TestNoTrinoImportsInDomain(t *testing.T) {
	const trinoPkg = "github.com/galileostudio/baleia/internal/trino"
	offending := map[string]bool{}
	for file, imports := range moduleImports(t) {
		if !strings.HasPrefix(file, "internal/api/") && !strings.HasPrefix(file, "internal/temporal/") {
			continue
		}
		for _, imp := range imports {
			if imp != trinoPkg {
				continue
			}
			offending[file] = true
			if _, known := trinoInDomain[file]; !known {
				t.Errorf("%s imports internal/trino; run SQL through internal/queryengine", file)
			}
		}
	}
	for file, issue := range trinoInDomain {
		if !offending[file] {
			t.Errorf("%s no longer imports internal/trino; remove it from trinoInDomain (%s)", file, issue)
		}
	}
}
