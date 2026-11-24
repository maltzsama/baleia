package datapolicy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// The data bundle is a public, versioned contract (ADR 0028): operators may
// bring their own Rego that reads it. Changing its shape is a FormatVersion
// bump, released with the Rego that reads it.
const (
	// FormatVersion is the bundle data contract the release Rego reads.
	FormatVersion = 1
	// DataRoot is the bundle's only root. The Rego bundle claims baleia/core
	// and baleia/trino, so a data bundle can never replace the policy code.
	DataRoot = "baleia/bundle"
	// SigningKeyID is the key id OPA's bundle verification config names.
	SigningKeyID = "baleia"
)

// SubjectEntry is one Trino identity in the bundle's subject map.
type SubjectEntry struct {
	User           string   `json:"user,omitempty"`
	ServiceAccount string   `json:"service_account,omitempty"`
	Teams          []string `json:"teams"`
	Groups         []string `json:"groups"`
}

// TablePolicies are one table's row policies and column masks in the bundle.
type TablePolicies struct {
	RowPolicies []RowPolicy             `json:"row_policies,omitempty"`
	ColumnMasks map[string][]ColumnMask `json:"column_masks,omitempty"`
}

// RowPolicy is a row filter in the bundle.
type RowPolicy struct {
	Name  string    `json:"name"`
	Mode  Mode      `json:"mode"`
	To    []Subject `json:"to"`
	Using string    `json:"using"`
}

// ColumnMask is a column mask in the bundle.
type ColumnMask struct {
	Name       string    `json:"name"`
	Priority   int32     `json:"priority"`
	To         []Subject `json:"to"`
	Expression string    `json:"expression"`
}

// CatalogContent is the published policies of one Baleia catalog: the names
// it has on the engines that enforce data policies, and its tables by schema
// and name.
type CatalogContent struct {
	Names  []string                             `json:"names"`
	Tables map[string]map[string]*TablePolicies `json:"tables"`
}

// Content is what a revision publishes: every catalog's policies, keyed by
// the catalog's id, and the subject map. A revision stores it as JSON; the
// bundle is derived from it.
type Content struct {
	Catalogs map[string]CatalogContent `json:"catalogs"`
	Subjects map[string]SubjectEntry   `json:"subjects"`
}

// bundleData is the bundle's data.json, format_version 1.
type bundleData struct {
	FormatVersion int                                             `json:"format_version"`
	Revision      string                                          `json:"revision"`
	NotAfter      string                                          `json:"not_after"`
	Subjects      map[string]SubjectEntry                         `json:"subjects"`
	Tables        map[string]map[string]map[string]*TablePolicies `json:"tables"`
}

// CatalogContentOf turns a catalog's enabled, valid policies into its
// published content under names. Disabled policies are left out; the caller
// refuses to publish invalid ones first (PublishProblems).
func CatalogContentOf(names []string, policies []Policy) CatalogContent {
	out := CatalogContent{Names: slices.Sorted(slices.Values(names)), Tables: map[string]map[string]*TablePolicies{}}
	for _, p := range policies {
		if p.State != StateEnabled || p.Validation != ValidationValid {
			continue
		}
		if out.Tables[p.Target.Schema] == nil {
			out.Tables[p.Target.Schema] = map[string]*TablePolicies{}
		}
		t := out.Tables[p.Target.Schema][p.Target.Table]
		if t == nil {
			t = &TablePolicies{}
			out.Tables[p.Target.Schema][p.Target.Table] = t
		}
		switch p.Kind {
		case KindRowFilter:
			t.RowPolicies = append(t.RowPolicies, RowPolicy{Name: p.Name, Mode: p.Mode, To: p.Subjects, Using: p.Compiled})
		case KindColumnMask:
			if t.ColumnMasks == nil {
				t.ColumnMasks = map[string][]ColumnMask{}
			}
			t.ColumnMasks[p.Target.Column] = append(t.ColumnMasks[p.Target.Column],
				ColumnMask{Name: p.Name, Priority: p.Priority, To: p.Subjects, Expression: p.Compiled})
		}
	}
	for _, tables := range out.Tables {
		for _, t := range tables {
			slices.SortFunc(t.RowPolicies, func(a, b RowPolicy) int { return strings.Compare(a.Name, b.Name) })
			for column := range t.ColumnMasks {
				slices.SortFunc(t.ColumnMasks[column], func(a, b ColumnMask) int { return strings.Compare(a.Name, b.Name) })
			}
		}
	}
	return out
}

