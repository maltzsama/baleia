package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

const (
	versionBeforeEngines = 23
	versionEngines       = 24
)

// testDB creates a throwaway database on the server of
// BALEIA_TEST_DATABASE_URL (a role allowed to CREATE DATABASE) and returns a
// handle to it migrated to version.
func testDB(t *testing.T, version int64) (*sql.DB, string) {
	t.Helper()
	admin := os.Getenv("BALEIA_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}
	adminDB, err := sql.Open("pgx", admin)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("baleia_migtest_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = adminDB.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		adminDB.Close()
	})

	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	dir := migrationsDir(t)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	goose.SetLogger(goose.NopLogger())
	if err := goose.UpTo(db, dir, version); err != nil {
		t.Fatal(err)
	}
	return db, dir
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../../db/migrations")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", strings.SplitN(strings.TrimSpace(q), "\n", 2)[0], err)
	}
}

func scalar[T any](t *testing.T, db *sql.DB, q string, args ...any) T {
	t.Helper()
	var v T
	if err := db.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", strings.SplitN(strings.TrimSpace(q), "\n", 2)[0], err)
	}
	return v
}

// seedLegacy writes what a pre-engines install holds: a user connection named
// like the backfill would name the Trino one, two catalogs on the default
// cluster, and executions against a live and a deleted catalog.
func seedLegacy(t *testing.T, db *sql.DB) { seed(t, db, "trino-default") }

// seed writes the rows of seedLegacy; pgName names the PostgreSQL connection.
func seed(t *testing.T, db *sql.DB, pgName string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO users (id, name, email, password_hash, role) VALUES
		('00000000-0000-0000-0000-0000000000a1', 'Admin', 'a@x', 'h', 'admin')`)
	mustExec(t, db, `INSERT INTO workspaces (id, name, type, polaris_catalog_name, owner_user_id) VALUES
		('00000000-0000-0000-0000-0000000000b1', 'ws', 'personal', 'p1', '00000000-0000-0000-0000-0000000000a1')`)
	mustExec(t, db, `INSERT INTO catalog_connections (id, name, type, url, created_by) VALUES
		('00000000-0000-0000-0000-0000000000c1', 'polaris', 'polaris', 'http://polaris:8181', '00000000-0000-0000-0000-0000000000a1'),
		('00000000-0000-0000-0000-0000000000c2', $1, 'postgresql', 'jdbc:postgresql://h/db', '00000000-0000-0000-0000-0000000000a1')`, pgName)
	mustExec(t, db, `INSERT INTO catalogs (id, connection_id, name, trino_catalog_name, cluster_id, warehouse, connector, enabled)
		SELECT '00000000-0000-0000-0000-0000000000d1', '00000000-0000-0000-0000-0000000000c1', 'lake', 'lake', id, 'wh1', 'iceberg', true FROM trino_clusters WHERE name = 'default'`)
	mustExec(t, db, `INSERT INTO catalogs (id, connection_id, name, trino_catalog_name, cluster_id, warehouse, connector, enabled)
		SELECT '00000000-0000-0000-0000-0000000000d2', '00000000-0000-0000-0000-0000000000c2', 'pg', 'pg', id, 'db', 'postgresql', false FROM trino_clusters WHERE name = 'default'`)
	mustExec(t, db, `INSERT INTO query_executions (id, workspace_id, user_id, status, sql, trino_catalog) VALUES
		('00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-0000000000a1', 'finished', 'SELECT 1', 'lake'),
		('00000000-0000-0000-0000-0000000000e2', '00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-0000000000a1', 'failed', 'SELECT 2', 'gone')`)
}

func TestEnginesBackfill(t *testing.T) {
	db, _ := testDB(t, versionBeforeEngines)
	seedLegacy(t, db)
	registryRows := scalar[int](t, db, `SELECT count(*) FROM trino_catalog_registry`)
	if err := goose.UpTo(db, migrationsDir(t), versionEngines); err != nil {
		t.Fatal(err)
	}

	// The default cluster has a system-owned trino connection and a federated
	// engine; the user's "trino-default" connection forced a suffix.
	var engName, kind, class, identity, connName, connType, connURL string
	var createdByNull bool
	err := db.QueryRow(`
		SELECT e.name, e.kind, e.class, e.identity_mode, c.name, c.type, c.config->>'url', c.created_by IS NULL
		FROM trino_clusters tc JOIN engines e ON e.id = tc.engine_id JOIN catalog_connections c ON c.id = e.connection_id
		WHERE tc.name = 'default'`).Scan(&engName, &kind, &class, &identity, &connName, &connType, &connURL, &createdByNull)
	if err != nil {
		t.Fatal(err)
	}
	if engName != "default" || kind != "trino" || class != "federated" || identity != "impersonation" ||
		connName != "trino-default-2" || connType != "trino" || connURL != "http://trino:8080" || !createdByNull {
		t.Errorf("engine = %s %s %s %s on %s (%s, %s, system=%v)", engName, kind, class, identity, connName, connType, connURL, createdByNull)
	}

	// One managed binding per catalog, carrying its Trino columns.
	type binding struct {
		name, connector, mode string
		enabled               bool
	}
	got := map[string]binding{}
	rows, err := db.Query(`SELECT c.name, b.engine_catalog_name, b.connector, b.mode, b.enabled FROM catalog_bindings b JOIN catalogs c ON c.id = b.catalog_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var cat string
		var b binding
		if err := rows.Scan(&cat, &b.name, &b.connector, &b.mode, &b.enabled); err != nil {
			t.Fatal(err)
		}
		got[cat] = b
	}
	rows.Close()
	if len(got) != 2 || got["lake"] != (binding{"lake", "iceberg", "managed", true}) || got["pg"] != (binding{"pg", "postgresql", "managed", false}) {
		t.Errorf("bindings = %+v", got)
	}
	if ref := scalar[string](t, db, `SELECT source_ref FROM catalogs WHERE name = 'lake'`); ref != "wh1" {
		t.Errorf("source_ref = %q", ref)
	}

	// Executions: the live catalog resolves to its binding; the deleted one
	// keeps a snapshot only.
	var bindingSet, catalogSet, engineSet bool
	var snapshot string
	if err := db.QueryRow(`SELECT target_binding_id IS NOT NULL, target_catalog_id IS NOT NULL, engine_id IS NOT NULL, target_snapshot::text
		FROM query_executions WHERE trino_catalog = 'lake'`).Scan(&bindingSet, &catalogSet, &engineSet, &snapshot); err != nil {
		t.Fatal(err)
	}
	if !bindingSet || !catalogSet || !engineSet || !strings.Contains(snapshot, `"engine_name": "default"`) || !strings.Contains(snapshot, `"catalog_name": "lake"`) {
		t.Errorf("live execution: binding=%v catalog=%v engine=%v snapshot=%s", bindingSet, catalogSet, engineSet, snapshot)
	}
	if err := db.QueryRow(`SELECT target_binding_id IS NOT NULL, target_snapshot::text FROM query_executions WHERE trino_catalog = 'gone'`).Scan(&bindingSet, &snapshot); err != nil {
		t.Fatal(err)
	}
	if bindingSet || !strings.Contains(snapshot, `"engine_catalog": "gone"`) {
		t.Errorf("orphan execution: binding=%v snapshot=%s", bindingSet, snapshot)
	}
	if strings.Contains(strings.ToLower(snapshot), "secret") || strings.Contains(snapshot, "password") {
		t.Errorf("snapshot carries credentials: %s", snapshot)
	}

	if n := scalar[int](t, db, `SELECT count(*) FROM trino_catalog_registry`); n != registryRows {
		t.Errorf("registry rows changed: %d → %d", registryRows, n)
	}
}

