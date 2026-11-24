package datapolicy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func untar(t *testing.T, tarball []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[hdr.Name] = content
	}
}

func publishedPolicies(catalogID uuid.UUID) []Policy {
	team := uuid.NewString()
	return []Policy{
		{Name: "eu", Kind: KindRowFilter, Mode: ModePermissive, State: StateEnabled, Validation: ValidationValid,
			Target: Target{CatalogID: catalogID, Schema: "sales", Table: "orders"}, Subjects: []Subject{{Team: team}}, Compiled: "region = 'EU'"},
		{Name: "hide", Kind: KindColumnMask, Priority: 1, State: StateEnabled, Validation: ValidationValid,
			Target: Target{CatalogID: catalogID, Schema: "sales", Table: "orders", Column: "email"}, Subjects: []Subject{{Everyone: true}}, Compiled: "'***'"},
		{Name: "off", Kind: KindRowFilter, Mode: ModePermissive, State: StateDisabled, Validation: ValidationValid,
			Target: Target{CatalogID: catalogID, Schema: "sales", Table: "orders"}, Subjects: []Subject{{Team: team}}, Compiled: "false"},
	}
}

func TestCatalogContentOf(t *testing.T) {
	t.Parallel()
	got := CatalogContentOf([]string{"lake_b", "lake_a"}, publishedPolicies(uuid.New()))
	if !slices.Equal(got.Names, []string{"lake_a", "lake_b"}) {
		t.Fatalf("names = %v", got.Names)
	}
	orders := got.Tables["sales"]["orders"]
	if orders == nil || len(orders.RowPolicies) != 1 || orders.RowPolicies[0].Using != "region = 'EU'" {
		t.Fatalf("row policies = %+v, want only the enabled one, compiled", orders)
	}
	if masks := orders.ColumnMasks["email"]; len(masks) != 1 || masks[0].Expression != "'***'" || masks[0].Priority != 1 {
		t.Fatalf("masks = %+v", masks)
	}
}

func TestContent_NameConflicts(t *testing.T) {
	t.Parallel()
	c := Content{Catalogs: map[string]CatalogContent{
		"a": {Names: []string{"lake", "sales"}},
		"b": {Names: []string{"lake"}},
		"c": {Names: []string{"other"}},
	}}
	if got := c.NameConflicts(); !slices.Equal(got, []string{"lake"}) {
		t.Fatalf("NameConflicts = %v, want [lake]", got)
	}
	if _, err := Bundle(NewRevision(ReasonPublish, []byte("{}"), time.Now(), nil), c, testKey(t)); err == nil {
		t.Fatal("Bundle built a bundle whose catalogs share a name")
	}
}

func TestBundle(t *testing.T) {
	t.Parallel()
	key := testKey(t)
	catalogID := uuid.New()
	content := Content{
		Catalogs: map[string]CatalogContent{catalogID.String(): CatalogContentOf([]string{"lake"}, publishedPolicies(catalogID))},
		Subjects: map[string]SubjectEntry{"u_ana": {User: "ana", Teams: []string{"t1"}, Groups: []string{}}},
	}
	notAfter := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	rev := NewRevision(ReasonPublish, []byte("{}"), notAfter, nil)

	tarball, err := Bundle(rev, content, key)
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	again, err := Bundle(rev, content, key)
	if err != nil || !bytes.Equal(tarball, again) {
		t.Fatal("the bundle of one revision is not deterministic")
	}

	files := untar(t, tarball)
	var manifest struct {
		Revision string   `json:"revision"`
		Roots    []string `json:"roots"`
	}
	if err := json.Unmarshal(files[".manifest"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Revision != rev.ID.String() || !slices.Equal(manifest.Roots, []string{DataRoot}) {
		t.Fatalf("manifest = %+v", manifest)
	}
	var data map[string]any
	if err := json.Unmarshal(files[DataRoot+"/data.json"], &data); err != nil {
		t.Fatal(err)
	}
	if data["format_version"] != float64(1) || data["revision"] != rev.ID.String() || data["not_after"] != "2030-01-02T03:04:05Z" {
		t.Fatalf("data header = %v %v %v", data["format_version"], data["revision"], data["not_after"])
	}
	tables, _ := data["tables"].(map[string]any)
	if _, ok := tables["lake"]; !ok {
		t.Fatalf("tables are not keyed by the engine catalog name: %v", tables)
	}

	// The signature verifies with the public key and covers every other file.
	var sigs struct {
		Signatures []string `json:"signatures"`
	}
	if err := json.Unmarshal(files[".signatures.json"], &sigs); err != nil || len(sigs.Signatures) != 1 {
		t.Fatalf("signatures = %s, %v", files[".signatures.json"], err)
	}
	parsed, err := jwt.Parse(sigs.Signatures[0], func(tok *jwt.Token) (any, error) { return &key.PublicKey, nil },
		jwt.WithValidMethods([]string{"RS256"}))
	if err != nil || parsed.Header["kid"] != SigningKeyID {
		t.Fatalf("signature: %v, kid %v", err, parsed.Header["kid"])
	}
	claims, _ := parsed.Claims.(jwt.MapClaims)
	listed, _ := claims["files"].([]any)
	if len(listed) != 2 {
		t.Fatalf("signed files = %v, want the manifest and data.json", listed)
	}
	for _, f := range listed {
		entry, _ := f.(map[string]any)
		name, _ := entry["name"].(string)
		want, err := opaFileHash(name, files[name])
		if err != nil || entry["hash"] != want {
			t.Fatalf("hash of %s = %v, want %s (%v)", name, entry["hash"], want, err)
		}
	}

	other := testKey(t)
	if _, err := jwt.Parse(sigs.Signatures[0], func(*jwt.Token) (any, error) { return &other.PublicKey, nil }); err == nil {
		t.Fatal("the signature verified with another key")
	}
	if _, err := Bundle(rev, content, nil); err == nil {
		t.Fatal("Bundle signed without a key")
	}
}

// TestOPAFileHash pins OPA's canonical JSON hashing: key order and spacing do
// not change the hash.
func TestOPAFileHash(t *testing.T) {
	t.Parallel()
	a, err := opaFileHash("x.json", []byte(`{"b":1,"a":[1,"<"]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := opaFileHash("x.json", []byte(`{ "a" : [1, "<"], "b" : 1 }`))
	if err != nil || a != b {
		t.Fatalf("canonical hashes differ: %s %s %v", a, b, err)
	}
	raw, err := opaFileHash("policy.rego", []byte("package x"))
	if err != nil || raw == a {
		t.Fatalf("raw file hash = %s, %v", raw, err)
	}
}

func TestParseSigningKey(t *testing.T) {
	t.Parallel()
	key := testKey(t)
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	for name, text := range map[string][]byte{"pkcs1": pkcs1, "pkcs8": pkcs8} {
		got, err := ParseSigningKey(string(text))
		if err != nil || !got.Equal(key) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := ParseSigningKey("not a key"); err == nil {
		t.Error("parsed text without a PEM block")
	}
}
