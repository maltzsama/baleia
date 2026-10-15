//go:build contract

// Package contract tests Baleia's half of the registry contract against the
// fixtures of the plugin release it pins (#39): make contract-fetch extracts
// contract/ from that release into testdata/, so the fixtures are never
// copied by hand and a change on either side fails here.
package contract

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/trino"
)

func fixture(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "contract", name))
	if err != nil {
		t.Fatalf("%v (run make contract-fetch)", err)
	}
	if v == nil {
		return
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

func TestVersionVectors(t *testing.T) {
	var vectors []struct {
		Name         string            `json:"name"`
		Connector    string            `json:"connector"`
		Properties   map[string]string `json:"properties"`
		ExpectedHash string            `json:"expected_hash"`
	}
	fixture(t, "catalog_version_vectors.json", &vectors)
	if len(vectors) == 0 {
		t.Fatal("no vectors")
	}
	for _, v := range vectors {
		if got := bindtrino.Version(v.Name, v.Connector, v.Properties); got != v.ExpectedHash {
			t.Errorf("%s: %s, plugin says %s", v.Name, got, v.ExpectedHash)
		}
	}
}

func TestPlaceholderCases(t *testing.T) {
	var cases []struct {
		Value   string `json:"value"`
		Matches bool   `json:"matches"`
		Origin  string `json:"origin"`
		Key     string `json:"key"`
	}
	fixture(t, "placeholder_cases.json", &cases)
	reserved := bindtrino.New(nil, nil).ReservedNames()
	n := 0
	for _, c := range cases {
		// Baleia only writes catalog-origin placeholders; env and file origins
		// are reserved catalog names, so a catalog can never collide with them.
		if !c.Matches || slices.Contains(reserved, c.Origin) {
			continue
		}
		n++
		if got := trino.SecretPlaceholder(c.Origin, c.Key); got != c.Value {
			t.Errorf("SecretPlaceholder(%q, %q) = %q, plugin expects %q", c.Origin, c.Key, got, c.Value)
		}
	}
	if n == 0 {
		t.Fatal("no catalog-origin cases")
	}
	for _, origin := range []string{"env", "file"} {
		if !slices.Contains(reserved, origin) {
			t.Errorf("%s must be reserved: a catalog named %s would read the plugin's %s secrets", origin, origin, origin)
		}
	}
}

// migratedDB is a throwaway database on BALEIA_TEST_DATABASE_URL with every
// migration applied.
func migratedDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	admin := os.Getenv("BALEIA_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}
	adminDB, err := sql.Open("pgx", admin)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("baleia_contract_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = adminDB.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		adminDB.Close()
	})
	u, _ := url.Parse(admin)
	u.Path = "/" + name
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, filepath.Join("..", "..", "db", "migrations")); err != nil {
		t.Fatal(err)
	}
	return db, u.String()
}

func TestReservedNames(t *testing.T) {
	var want []string
	fixture(t, "reserved_catalog_names.json", &want)
	got := bindtrino.New(nil, nil).ReservedNames()
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("binder reserves %v, plugin %v", got, want)
	}

	db, _ := migratedDB(t)
	cluster := ""
	if err := db.QueryRow(`SELECT id::text FROM trino_clusters WHERE name = 'default'`).Scan(&cluster); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO catalog_connections (id, name, type, url) VALUES ('00000000-0000-0000-0000-0000000000c1', 'c', 'polaris', 'http://p')`); err != nil {
		t.Fatal(err)
	}
	checkViolation := func(err error) bool {
		var pg *pgconn.PgError
		return errors.As(err, &pg) && pg.Code == "23514"
	}
	for _, name := range want {
		_, err := db.Exec(`INSERT INTO catalogs (connection_id, name, trino_catalog_name, cluster_id, warehouse, connector)
			VALUES ('00000000-0000-0000-0000-0000000000c1', $1, $1, $2, 'w', 'iceberg')`, name, cluster)
		if !checkViolation(err) {
			t.Errorf("catalogs accepted the reserved name %q: %v", name, err)
		}
		_, err = db.Exec(`INSERT INTO trino_catalog_registry (cluster_id, catalog_name, connector_name, properties) VALUES ($1, $2, 'iceberg', '{}')`, cluster, name)
		if !checkViolation(err) {
			t.Errorf("trino_catalog_registry accepted the reserved name %q: %v", name, err)
		}
	}
}

var statementRe = regexp.MustCompile(`(?s)-- name: (\w+)\n(.*?);\n`)

// TestRegistrySchema prepares every statement the plugin issues, as the
// plugin's role, against Baleia's migrated schema: a renamed column or a
// missing grant fails here before it fails in Trino.
func TestRegistrySchema(t *testing.T) {
	fixture(t, "registry_schema.sql", nil)
	raw, _ := os.ReadFile(filepath.Join("testdata", "contract", "registry_schema.sql"))
	stmts := statementRe.FindAllStringSubmatch(string(raw), -1)
	if len(stmts) == 0 {
		t.Fatal("no statements in registry_schema.sql")
	}
	db, _ := migratedDB(t)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), `SET ROLE baleia_trino`); err != nil {
		t.Fatal(err)
	}
	for _, s := range stmts {
		name, text := s[1], s[2]
		n := 0
		pg := regexp.MustCompile(`\?`).ReplaceAllStringFunc(text, func(string) string { n++; return fmt.Sprintf("$%d", n) })
		if _, err := conn.ExecContext(t.Context(), "PREPARE contract_"+strings.ToLower(name)+" AS "+pg); err != nil {
			t.Errorf("%s does not prepare as baleia_trino: %v", name, err)
		}
	}
}

// TestRegistryWriteSemantics: only the plugin confirms (updated_by =
// 'trino'); Baleia's queries never claim it, and never write the registry's
// sync columns after the plugin did (#38).
func TestRegistryWriteSemantics(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "db", "queries", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatal("no queries", err)
	}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		q := string(b)
		if strings.Contains(q, "'trino'") && strings.Contains(q, "trino_catalog_registry") &&
			regexp.MustCompile(`updated_by\s*=\s*'trino'|'trino'\s*\)`).MatchString(q) {
			t.Errorf("%s writes updated_by = 'trino'; only the plugin confirms", f)
		}
		if regexp.MustCompile(`(?i)UPDATE\s+trino_catalog_registry[^;]*sync_status\s*=\s*'error'`).MatchString(q) {
			t.Errorf("%s marks registry errors; errors live on the binding (#38)", f)
		}
	}
}
