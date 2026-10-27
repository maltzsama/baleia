package trino

import "testing"

// TestVersion_Golden pins one vector the plugin also pins. The full shared
// set lives in the plugin release's contract/ and is checked by
// internal/contract (make contract-fetch; go test -tags contract), so no
// copy of the vector file is kept here (#39).
func TestVersion_Golden(t *testing.T) {
	t.Parallel()
	props := map[string]string{"tpch.splits-per-node": "4"}
	if got := Version("tpch_test", "tpch", props); got != "2f8450cf6bb5940875d1a8d60a13e7d0541a4bb3b9eaa46a1063de16b108f636" {
		t.Errorf("Version = %s; the algorithm no longer matches the plugin's", got)
	}
}

// TestVersion_OrderIndependent verifies that map iteration order does not
// affect the hash: the algorithm sorts keys alphabetically before hashing.
func TestVersion_OrderIndependent(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
