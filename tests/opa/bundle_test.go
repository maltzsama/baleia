//go:build opa

package opa

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// dataRoot is the only root a data bundle may write: the Rego package and the
// deployment configuration live outside it, so a bundle cannot replace them.
const dataRoot = "baleia/bundle"

// policyTarball packages deploy/opa/baleia (tests and test data excluded) as
// the OPA bundle the plugin release would ship. Its manifest revision is the
// policy digest, which OPA reports in its status. The tarball is
// deterministic, so its checksum is stable for the same sources.
func policyTarball() (tarball []byte, digest string, err error) {
	root := filepath.Join(repoRoot(), "deploy", "opa")
	files := map[string][]byte{}
	err = filepath.WalkDir(filepath.Join(root, "baleia"), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".rego") || strings.HasSuffix(path, "_test.rego") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		files[filepath.ToSlash(rel)] = content
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("reading the rego package: %w", err)
	}
	digest = policyDigest(files)
	manifest, err := json.Marshal(map[string]any{
		"revision": digest,
		"roots":    []string{"baleia/core", "baleia/trino"},
	})
	if err != nil {
		return nil, "", err
	}
	files[".manifest"] = manifest
	tarball, err = writeTarball(files)
	return tarball, digest, err
}

// policyDigest hashes the sorted list of "<path> <sha256>" lines of the Rego
// files.
func policyDigest(files map[string][]byte) string {
	var lines strings.Builder
	for _, name := range slices.Sorted(maps.Keys(files)) {
		sum := sha256.Sum256(files[name])
		fmt.Fprintf(&lines, "%s %s\n", name, hex.EncodeToString(sum[:]))
	}
	sum := sha256.Sum256([]byte(lines.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeTarball(files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		hdr := &tar.Header{
			Name:    name,
			Mode:    0o644,
			Size:    int64(len(files[name])),
			ModTime: time.Unix(0, 0),
		}
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

// dataBundle is the bundle data contract, format_version 1: what Baleia
// serializes from the policies the user saved, as-is.
type dataBundle struct {
	FormatVersion int                                             `json:"format_version"`
	Revision      string                                          `json:"revision"`
	NotAfter      string                                          `json:"not_after"` // the lease, RFC 3339
	Subjects      map[string]subject                              `json:"subjects"`
	Tables        map[string]map[string]map[string]*tablePolicies `json:"tables"`
}

type subject struct {
	User           string   `json:"user,omitempty"`
	ServiceAccount string   `json:"service_account,omitempty"`
	Teams          []string `json:"teams"`
	Groups         []string `json:"groups"`
}

type tablePolicies struct {
	RowPolicies []rowPolicy             `json:"row_policies,omitempty"`
	ColumnMasks map[string][]columnMask `json:"column_masks,omitempty"`
}

type rowPolicy struct {
	Name  string       `json:"name"`
	Mode  string       `json:"mode"`
	To    []subjectRef `json:"to"`
	Using string       `json:"using"`
}

type columnMask struct {
	Name       string       `json:"name"`
	Priority   int          `json:"priority"`
	To         []subjectRef `json:"to"`
	Expression string       `json:"expression"`
}

type subjectRef struct {
	User     string       `json:"user,omitempty"`
	Team     string       `json:"team,omitempty"`
	Group    string       `json:"group,omitempty"`
	Everyone bool         `json:"everyone,omitempty"`
	Except   []subjectRef `json:"except,omitempty"`
}

func newBundle(revision string) *dataBundle {
	return &dataBundle{
		FormatVersion: 1,
		Revision:      revision,
		NotAfter:      time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
		Subjects:      map[string]subject{},
		Tables:        map[string]map[string]map[string]*tablePolicies{},
	}
}

func (b *dataBundle) table(catalog, schema, name string) *tablePolicies {
	if b.Tables[catalog] == nil {
		b.Tables[catalog] = map[string]map[string]*tablePolicies{}
	}
	if b.Tables[catalog][schema] == nil {
		b.Tables[catalog][schema] = map[string]*tablePolicies{}
	}
	if b.Tables[catalog][schema][name] == nil {
		b.Tables[catalog][schema][name] = &tablePolicies{ColumnMasks: map[string][]columnMask{}}
	}
	return b.Tables[catalog][schema][name]
}

// bundleOptions shapes a data bundle tarball for the fail-closed cases.
type bundleOptions struct {
	unsigned bool            // no .signatures.json
	signWith *rsa.PrivateKey // another key than the one OPA trusts
	tamper   bool            // change data.json after signing
	roots    []string        // manifest roots (default: dataRoot)
}

// build returns the gzipped bundle tarball.
func (b *dataBundle) build(key *rsa.PrivateKey, opts bundleOptions) ([]byte, error) {
	data, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	roots := opts.roots
	if roots == nil {
		roots = []string{dataRoot}
	}
	manifest, err := json.Marshal(map[string]any{"revision": b.Revision, "roots": roots})
	if err != nil {
		return nil, err
	}
	dataPath := dataRoot + "/data.json"
	files := map[string][]byte{".manifest": manifest, dataPath: data}
	if !opts.unsigned {
		signer := key
		if opts.signWith != nil {
			signer = opts.signWith
		}
		signatures, signErr := signBundle(files, signer)
		if signErr != nil {
			return nil, signErr
		}
		files[".signatures.json"] = signatures
	}
	if opts.tamper {
		files[dataPath] = bytes.Replace(data, []byte(`"revision"`), []byte(`"revision_" : 0, "revision"`), 1)
	}
	return writeTarball(files)
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
	infos := []fileInfo{}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		sum, err := opaFileHash(name, files[name])
		if err != nil {
			return nil, err
		}
		infos = append(infos, fileInfo{Name: name, Hash: sum, Algorithm: "SHA-256"})
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"files": infos})
	token.Header["kid"] = "baleia"
	signed, err := token.SignedString(key)
	if err != nil {
		return nil, fmt.Errorf("signing the bundle: %w", err)
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
		return "", fmt.Errorf("parsing %s: %w", name, err)
	}
	if err := hashWalk(h, value); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashWalk(h interface{ Write([]byte) (int, error) }, v any) error {
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

func hashPrimitive(h interface{ Write([]byte) (int, error) }, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	h.Write(bytes.TrimRight(buf.Bytes(), "\n"))
	return nil
}

const bundlePath = "/usr/share/nginx/html/opa/bundles/baleia.tar.gz"

// serve uploads raw bytes as the bundle the bundle server returns.
func serve(t testing.TB, tarball []byte) {
	t.Helper()
	script := fmt.Sprintf("mkdir -p %[1]s && cat > %[1]s/.next && mv %[1]s/.next %[2]s", filepath.Dir(bundlePath), bundlePath)
	if _, err := kubectl(t.Context(), tarball, "exec", "-i", "deploy/bundles", "--", "sh", "-ec", script); err != nil {
		t.Fatal(err)
	}
}

// serveNothing makes the bundle server answer 404.
func serveNothing(t testing.TB) {
	t.Helper()
	mustKubectl(t, "exec", "deploy/bundles", "--", "rm", "-f", bundlePath)
}

// publish signs b with the harness key, serves it and waits until every
// given OPA reports it active.
func publish(t testing.TB, b *dataBundle, deployments ...string) {
	t.Helper()
	tarball, err := b.build(harness.signingKey, bundleOptions{})
	if err != nil {
		t.Fatal(err)
	}
	serve(t, tarball)
	for _, d := range deployments {
		waitActive(t, d, b.Revision)
	}
}

// opaStatus is the part of GET /v1/status the suite reads.
type opaStatus struct {
	Result struct {
		Bundles map[string]struct {
			ActiveRevision string    `json:"active_revision"`
			Code           string    `json:"code"`
			Message        string    `json:"message"`
			LastRequest    time.Time `json:"last_request"`
			Errors         []struct {
				Message string `json:"message"`
			} `json:"errors"`
		} `json:"bundles"`
	} `json:"result"`
}

func readStatus(t testing.TB, addr string) (opaStatus, error) {
	t.Helper()
	var status opaStatus
	resp, err := http.Get("http://" + addr + "/v1/status")
	if err != nil {
		return status, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return status, fmt.Errorf("status: HTTP %d", resp.StatusCode)
	}
	return status, json.NewDecoder(resp.Body).Decode(&status)
}

// waitActive waits until the deployment's OPA reports revision as the active
// data bundle.
func waitActive(t testing.TB, deployment, revision string) {
	t.Helper()
	addr := forward(t, "pod/"+podName(t, deployment), 8181)
	err := eventually(t.Context(), 2*time.Minute, func() error {
		status, err := readStatus(t, addr)
		if err != nil {
			return err
		}
		got := status.Result.Bundles["baleia"]
		if got.ActiveRevision != revision {
			return fmt.Errorf("%s: active revision %q (%s %s), want %q", deployment, got.ActiveRevision, got.Code, got.Message, revision)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
