package api

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestErrorCodesClosed enforces the closed error-code contract (#67):
//
//  1. handlers never build an Error/ValidationError with a string literal code;
//  2. every generated ErrorCode has an error_<code> key in both locales;
//  3. every generated FieldCode has a field_<code> key in both locales.
//
// The UI translates code+params, so a code without a message is a broken UI.
func TestErrorCodesClosed(t *testing.T) {
	root := filepath.Join("..", "..")

	t.Run("no string literal codes", func(t *testing.T) {
		literal := regexp.MustCompile(`\bCode:\s*"`)
		err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if m := literal.FindIndex(data); m != nil {
				line := 1 + strings.Count(string(data[:m[0]]), "\n")
				t.Errorf("%s:%d builds a code from a string literal; use the generated ErrorCode constant", path, line)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	models, err := os.ReadFile(filepath.Join(root, "internal", "api", "models.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	errorCodes := enumValues(t, models, "ErrorCode")
	fieldCodes := enumValues(t, models, "FieldCode")

	for _, locale := range []string{"en", "pt-BR"} {
		messages := readMessages(t, filepath.Join(root, "web", "messages", locale+".json"))
		for _, code := range errorCodes {
			if _, ok := messages["error_"+code]; !ok {
				t.Errorf("%s.json is missing the error_%s message", locale, code)
			}
		}
		for _, code := range fieldCodes {
			if _, ok := messages["field_"+code]; !ok {
				t.Errorf("%s.json is missing the field_%s message", locale, code)
			}
		}
	}
}

func enumValues(t *testing.T, source []byte, typeName string) []string {
	t.Helper()
	re := regexp.MustCompile(`[A-Za-z0-9_]+\s+` + typeName + `\s*=\s*"([a-z_]+)"`)
	matches := re.FindAllSubmatch(source, -1)
	if len(matches) == 0 {
		t.Fatalf("no %s constants found in models.gen.go", typeName)
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, string(m[1]))
	}
	return out
}

func readMessages(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var messages map[string]any
	if err := json.Unmarshal(data, &messages); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return messages
}
