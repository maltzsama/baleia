package migrate

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/testdb"
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

	upTo(t, db, dir, 79)

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

// TestWorkspaceCatalogScope pins ADR 0018 §4: baleia_workspace_catalog_ids
// returns exactly the enabled catalogs granted to a workspace — the owner's
// own catalogs no longer auto-reach, and a disabled catalog never counts.
func TestWorkspaceCatalogScope(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	one := func(q string, args ...any) string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, q, args...).Scan(&s); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return s
	}

	user := one(`INSERT INTO users (name, email, password_hash) VALUES ('owner', 'owner@test', 'x') RETURNING id::text`)
	conn := one(`INSERT INTO catalog_connections (name, type, url, created_by) VALUES ('c', 'polaris', 'http://p', $1::uuid) RETURNING id::text`, user)
	lake := one(`INSERT INTO catalogs (connection_id, name, origin) VALUES ($1::uuid, 'lake', 'native') RETURNING id::text`, conn)
	one(`INSERT INTO catalogs (connection_id, name, origin) VALUES ($1::uuid, 'off', 'native') RETURNING id::text`, conn)
	if _, err := pool.Exec(ctx, `UPDATE catalogs SET enabled = false WHERE name = 'off'`); err != nil {
		t.Fatal(err)
	}
	ws := one(`INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ('ws', 'personal', 'ws_polaris', $1::uuid) RETURNING id::text`, user)
	if _, err := pool.Exec(ctx, `INSERT INTO workspace_catalog_grants (workspace_id, catalog_id, granted_by) VALUES ($1::uuid, $2::uuid, $3::uuid)`, ws, lake, user); err != nil {
		t.Fatal(err)
	}

	got := one(`SELECT coalesce(string_agg(id::text, ',' ORDER BY id::text), '') FROM (SELECT baleia_workspace_catalog_ids($1::uuid) AS id) s`, ws)
	if got != lake {
		t.Fatalf("scope = %q, want only the granted enabled catalog %q", got, lake)
	}
}