// NameConflicts lists the engine catalog names that two published catalogs
// share. The bundle keys tables by that name for every engine, so a shared
// name would apply one catalog's policies to the other's tables.
func (c Content) NameConflicts() []string {
	owner := map[string]string{}
	var conflicts []string
	for _, id := range slices.Sorted(maps.Keys(c.Catalogs)) {
		for _, name := range c.Catalogs[id].Names {
			if other, taken := owner[name]; taken && other != id {
				conflicts = append(conflicts, name)
				continue
			}
			owner[name] = id
		}
	}
	return slices.Compact(slices.Sorted(slices.Values(conflicts)))
}

// Bundle builds a revision's signed data bundle: a gzipped tarball with the
// manifest (revision and root), data.json and .signatures.json, the way
// `opa build --signing-key` lays it out. The tarball is deterministic for the
// same revision, content and key.
func Bundle(rev Revision, content Content, key *rsa.PrivateKey) ([]byte, error) {
	if key == nil {
		return nil, fmt.Errorf("datapolicy bundle: no signing key")
	}
	if conflicts := content.NameConflicts(); len(conflicts) > 0 {
		return nil, fmt.Errorf("datapolicy bundle: catalog names shared by two catalogs: %s", strings.Join(conflicts, ", "))
	}
	data := bundleData{
		FormatVersion: FormatVersion,
		Revision:      rev.ID.String(),
		NotAfter:      rev.NotAfter.UTC().Format(time.RFC3339),
		Subjects:      content.Subjects,
		Tables:        map[string]map[string]map[string]*TablePolicies{},
	}
	if data.Subjects == nil {
		data.Subjects = map[string]SubjectEntry{}
	}
	for _, catalog := range content.Catalogs {
		for _, name := range catalog.Names {
			data.Tables[name] = catalog.Tables
		}
	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("datapolicy bundle: encode data: %w", err)
	}
	manifest, err := json.Marshal(map[string]any{"revision": rev.ID.String(), "roots": []string{DataRoot}})
	if err != nil {
		return nil, fmt.Errorf("datapolicy bundle: encode manifest: %w", err)
	}
	files := map[string][]byte{".manifest": manifest, DataRoot + "/data.json": dataJSON}
	signatures, err := signBundle(files, key)
	if err != nil {
		return nil, err
	}
	files[".signatures.json"] = signatures
	return writeTarball(files)
}

func writeTarball(files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(files[name])), ModTime: time.Unix(0, 0)}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(files[name]); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// signBundle produces .signatures.json the way `opa build --signing-key`
// does: one RS256 JWT listing every file with the SHA-256 of its canonical
// content (JSON files are hashed over their parsed form, keys sorted).
func signBundle(files map[string][]byte, key *rsa.PrivateKey) ([]byte, error) {
	type fileInfo struct {
		Name      string `json:"name"`
		Hash      string `json:"hash"`
		Algorithm string `json:"algorithm"`
	}
	infos := make([]fileInfo, 0, len(files))
	for _, name := range slices.Sorted(maps.Keys(files)) {
		sum, err := opaFileHash(name, files[name])
		if err != nil {
			return nil, err
		}
		infos = append(infos, fileInfo{Name: name, Hash: sum, Algorithm: "SHA-256"})
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"files": infos})
	token.Header["kid"] = SigningKeyID
	signed, err := token.SignedString(key)
	if err != nil {
		return nil, fmt.Errorf("datapolicy bundle: sign: %w", err)
	}
	return json.Marshal(map[string]any{"signatures": []string{signed}})
}

// opaFileHash mirrors OPA's bundle hashing (v1/bundle/hash.go): structured
// files are parsed with numbers kept literal, then walked with object keys
// sorted; primitives are JSON-encoded without HTML escaping.
func opaFileHash(name string, content []byte) (string, error) {
	h := sha256.New()
	if !strings.HasSuffix(name, ".json") && name != ".manifest" {
		h.Write(content)
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return "", fmt.Errorf("datapolicy bundle: parse %s: %w", name, err)
	}
	if err := hashWalk(h, value); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type hashWriter interface{ Write([]byte) (int, error) }

func hashWalk(h hashWriter, v any) error {
	switch x := v.(type) {
	case map[string]any:
		h.Write([]byte("{"))
		for i, key := range slices.Sorted(maps.Keys(x)) {
			if i > 0 {
				h.Write([]byte(","))
			}
			if err := hashPrimitive(h, key); err != nil {
				return err
			}
			h.Write([]byte(":"))
			if err := hashWalk(h, x[key]); err != nil {
				return err
			}
		}
		h.Write([]byte("}"))
	case []any:
		h.Write([]byte("["))
		for i, e := range x {
			if i > 0 {
				h.Write([]byte(","))
			}
			if err := hashWalk(h, e); err != nil {
				return err
			}
		}
		h.Write([]byte("]"))
	default:
		return hashPrimitive(h, x)
	}
	return nil
}

func hashPrimitive(h hashWriter, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	h.Write(bytes.TrimRight(buf.Bytes(), "\n"))
	return nil
}
