package migrate

import (
	"testing"

	"github.com/pressly/goose/v3"
)

// TestWorkspaceCatalogScopeBackfill: migrating from 00078 to 00079 turns the
// personal auto-reach into an explicit grant, and only for the pairs the rule
// reached (ADR 0018 §4.3).
func TestWorkspaceCatalogScopeBackfill(t *testing.T) {
	db, dir := testDB(t, 78)

	user := scalar[string](t, db, `INSERT INTO users (name, email, password_hash) VALUES ('o', 'o@x', 'x') RETURNING id::text`)
	conn := scalar[string](t, db, `INSERT INTO catalog_connections (name, type, url, created_by) VALUES ('c', 'polaris', 'http://p', $1::uuid) RETURNING id::text`, user)
	cat := scalar[string](t, db, `INSERT INTO catalogs (connection_id, name, origin) VALUES ($1::uuid, 'lake', 'native') RETURNING id::text`, conn)
	ws := scalar[string](t, db, `INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ('w', 'personal', 'p', $1::uuid) RETURNING id::text`, user)
	// A stranger's personal workspace must not get the grant.
	other := scalar[string](t, db, `INSERT INTO users (name, email, password_hash) VALUES ('s', 's@x', 'x') RETURNING id::text`)
	otherWS := scalar[string](t, db, `INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ('w2', 'personal', 'p2', $1::uuid) RETURNING id::text`, other)

	if err := goose.UpTo(db, dir, 79); err != nil {
		t.Fatal(err)
	}

	count := func(workspace, catalog string) int {
		return scalar[int](t, db, `SELECT count(*) FROM workspace_catalog_grants WHERE workspace_id = $1::uuid AND catalog_id = $2::uuid`, workspace, catalog)
	}
	if count(ws, cat) != 1 {
		t.Fatalf("owner's personal workspace did not get the backfilled grant")
	}
	if count(otherWS, cat) != 0 {
		t.Fatalf("a stranger's workspace got the owner's catalog grant")
	}
}