// TestManagedBindingFollowsCatalogWrites: until the handlers write bindings,
// the transitional trigger keeps the managed binding in step with catalogs.
func TestManagedBindingFollowsCatalogWrites(t *testing.T) {
	db, _ := testDB(t, versionEngines)
	seed(t, db, "pg") // the migration already created trino-default

	q := func() (name, connector string, enabled bool, count int) {
		count = scalar[int](t, db, `SELECT count(*) FROM catalog_bindings WHERE catalog_id = '00000000-0000-0000-0000-0000000000d1'`)
		if count == 1 {
			if err := db.QueryRow(`SELECT engine_catalog_name, connector, enabled FROM catalog_bindings WHERE catalog_id = '00000000-0000-0000-0000-0000000000d1'`).Scan(&name, &connector, &enabled); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	if name, conn, en, n := q(); n != 1 || name != "lake" || conn != "iceberg" || !en {
		t.Fatalf("after insert: %s %s %v (%d)", name, conn, en, n)
	}
	mustExec(t, db, `UPDATE catalogs SET trino_catalog_name = 'lake2', enabled = false, warehouse = 'wh2' WHERE id = '00000000-0000-0000-0000-0000000000d1'`)
	if name, _, en, n := q(); n != 1 || name != "lake2" || en {
		t.Errorf("after rename: %s %v (%d)", name, en, n)
	}
	if ref := scalar[string](t, db, `SELECT source_ref FROM catalogs WHERE id = '00000000-0000-0000-0000-0000000000d1'`); ref != "wh2" {
		t.Errorf("source_ref = %q", ref)
	}
	mustExec(t, db, `DELETE FROM catalogs WHERE id = '00000000-0000-0000-0000-0000000000d1'`)
	if _, _, _, n := q(); n != 0 {
		t.Errorf("binding survived its catalog")
	}
}

func TestEnginesDownUp(t *testing.T) {
	db, dir := testDB(t, versionBeforeEngines)
	seedLegacy(t, db)
	if err := goose.UpTo(db, dir, versionEngines); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownTo(db, dir, versionBeforeEngines); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"engines", "catalog_bindings"} {
		if scalar[bool](t, db, `SELECT to_regclass($1) IS NOT NULL`, tbl) {
			t.Errorf("%s survived down", tbl)
		}
	}
	if n := scalar[int](t, db, `SELECT count(*) FROM catalog_connections WHERE type = 'trino'`); n != 0 {
		t.Errorf("%d backfilled trino connections survived down", n)
	}
	if n := scalar[int](t, db, `SELECT count(*) FROM catalog_connections`); n != 2 {
		t.Errorf("user connections after down = %d, want 2", n)
	}
	if n := scalar[int](t, db, `SELECT count(*) FROM catalogs`); n != 2 {
		t.Errorf("catalogs after down = %d, want 2", n)
	}
	if err := goose.UpTo(db, dir, versionEngines); err != nil {
		t.Fatal(err)
	}
	if n := scalar[int](t, db, `SELECT count(*) FROM catalog_bindings`); n != 2 {
		t.Errorf("bindings after down/up = %d, want 2", n)
	}
}

// TestPluginContractUnchanged: the plugin reads trino_clusters and owns the
// registry as baleia_trino; both still work after the migration.
func TestPluginContractUnchanged(t *testing.T) {
	db, _ := testDB(t, versionEngines)
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET ROLE baleia_trino`); err != nil {
		t.Skipf("cannot SET ROLE baleia_trino with this test user: %v", err)
	}
	var id, name, coord string
	if err := conn.QueryRowContext(ctx, `SELECT id, name, coordinator_url FROM trino_clusters WHERE name = 'default'`).Scan(&id, &name, &coord); err != nil {
		t.Fatalf("baleia_trino cannot read trino_clusters: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT cluster_id, catalog_name, connector_name, properties, catalog_version, enabled FROM trino_catalog_registry`); err != nil {
		t.Fatalf("baleia_trino cannot read the registry: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `RESET ROLE`); err != nil {
		t.Fatal(err)
	}
}

const versionNativeCatalogs = 25

func TestNativeCatalogs(t *testing.T) {
	db, dir := testDB(t, versionNativeCatalogs)
	seed(t, db, "pg")

	// A native catalog needs none of the Trino columns; a source one still does.
	mustExec(t, db, `INSERT INTO catalogs (id, connection_id, name, origin) VALUES
		('00000000-0000-0000-0000-0000000000f1', '00000000-0000-0000-0000-0000000000c2', 'sales', 'native')`)
	if _, err := db.Exec(`INSERT INTO catalogs (connection_id, name) VALUES ('00000000-0000-0000-0000-0000000000c2', 'broken')`); err == nil {
		t.Error("a source catalog without Trino columns was accepted")
	}
	if n := scalar[int](t, db, `SELECT count(*) FROM catalog_bindings WHERE catalog_id = '00000000-0000-0000-0000-0000000000f1'`); n != 0 {
		t.Errorf("the transitional trigger bound a native catalog (%d bindings)", n)
	}

	if err := goose.DownTo(db, dir, versionEngines); err != nil {
		t.Fatal(err)
	}
	if n := scalar[int](t, db, `SELECT count(*) FROM catalogs WHERE origin = 'native'`); n != 0 {
		t.Errorf("%d native catalogs survived down", n)
	}
	if n := scalar[int](t, db, `SELECT count(*) FROM catalogs`); n != 2 {
		t.Errorf("source catalogs after down = %d, want 2", n)
	}
	if err := goose.UpTo(db, dir, versionNativeCatalogs); err != nil {
		t.Fatal(err)
	}
}

const versionVocabulary = 26

func TestEngineVocabulary(t *testing.T) {
	db, dir := testDB(t, versionNativeCatalogs)
	seed(t, db, "pg")
	mustExec(t, db, `INSERT INTO query_statements (execution_id, position, sql, status, trino_query_id, rows_returned, result_key, result_bytes, truncated)
		VALUES ('00000000-0000-0000-0000-0000000000e1', 1, 'SELECT 1', 'finished', 'q1', 3, 'query-results/x/1/0.arrow', 99, true),
		       ('00000000-0000-0000-0000-0000000000e1', 2, 'CREATE TABLE t (a int)', 'finished', 'q2', 0, NULL, NULL, false)`)
	if err := goose.UpTo(db, dir, versionVocabulary); err != nil {
		t.Fatal(err)
	}
	got := scalar[string](t, db, `SELECT result_sets::text FROM query_statements WHERE position = 1`)
	if !strings.Contains(got, `"result_key": "query-results/x/1/0.arrow"`) || !strings.Contains(got, `"rows": 3`) || !strings.Contains(got, `"truncated": true`) {
		t.Errorf("backfilled result_sets = %s", got)
	}
	if got := scalar[string](t, db, `SELECT result_sets::text FROM query_statements WHERE position = 2`); got != "[]" {
		t.Errorf("a statement without results: %s", got)
	}
	if got := scalar[string](t, db, `SELECT engine_query_id FROM query_statements WHERE position = 1`); got != "q1" {
		t.Errorf("engine_query_id = %q", got)
	}
	if err := goose.DownTo(db, dir, versionNativeCatalogs); err != nil {
		t.Fatal(err)
	}
	if got := scalar[string](t, db, `SELECT trino_query_id FROM query_statements WHERE position = 1`); got != "q1" {
		t.Errorf("trino_query_id after down = %q", got)
	}
	if err := goose.UpTo(db, dir, versionVocabulary); err != nil {
		t.Fatal(err)
	}
}
