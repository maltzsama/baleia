package migrate

import (
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	versionBeforeSemantic = 36
	versionSemantic       = 37
)

// TestSemanticLayerUpDown: the semantic-layer migration applies, reverts and
// applies again on a database that already has content.
func TestSemanticLayerUpDown(t *testing.T) {
	db, dir := testDB(t, versionSemantic)

	hasTable := func(name string) bool {
		t.Helper()
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+name).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		return exists
	}

	for _, table := range []string{"semantic_docs", "semantic_metrics"} {
		if !hasTable(table) {
			t.Fatalf("%s missing after up", table)
		}
	}

	if err := goose.DownTo(db, dir, versionBeforeSemantic); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, table := range []string{"semantic_docs", "semantic_metrics"} {
		if hasTable(table) {
			t.Fatalf("%s still present after down", table)
		}
	}

	if err := goose.UpTo(db, dir, versionSemantic); err != nil {
		t.Fatalf("up again: %v", err)
	}
	for _, table := range []string{"semantic_docs", "semantic_metrics"} {
		if !hasTable(table) {
			t.Fatalf("%s missing after down/up", table)
		}
	}
}

// TestSemanticDocsNaturalKey: docs are unique per workspace/catalog/namespace/
// table/column, with NULL column (table-level) treated as a value.
func TestSemanticDocsNaturalKey(t *testing.T) {
	db, _ := testDB(t, versionSemantic)

	var user string
	if err := db.QueryRow(`INSERT INTO users (name, email, password_hash) VALUES ('u', 'u@x', 'x') RETURNING id::text`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	var ws string
	if err := db.QueryRow(`INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ('w', 'personal', 'p', $1) RETURNING id::text`, user).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	var conn string
	if err := db.QueryRow(`INSERT INTO catalog_connections (name, type, url) VALUES ('c', 'polaris', 'http://p') RETURNING id::text`).Scan(&conn); err != nil {
		t.Fatal(err)
	}
	var catalog string
	if err := db.QueryRow(`INSERT INTO catalogs (connection_id, name, origin) VALUES ($1, 'lake', 'native') RETURNING id::text`, conn).Scan(&catalog); err != nil {
		t.Fatal(err)
	}

	insert := func(column any, description string) error {
		_, err := db.Exec(`INSERT INTO semantic_docs (workspace_id, catalog_id, namespace, table_name, column_name, description, updated_by)
			VALUES ($1, $2, '{sales}', 'orders', $3, $4, $5)`, ws, catalog, column, description, user)
		return err
	}
	if err := insert(nil, "table doc"); err != nil {
		t.Fatal(err)
	}
	if err := insert(nil, "another table doc"); err == nil {
		t.Fatal("a second table-level doc for the same natural key must violate the unique constraint")
	}
	if err := insert("id", "column doc"); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM semantic_docs WHERE table_name = 'orders'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("docs = %d, want 2 (table-level + one column)", n)
	}
}
