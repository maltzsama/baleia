// Package catalogs implements the projection of catalog domain rows into the
// flat trino_catalog_registry rows consumed by the baleia-trino-catalog-store
// Trino plugin, and a byte-for-byte replica of Trino's
// FileCatalogStore.computeCatalogVersion so that the Baleia backend and the
// plugin Java side can agree on a stable catalog_version hash.
package trino

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"unicode/utf16"
)

// Version returns the SHA-256 hex digest computed with the exact same algorithm
// as io.trino.connector.FileCatalogStore.computeCatalogVersion on Trino 482.
//
// The Trino algorithm uses Guava Hasher:
//   - putUnencodedChars writes UTF-16 little-endian, one Java char (2 bytes)
//     per code unit. Surrogate pairs ( Supplementary/emoji characters ) emit
//     two uint16 code units, exactly like Java's internal representation.
//   - putInt(int) writes 4 bytes little-endian.
//   - String.length() in Java counts UTF-16 code units, NOT bytes — so the
//     length prefix must use the count of code units, not len(s) in Go.
//
// The shared golden vectors live in testdata/catalog_version_vectors.json
// (copied from baleia-trino-catalog-store/src/test/resources/) and are
// consumed both by version_test.go here and by ComputeCatalogVersionTest on
// the Java side. If you change the algorithm or the vectors, update both.
func Version(catalogName, connectorName string, properties map[string]string) string {
	h := sha256.New()
	writeUnencodedChars(h, "catalog-hash")
	hashLengthPrefixedString(h, catalogName)
	hashLengthPrefixedString(h, connectorName)

	putIntLE(h, len(properties))

	keys := sortedKeys(properties)
	for _, k := range keys {
		hashLengthPrefixedString(h, k)
		hashLengthPrefixedString(h, properties[k])
	}

	return fmt.Sprintf("%x", h.Sum(nil))
}

// sortedKeys returns the map keys sorted alphabetically. Matches Guava's
// ImmutableSortedMap.copyOf(properties).forEach(...) ordering, which uses
// natural String order: UTF-16 code-unit-wise comparison, identical to Go's
// default string sort for all inputs we accept.
func sortedKeys(properties map[string]string) []string {
	keys := make([]string, 0, len(properties))
	for k := range properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// hashLengthPrefixedString mirrors Guava's
// hashLengthPrefixedString(Hasher, String): putInt(String.length()) followed
// by putUnencodedChars.
//
// Java's String.length() returns the number of UTF-16 code units in the
// string, so we must encode the string into UTF-16 code units first and use
// that count — NOT len(s), which counts UTF-8 bytes. For ASCII inputs the
// two coincide; for BMP characters above U+007F they diverge (UTF-8 uses
// more bytes than code units); for characters above U+FFFF they diverge
// further (UTF-16 uses 2 surrogate code units, UTF-8 uses 4 bytes).
//
// We also write 2 bytes per code unit (little-endian), as Guava does.
func hashLengthPrefixedString(w hashWriter, s string) {
	units := utf16.Encode([]rune(s))
	putIntLE(w, len(units))
	for _, u := range units {
		var buf [2]byte
		binary.LittleEndian.PutUint16(buf[:], u)
		_, _ = w.Write(buf[:])
	}
}

// writeUnencodedChars writes s as UTF-16 little-endian without a length prefix.
// Used for the "catalog-hash" magic header, matching Guava's
// hasher.putUnencodedChars("catalog-hash").
func writeUnencodedChars(w hashWriter, s string) {
	units := utf16.Encode([]rune(s))
	for _, u := range units {
		var buf [2]byte
		binary.LittleEndian.PutUint16(buf[:], u)
		_, _ = w.Write(buf[:])
	}
}

// putIntLE writes v as 4 bytes little-endian, matching Guava's
// Hasher.putInt(int).
func putIntLE(w hashWriter, v int) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], uint32(v))
	_, _ = w.Write(buf[:])
}

type hashWriter interface {
	Write([]byte) (int, error)
}
