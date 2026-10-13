package catalogs

import (
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestProject_PolarisBuildsPolarisShape verifies that the properties map
// produced by Project() for an Iceberg REST (Polaris) catalog has exactly
// the eight keys Trino expects, with vended-credentials and fs.s3.enabled
// both enabled, and that the version hash over those properties reproduces
// the value captured from a running Trino 482 container for the same inputs.
//
// After I1 (CR-017), Project returns a Projection{Row, SecretKeys} — the
// Row carries the REAL secret (the registry stores real values so the plugin
// can load them at boot), and SecretKeys lists the keys the DDL generator
// must replace with @baleia-secret[...] placeholders in the CREATE CATALOG text.
func TestProject_PolarisBuildsPolarisShape(t *testing.T) {
	conn := db.CatalogConnection{
		ID:             pgtype.UUID{Valid: true},
		Name:           "pol-prod",
		Type:           "polaris",
		Url:            "http://polaris:8181",
		CredentialsEnc: strPtr("enc-not-used-here"), // not used: we pass creds directly
	}
	clusterID := pgtype.UUID{Bytes: [16]byte{1, 2, 3, 4}, Valid: true}
	cat := db.Catalog{
		ID:               pgtype.UUID{Valid: true},
		ConnectionID:     pgtype.UUID{Valid: true},
		Name:             "pol-prod",
		TrinoCatalogName: "baleia_polaris",
		ClusterID:        clusterID,
		Warehouse:        "baleia",
		Connector:        "iceberg",
	}
	creds := Credentials{ClientID: "abc", ClientSecret: "def"}

	proj, err := Project(conn, cat, creds)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	row := proj.Row

	if row.CatalogName != "baleia_polaris" || row.ConnectorName != "iceberg" {
		t.Fatalf("got row %+v", row)
	}

	// The registry stores the REAL credential — not a placeholder.
	// Placeholder substitution is the DDL generator's job (internal/trino).
	expectedProps := map[string]string{
		"iceberg.catalog.type":                          "rest",
		"iceberg.rest-catalog.uri":                      "http://polaris:8181/api/catalog",
		"iceberg.rest-catalog.warehouse":                "baleia",
		"iceberg.rest-catalog.security":                 "OAUTH2",
		"iceberg.rest-catalog.oauth2.credential":         "abc:def",
		"iceberg.rest-catalog.oauth2.scope":              "PRINCIPAL_ROLE:ALL",
		"iceberg.rest-catalog.vended-credentials-enabled": "true",
		"fs.s3.enabled":                                  "true",
	}
	for k, want := range expectedProps {
		if got, ok := row.Properties[k]; !ok || got != want {
			t.Errorf("prop %q = %q; want %q (present=%v)", k, got, want, ok)
		}
	}
	if len(row.Properties) != len(expectedProps) {
		t.Errorf("got %d props; want %d (%v)", len(row.Properties), len(expectedProps), row.Properties)
	}

	// The DDL generator must redact exactly the oauth2.credential.
	wantSecretKeys := []string{"iceberg.rest-catalog.oauth2.credential"}
	if len(proj.SecretKeys) != len(wantSecretKeys) {
		t.Fatalf("got %d SecretKeys; want %d (%v)", len(proj.SecretKeys), len(wantSecretKeys), proj.SecretKeys)
	}
	for i, k := range wantSecretKeys {
		if proj.SecretKeys[i] != k {
			t.Errorf("SecretKeys[%d] = %q; want %q", i, proj.SecretKeys[i], k)
		}
	}

	// Version hash over the produced properties must match the golden value
	// captured from Trino 482, because Project() builds exactly the
	// eight-property map used in that golden sample.
	//
	// Captured from a running Trino 482 container with the exact 8-property
	// map produced by Project() for a Polaris catalog (Golden2.java).
	gotVersion := Version(row.CatalogName, row.ConnectorName, row.Properties)
	wantVersion := "d0e1bd0910fac8707c67296aa19bffbfa2740e4bbe578b4b15aa29897a9aa2a9"
	if gotVersion != wantVersion {
		t.Errorf("Version mismatch:\n got %q\n want %q", gotVersion, wantVersion)
	}
}

// TestProject_RejectsReservedName verifies the validation guards against
// catalog names that collide with Trino built-ins and the plugin's reserved
// secret origins ("env", "file").
func TestProject_RejectsReservedName(t *testing.T) {
	for _, name := range []string{"system", "jmx", "tpch", "tpcds", "memory", "env", "file"} {
		t.Run(name, func(t *testing.T) {
			conn := db.CatalogConnection{Type: "polaris"}
			cat := db.Catalog{
				TrinoCatalogName: name,
				Connector:        "iceberg",
				ClusterID:        pgtype.UUID{Valid: true, Bytes: [16]byte{1}},
			}
			if _, err := Project(conn, cat, Credentials{}); err == nil {
				t.Fatalf("Project accepted reserved name %q", name)
			}
		})
	}
}

// TestProject_RejectsInvalidName enforces the regex CHECK.
func TestProject_RejectsInvalidName(t *testing.T) {
	conn := db.CatalogConnection{Type: "polaris"}
	cat := db.Catalog{
		TrinoCatalogName: "Baleia_Polaris", // uppercase, not allowed by Trino
		Connector:        "iceberg",
		ClusterID:         pgtype.UUID{Valid: true, Bytes: [16]byte{1}},
	}
	if _, err := Project(conn, cat, Credentials{}); err == nil {
		t.Fatal("Project accepted uppercase name")
	}
}

// TestProject_RejectsMissingCluster.
func TestProject_RejectsMissingCluster(t *testing.T) {
	conn := db.CatalogConnection{Type: "polaris"}
	cat := db.Catalog{
		TrinoCatalogName: "x",
		Connector:        "iceberg",
		ClusterID:        pgtype.UUID{Valid: false},
	}
	if _, err := Project(conn, cat, Credentials{}); err == nil {
		t.Fatal("Project accepted missing cluster_id")
	}
}

// TestProject_RegistryCarriesRealSecret verifies the I1 invariant: the
// registry row ALWAYS carries the real secret, regardless of how the DDL
// generator will redact it later. The old `redactSecrets bool` flag would
// write the placeholder into the registry and break boot — this test pins
// the fix.
func TestProject_RegistryCarriesRealSecret(t *testing.T) {
	conn := db.CatalogConnection{Type: "polaris", Url: "http://polaris:8181"}
	cat := db.Catalog{
		TrinoCatalogName: "baleia_polaris",
		Connector:        "iceberg",
		Warehouse:        "baleia",
		ClusterID:         pgtype.UUID{Valid: true, Bytes: [16]byte{1}},
	}
	creds := Credentials{ClientID: "abc", ClientSecret: "def"}

	proj, err := Project(conn, cat, creds)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}

	// The registry row must carry the REAL credential. The plugin's
	// SecretResolver would reject a stored placeholder as a circular
	// reference (CR-015 §2.3 D4b); if the registry has the placeholder,
	// boot fails.
	got := proj.Row.Properties["iceberg.rest-catalog.oauth2.credential"]
	want := "abc:def"
	if got != want {
		t.Errorf("registry credential = %q; want REAL value %q (placeholder must NEVER be in registry)",
			got, want)
	}

	// SecretKeys is the only channel through which the DDL generator
	// learns which keys to redact. Without it, secrets would leak in clear
	// into system.runtime.queries (the Trino Web UI equivalent).
	if len(proj.SecretKeys) == 0 {
		t.Fatal("SecretKeys is empty; DDL generator has no way to know which keys to redact")
	}
}

