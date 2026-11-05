package migrate

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/testdb"
)

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
