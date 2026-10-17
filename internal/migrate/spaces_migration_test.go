package migrate

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	versionBeforeSpaces = 37
	versionSpaces       = 38
)

func TestSpacesUpDown(t *testing.T) {
	db, dir := testDB(t, versionSpaces)

	hasTable := func(name string) bool {
		t.Helper()
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+name).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		return exists
	}
	for _, table := range []string{"spaces", "space_members"} {
		if !hasTable(table) {
			t.Fatalf("%s missing after up", table)
		}
	}

	if err := goose.DownTo(db, dir, versionBeforeSpaces); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, table := range []string{"spaces", "space_members"} {
		if hasTable(table) {
			t.Fatalf("%s still present after down", table)
		}
	}

	if err := goose.UpTo(db, dir, versionSpaces); err != nil {
		t.Fatalf("up again: %v", err)
	}
	for _, table := range []string{"spaces", "space_members"} {
		if !hasTable(table) {
			t.Fatalf("%s missing after down/up", table)
		}
	}
}

// TestSpacesBackfill migrates a database that already has content (including
// cross-workspace references created through the old API) and proves the
// cleanup, the default space and the slug backfill.
func TestSpacesBackfill(t *testing.T) {
	db, dir := testDB(t, versionBeforeSpaces)

	id := func(q string, args ...any) string {
		t.Helper()
		var s string
		if err := db.QueryRow(q, args...).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	user := id(`INSERT INTO users (name, email, password_hash) VALUES ('u', 'u@x', 'x') RETURNING id::text`)
	ws1 := id(`INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ('one', 'personal', 'p1', $1) RETURNING id::text`, user)
	ws2 := id(`INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ('two', 'personal', 'p2', $1) RETURNING id::text`, user)
	f1 := id(`INSERT INTO query_folders (workspace_id, name) VALUES ($1, 'A') RETURNING id::text`, ws1)
	f2 := id(`INSERT INTO query_folders (workspace_id, name) VALUES ($1, 'B') RETURNING id::text`, ws2)
	f3 := id(`INSERT INTO query_folders (workspace_id, parent_id, name) VALUES ($1, $2, 'C') RETURNING id::text`, ws1, f2)
	q1 := id(`INSERT INTO saved_queries (workspace_id, folder_id, created_by, name, sql) VALUES ($1, $2, $3, 'Revenue', 'select 1') RETURNING id::text`, ws1, f2, user)
	q2 := id(`INSERT INTO saved_queries (workspace_id, created_by, name, sql) VALUES ($1, $2, 'revenue!', 'select 2') RETURNING id::text`, ws1, user)

	if err := goose.UpTo(db, dir, versionSpaces); err != nil {
		t.Fatalf("up: %v", err)
	}

	defaultSpace := func(ws string) string {
		t.Helper()
		var s string
		if err := db.QueryRow(`SELECT id::text FROM spaces WHERE workspace_id = $1 AND is_default`, ws).Scan(&s); err != nil {
			t.Fatalf("default space of %s: %v", ws, err)
		}
		return s
	}
	s1, s2 := defaultSpace(ws1), defaultSpace(ws2)
	for _, ws := range []string{ws1, ws2} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM spaces WHERE workspace_id = $1 AND is_default`, ws).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("workspace %s has %d default spaces, want 1", ws, n)
		}
	}

	var spaceID string
	for _, tc := range []struct{ table, id, want string }{
		{"query_folders", f1, s1}, {"query_folders", f2, s2}, {"query_folders", f3, s1},
		{"saved_queries", q1, s1}, {"saved_queries", q2, s1},
	} {
		if err := db.QueryRow(`SELECT space_id::text FROM `+tc.table+` WHERE id = $1`, tc.id).Scan(&spaceID); err != nil {
			t.Fatal(err)
		}
		if spaceID != tc.want {
			t.Fatalf("%s %s space_id = %s, want %s", tc.table, tc.id, spaceID, tc.want)
		}
	}

	// Cross-workspace references are reset, and the surviving ones stay.
	var parent sql.NullString
	if err := db.QueryRow(`SELECT parent_id::text FROM query_folders WHERE id = $1`, f3).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if parent.Valid {
		t.Fatalf("cross-workspace parent was not reset: %v", parent)
	}
	var folder sql.NullString
	if err := db.QueryRow(`SELECT folder_id::text FROM saved_queries WHERE id = $1`, q1).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if folder.Valid {
		t.Fatalf("cross-workspace folder was not reset: %v", folder)
	}
	// Same-workspace content keeps its folder.
	if err := db.QueryRow(`SELECT folder_id::text FROM saved_queries WHERE id = $1`, q2).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if folder.Valid {
		t.Fatalf("query without a folder gained one: %v", folder)
	}

	// Slugs are non-empty and unique in their scope; the collision gets _2.
	var slug string
	if err := db.QueryRow(`SELECT slug FROM saved_queries WHERE id = $1`, q1).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	if slug != "revenue" {
		t.Fatalf("q1 slug = %q, want revenue", slug)
	}
	if err := db.QueryRow(`SELECT slug FROM saved_queries WHERE id = $1`, q2).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	if slug != "revenue_2" {
		t.Fatalf("q2 slug = %q, want revenue_2", slug)
	}
	var foldersInSpace1, distinctSlugs int
	if err := db.QueryRow(`SELECT count(*), count(DISTINCT slug) FROM query_folders WHERE space_id = $1`, s1).Scan(&foldersInSpace1, &distinctSlugs); err != nil {
		t.Fatal(err)
	}
	if foldersInSpace1 != distinctSlugs {
		t.Fatalf("folder slugs not unique: %d folders, %d slugs", foldersInSpace1, distinctSlugs)
	}
}

func TestSpacesDefaultSpaceAndEffectiveRole(t *testing.T) {
	db, _ := testDB(t, versionSpaces)

	id := func(q string, args ...any) string {
		t.Helper()
		var s string
		if err := db.QueryRow(q, args...).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	role := func(ws, space, user string) sql.NullString {
		t.Helper()
		var r sql.NullString
		if err := db.QueryRow(`SELECT baleia_effective_space_role($1::uuid, $2::uuid, $3::uuid)`, ws, space, user).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return r
	}

	owner := id(`INSERT INTO users (name, email, password_hash) VALUES ('o', 'o@x', 'x') RETURNING id::text`)
	member := id(`INSERT INTO users (name, email, password_hash) VALUES ('m', 'm@x', 'x') RETURNING id::text`)
	outsider := id(`INSERT INTO users (name, email, password_hash) VALUES ('x', 'x@x', 'x') RETURNING id::text`)
	team := id(`INSERT INTO teams (name, created_by) VALUES ('t', $1) RETURNING id::text`, owner)
	exec(`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'owner'), ($1, $3, 'member')`, team, owner, member)
	ws := id(`INSERT INTO workspaces (name, type, polaris_catalog_name, owner_team_id) VALUES ('w', 'shared', 'p', $1) RETURNING id::text`, team)

	// The trigger creates exactly one default space, slugged 'general'.
	var n int
	var defaultSpace string
	if err := db.QueryRow(`SELECT count(*), max(id::text) FROM spaces WHERE workspace_id = $1 AND is_default`, ws).Scan(&n, &defaultSpace); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("default spaces = %d, want 1", n)
	}
	var slug string
	if err := db.QueryRow(`SELECT slug FROM spaces WHERE id = $1`, defaultSpace).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	if slug != "general" {
		t.Fatalf("default slug = %q, want general", slug)
	}

	if got := role(ws, defaultSpace, owner); !got.Valid || got.String != "manager" {
		t.Fatalf("team owner role = %v, want manager", got)
	}
	if got := role(ws, defaultSpace, member); !got.Valid || got.String != "editor" {
		t.Fatalf("member role = %v, want editor", got)
	}
	if got := role(ws, defaultSpace, outsider); got.Valid {
		t.Fatalf("outsider role = %v, want none", got)
	}

	// A restricted space is invisible to a workspace member without an entry.
	restricted := id(`INSERT INTO spaces (workspace_id, name, slug, visibility, default_role) VALUES ($1, 'Restricted', 'restricted', 'restricted', 'viewer') RETURNING id::text`, ws)
	if got := role(ws, restricted, member); got.Valid {
		t.Fatalf("member of restricted space = %v, want none", got)
	}
	exec(`INSERT INTO space_members (space_id, principal_type, principal_id, role) VALUES ($1, 'user', $2, 'viewer')`, restricted, member)
	if got := role(ws, restricted, member); !got.Valid || got.String != "viewer" {
		t.Fatalf("explicit viewer = %v, want viewer", got)
	}
	// A team entry raises the same member to manager.
	exec(`INSERT INTO space_members (space_id, principal_type, principal_id, role) VALUES ($1, 'team', $2, 'manager')`, restricted, team)
	if got := role(ws, restricted, member); !got.Valid || got.String != "manager" {
		t.Fatalf("team manager = %v, want manager", got)
	}
	// A stale entry does not make a non-member visible.
	exec(`INSERT INTO space_members (space_id, principal_type, principal_id, role) VALUES ($1, 'user', $2, 'manager')`, restricted, outsider)
	if got := role(ws, restricted, outsider); got.Valid {
		t.Fatalf("non-member with a stale entry = %v, want none", got)
	}
}
