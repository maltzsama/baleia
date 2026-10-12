package catalogs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// vector mirrors the wire shape of catalog_version_vectors.json, the shared
// golden file between this Go package and the baleia-trino-catalog-store
// Java plugin (ComputeCatalogVersionTest). Both sides read the same JSON
// from their respective testdata / src/test/resources — the file ships in
// both repos so any change in the algorithm or inputs is caught on both
// sides as a single golden-source-of-truth.
type vector struct {
	Name         string            `json:"name"`
	Connector    string            `json:"connector"`
	Properties   map[string]string `json:"properties"`
	ExpectedHash string            `json:"expected_hash"`
	Note         string            `json:"note"`
}

// loadVectors reads testdata/catalog_version_vectors.json. If the file is
// missing or empty the test fails loudly — a silent pass would mean we are
// no longer pinned to the plugin Java side.
func loadVectors(t *testing.T) []vector {
	t.Helper()
	path := filepath.Join("testdata", "catalog_version_vectors.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var vs []vector
	if err := json.Unmarshal(data, &vs); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(vs) == 0 {
		t.Fatalf("%s contains no vectors", path)
	}
	return vs
}

// TestVersion_SharedVectors pins Version() against the golden vectors shared
// with the Java plugin. Both sides read catalog_version_vectors.json; this
// test fails if the Go implementation diverges from the recorded hashes
// (e.g. due to a wrong UTF-16 length prefix or an unordered iteration).
//
// If you regenerated the hashes intentionally (algorithm changed, inputs
// changed), update testdata/catalog_version_vectors.json AND re-run
// ComputeCatalogVersionTest on the Java side so the contract stays in sync.
func TestVersion_SharedVectors(t *testing.T) {
	for _, v := range loadVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			got := Version(v.Name, v.Connector, v.Properties)
			if got != v.ExpectedHash {
				t.Errorf("Version(%q,%q,%v) = %q; want %q (note: %s)",
					v.Name, v.Connector, v.Properties, got, v.ExpectedHash, v.Note)
			}
		})
	}
}

// TestVersion_OrderIndependent verifies that map iteration order does not
// affect the hash: the algorithm sorts keys alphabetically before hashing.
func TestVersion_OrderIndependent(t *testing.T) {
	a := map[string]string{"b": "2", "a": "1", "c": "3"}
	b := map[string]string{"c": "3", "a": "1", "b": "2"}
	if Version("cat", "iceberg", a) != Version("cat", "iceberg", b) {
		t.Fatal("Version is sensitive to map iteration order; must be canonical")
	}
}

// TestVersion_PropOrderMatters verifies that two catalogs differing only in
// property values produce different versions. This guards against accidental
// canonicalization that drops the values.
func TestVersion_PropOrderMatters(t *testing.T) {
	x := map[string]string{"iceberg.rest-catalog.uri": "http://a"}
	y := map[string]string{"iceberg.rest-catalog.uri": "http://b"}
	if Version("cat", "iceberg", x) == Version("cat", "iceberg", y) {
		t.Fatal("Version should change when property values change")
	}
}

// TestVersion_HashesResolvedValuesNotPlaceholders pins the easiest-to-break
// invariant of the Java<->Go contract: catalog_version is ALWAYS computed
// over the real values stored in trino_catalog_registry, NEVER over the
// placeholder text that goes into the DDL.
//
// The plugin resolves secrets BEFORE calling computeCatalogVersion
// (BaleiaCatalogStore.createCatalogProperties). If Go hashes the redacted
// map, the two sides diverge for every catalog with a secret and the #23557
// check becomes a permanent false positive.
func TestVersion_HashesResolvedValuesNotPlaceholders(t *testing.T) {
	real := map[string]string{
		"iceberg.catalog.type":                   "rest",
		"iceberg.rest-catalog.oauth2.credential": "abc:def",
	}
	redacted := map[string]string{
		"iceberg.catalog.type":                   "rest",
		"iceberg.rest-catalog.oauth2.credential": "@baleia-secret[cat:iceberg.rest-catalog.oauth2.credential]",
	}

	if Version("cat", "iceberg", real) == Version("cat", "iceberg", redacted) {
		t.Fatal("identical hash for real value and placeholder — impossible; review Version()")
	}
}

// TestVersion_NameMatters: catalog name and connector are part of the hash.
func TestVersion_NameMatters(t *testing.T) {
	props := map[string]string{"tpch.splits-per-node": "4"}
	a := Version("x", "tpch", props)
	b := Version("y", "tpch", props)
	c := Version("x", "memory", props)
	if a == b {
		t.Fatal("Version should change when catalogName changes")
	}
	if a == c {
		t.Fatal("Version should change when connectorName changes")
	}
}