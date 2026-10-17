package migrate

import (
	"testing"

	"github.com/pressly/goose/v3"
)

const versionContentVersions = 39

func TestContentVersionsUpDown(t *testing.T) {
	db, dir := testDB(t, versionContentVersions)
	hasTable := func() bool {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass('public.content_versions') IS NOT NULL`).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		return exists
	}
	if !hasTable() {
		t.Fatal("content_versions missing after up")
	}
	if err := goose.DownTo(db, dir, versionContentVersions-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if hasTable() {
		t.Fatal("content_versions still present after down")
	}
	if err := goose.UpTo(db, dir, versionContentVersions); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if !hasTable() {
		t.Fatal("content_versions missing after down/up")
	}
}

func TestContentVersionsAppendOnly(t *testing.T) {
	db, _ := testDB(t, versionContentVersions)
	var user, ws, query string
	if err := db.QueryRow(`INSERT INTO users (name, email, password_hash) VALUES ('u', 'u@x', 'x') RETURNING id::text`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ('w', 'personal', 'p', $1) RETURNING id::text`, user).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	var space string
	if err := db.QueryRow(`SELECT id::text FROM spaces WHERE workspace_id = $1 AND is_default`, ws).Scan(&space); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO saved_queries (workspace_id, space_id, created_by, updated_by, name, slug, sql)
		VALUES ($1, $2, $3, $3, 'q', 'q', 'select 1') RETURNING id::text`, ws, space, user).Scan(&query); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO content_versions (workspace_id, space_id, entity_type, entity_id, version, change_kind, snapshot, author_id)
		VALUES ($1, $2, 'saved_query', $3, 1, 'create', '{}', $4)`, ws, space, query, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE content_versions SET change_kind = 'update'`); err == nil {
		t.Fatal("UPDATE on content_versions must fail")
	}
	if _, err := db.Exec(`DELETE FROM content_versions`); err == nil {
		t.Fatal("DELETE on content_versions must fail")
	}
}

func TestWorkspaceDeleteCascadesVersions(t *testing.T) {
	db, _ := testDB(t, versionContentVersions)
	var user, ws, query string
	if err := db.QueryRow(`INSERT INTO users (name, email, password_hash) VALUES ('u', 'u@x', 'x') RETURNING id::text`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ('w', 'personal', 'p', $1) RETURNING id::text`, user).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	var space string
	if err := db.QueryRow(`SELECT id::text FROM spaces WHERE workspace_id = $1 AND is_default`, ws).Scan(&space); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO saved_queries (workspace_id, space_id, created_by, updated_by, name, slug, sql)
		VALUES ($1, $2, $3, $3, 'q', 'q', 'select 1') RETURNING id::text`, ws, space, user).Scan(&query); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO content_versions (workspace_id, space_id, entity_type, entity_id, version, change_kind, snapshot, author_id)
		VALUES ($1, $2, 'saved_query', $3, 1, 'create', '{}', $4)`, ws, space, query, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM workspaces WHERE id = $1`, ws); err != nil {
		t.Fatalf("deleting the workspace must cascade through the append-only trigger: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM content_versions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("versions left after workspace delete: %d", n)
	}
}
