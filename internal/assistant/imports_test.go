package assistant

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestImportBoundary pins the #62 rule: the assistant persists only its own
// tables and reaches data exclusively through the MCP tools. It may not import
// the API, the main db package, an engine adapter, Temporal or object storage.
func TestImportBoundary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbiddenExact := map[string]bool{
		"github.com/galileostudio/baleia/internal/db":          true,
		"github.com/galileostudio/baleia/internal/api":         true,
		"github.com/galileostudio/baleia/internal/trino":       true,
		"github.com/galileostudio/baleia/internal/queryengine": true,
		"github.com/galileostudio/baleia/internal/temporal":    true,
		"github.com/galileostudio/baleia/internal/storage":     true,
	}
	forbiddenPrefixes := []string{
		"github.com/galileostudio/baleia/internal/trino/",
		"github.com/galileostudio/baleia/internal/queryengine/",
		"github.com/galileostudio/baleia/internal/temporal/",
		"github.com/galileostudio/baleia/internal/storage/",
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, imp := range parsed.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			if forbiddenExact[path] {
				t.Errorf("%s imports the forbidden package %s", file, path)
			}
			for _, prefix := range forbiddenPrefixes {
				if strings.HasPrefix(path, prefix) {
					t.Errorf("%s imports the forbidden package %s", file, path)
				}
			}
		}
	}
}
