package opa

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestDigest recomputes the digest from the files on disk, the way the policy
// bundle's manifest revision is computed, test files left out.
func TestDigest(t *testing.T) {
	t.Parallel()
	var names []string
	err := filepath.WalkDir("baleia", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".rego") && !strings.HasSuffix(path, "_test.rego") {
			names = append(names, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	var lines strings.Builder
	for _, name := range names {
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		fmt.Fprintf(&lines, "%s %s\n", name, hex.EncodeToString(sum[:]))
	}
	sum := sha256.Sum256([]byte(lines.String()))
	if got, want := Digest(), hex.EncodeToString(sum[:]); got != want || len(names) == 0 {
		t.Fatalf("Digest = %s, want %s over %v", got, want, names)
	}
}
