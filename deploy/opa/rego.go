// Package opa holds the Rego package Baleia's data policies run on (ADR
// 0028), and the digest OPA reports for it.
package opa

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

//go:embed baleia/core/*.rego baleia/trino/*.rego
var rego embed.FS

// Digest is the policy bundle's manifest revision: the SHA-256 of the sorted
// "<path> <sha256>" lines of its Rego files, test files left out. OPA reports
// it as the policy bundle's active revision, and Baleia compares the two
// (data_policy_digest_mismatch).
func Digest() string {
	files := map[string][]byte{}
	err := fs.WalkDir(rego, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".rego") || strings.HasSuffix(path, "_test.rego") {
			return nil
		}
		content, err := rego.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = content
		return nil
	})
	if err != nil {
		// The files are embedded at build time; a read cannot fail.
		panic(fmt.Sprintf("opa: read the embedded rego: %v", err))
	}
	var lines strings.Builder
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		sum := sha256.Sum256(files[name])
		fmt.Fprintf(&lines, "%s %s\n", name, hex.EncodeToString(sum[:]))
	}
	sum := sha256.Sum256([]byte(lines.String()))
	return hex.EncodeToString(sum[:])
}