func strPtr(s string) *string { return &s }
func testKeyring(t *testing.T) *crypto.Keyring {
	t.Helper()
	kr, err := crypto.NewKeyring("test-key-0123456789abcdef0123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

// TestDecodeCredentials_Unreadable: any envelope that cannot be opened yields a
// typed error and zero credentials; the stored value is never used as plaintext.
func TestDecodeCredentials_Unreadable(t *testing.T) {
	kr := testKeyring(t)
	id := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	other := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	sealed, err := kr.Seal([]byte(`{"client_secret":"s3cr3t"}`), CredentialsAAD(id))
	if err != nil {
		t.Fatal(err)
	}
	legacy := "bGVnYWN5LWNpcGhlcnRleHQ"

	cases := map[string]struct {
		conn db.CatalogConnection
		kr   *crypto.Keyring
	}{
		"legacy format":           {db.CatalogConnection{ID: id, CredentialsEnc: &legacy}, kr},
		"envelope of another row": {db.CatalogConnection{ID: other, CredentialsEnc: &sealed}, kr},
		"no keyring configured":   {db.CatalogConnection{ID: id, CredentialsEnc: &sealed}, nil},
	}
	for name, tc := range cases {
		got, err := DecodeCredentials(tc.conn, tc.kr)
		if !errors.Is(err, ErrCredentialsUnreadable) {
			t.Errorf("%s: err = %v, want ErrCredentialsUnreadable", name, err)
		}
		if got != (Credentials{}) {
			t.Errorf("%s: returned credentials %+v alongside an error", name, got)
		}
	}

	good, err := DecodeCredentials(db.CatalogConnection{ID: id, CredentialsEnc: &sealed}, kr)
	if err != nil || good.ClientSecret != "s3cr3t" {
		t.Fatalf("DecodeCredentials = %+v, %v", good, err)
	}
	none, err := DecodeCredentials(db.CatalogConnection{ID: id}, nil)
	if err != nil || none != (Credentials{}) {
		t.Fatalf("no credentials stored: %+v, %v", none, err)
	}
}
