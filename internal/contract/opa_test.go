//go:build contract

package contract

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maltzsama/baleia/deploy/opa"
)

// TestOPAPolicyRevision pins the Rego of the plugin release to the Rego this
// Baleia embeds (ADR 0028): the release's policy bundle must carry, as its
// manifest revision, the digest Baleia expects OPA to report. A Rego change on
// either side fails here until both match.
func TestOPAPolicyRevision(t *testing.T) {
	want := opa.Digest()
	raw, err := os.ReadFile(filepath.Join("testdata", "opa", "REVISION"))
	if err != nil {
		t.Fatalf("%v (run make contract-fetch)", err)
	}
	if got := strings.TrimSpace(string(raw)); got != want {
		t.Fatalf("plugin REVISION = %s, Baleia's deploy/opa digest = %s", got, want)
	}

	f, err := os.Open(filepath.Join("testdata", "opa", "baleia-opa-policy.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var manifest struct {
		Revision string   `json:"revision"`
		Roots    []string `json:"roots"`
	}
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimPrefix(hdr.Name, "/") != ".manifest" {
			continue
		}
		if err := json.NewDecoder(tr).Decode(&manifest); err != nil {
			t.Fatal(err)
		}
		found = true
	}
	if !found {
		t.Fatal("the policy bundle has no .manifest")
	}
	if manifest.Revision != want {
		t.Errorf("bundle revision = %s, want %s", manifest.Revision, want)
	}
	slices.Sort(manifest.Roots)
	if want := []string{"baleia/core", "baleia/trino"}; !slices.Equal(manifest.Roots, want) {
		t.Errorf("bundle roots = %v, want %v: a data bundle must not be able to replace the code", manifest.Roots, want)
	}
}

// TestOPAAccessControlProperties checks the Trino OPA properties the release
// ships: Trino 484 reads an unset row-filters or column-masking URI as "no
// filter" or "no mask", so all five must name the baleia.trino rules.
func TestOPAAccessControlProperties(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "opa", "opa-access-control.properties"))
	if err != nil {
		t.Fatalf("%v (run make contract-fetch)", err)
	}
	defer func() { _ = f.Close() }()
	props := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("unparsable line %q", line)
		}
		props[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	const base = "http://127.0.0.1:8181/v1/data/baleia/trino/"
	want := map[string]string{
		"access-control.name":                 "opa",
		"opa.policy.uri":                      base + "allow",
		"opa.policy.batched-uri":              base + "batch",
		"opa.policy.row-filters-uri":          base + "row_filters",
		"opa.policy.column-masking-uri":       base + "column_mask",
		"opa.policy.batch-column-masking-uri": base + "batch_column_masks",
	}
	for key, value := range want {
		if props[key] != value {
			t.Errorf("%s = %q, want %q", key, props[key], value)
		}
	}
}
