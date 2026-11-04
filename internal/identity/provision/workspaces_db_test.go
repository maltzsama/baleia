package provision

import (
	"context"
	"sync"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestApplyWorkspaces_Postgres (#653): groups set workspace roles at sign-in,
// a role an admin set wins over a group, leaving a group drops its role, and
// on a shared workspace a group role needs team membership.
func TestApplyWorkspaces_Postgres(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := db.New(testdb.New(t))
	pw := "x"
	newUser := func(email string) db.User {
		u, err := q.CreateUser(ctx, db.CreateUserParams{Name: email, Email: email, PasswordHash: &pw, Role: "member", AuthProvider: "local"})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	ana, owner := newUser("ana@t"), newUser("owner@t")
	team, err := q.CreateTeam(ctx, db.CreateTeamParams{Name: "fin", CreatedBy: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.AddTeamMember(ctx, db.AddTeamMemberParams{TeamID: team.ID, UserID: ana.ID, Role: "member"}); err != nil {
		t.Fatal(err)
	}
	fin, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "Fin", Type: "shared", PolarisCatalogName: "ws_g_fin", OwnerTeamID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	team2, err := q.CreateTeam(ctx, db.CreateTeamParams{Name: "ops", CreatedBy: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	ops, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "Ops", Type: "shared", PolarisCatalogName: "ws_g_ops", OwnerTeamID: team2.ID})
	if err != nil {
		t.Fatal(err)
	}
	role := func(ws pgtype.UUID) string {
		t.Helper()
		r, err := q.GetWorkspaceMemberRole(ctx, db.GetWorkspaceMemberRoleParams{ID: ws, UserID: ana.ID})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	assign := func(ws ...identity.WorkspaceAssignment) {
		t.Helper()
		if err := ApplyWorkspaces(ctx, q, ana.ID, ws); err != nil {
			t.Fatal(err)
		}
	}

	// A group makes her a viewer of Fin; Ops is not hers (not in its team).
	assign(identity.WorkspaceAssignment{WorkspaceID: fin.ID.String(), Role: "viewer"},
		identity.WorkspaceAssignment{WorkspaceID: ops.ID.String(), Role: "admin"})
	if got := role(fin.ID); got != "viewer" {
		t.Errorf("group role = %q", got)
	}
	if got := role(ops.ID); got != "" {
		t.Errorf("a group role reached a workspace outside her team: %q", got)
	}

	// Leaving the group: back to the team default.
	assign()
	if got := role(fin.ID); got != "editor" {
		t.Errorf("after leaving the group = %q, want the team default", got)
	}

	// An admin's role wins over a group, and survives the group going away.
	if _, err := q.UpsertWorkspaceMember(ctx, db.UpsertWorkspaceMemberParams{WorkspaceID: fin.ID, UserID: ana.ID, Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	assign(identity.WorkspaceAssignment{WorkspaceID: fin.ID.String(), Role: "viewer"})
	if got := role(fin.ID); got != "admin" {
		t.Errorf("a group overwrote an admin's role: %q", got)
	}
	assign()
	if got := role(fin.ID); got != "admin" {
		t.Errorf("an admin's role was dropped with the group: %q", got)
	}

	// A group never reaches someone else's personal workspace.
	mine, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "Mine", Type: "personal", PolarisCatalogName: "ws_g_mine", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	assign(identity.WorkspaceAssignment{WorkspaceID: mine.ID.String(), Role: "admin"})
	if got := role(mine.ID); got != "" {
		t.Errorf("a group reached a personal workspace: %q", got)
	}

	// Concurrent sign-ins of the same member are serialized: the last one's
	// mapping holds, never a mix that drops both.
	if _, err := q.DeleteWorkspaceMember(ctx, db.DeleteWorkspaceMemberParams{WorkspaceID: fin.ID, UserID: ana.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.AddTeamMember(ctx, db.AddTeamMemberParams{TeamID: team2.ID, UserID: ana.ID, Role: "member"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			a := identity.WorkspaceAssignment{WorkspaceID: fin.ID.String(), Role: "viewer"}
			if i%2 == 1 {
				a = identity.WorkspaceAssignment{WorkspaceID: ops.ID.String(), Role: "viewer"}
			}
			if err := ApplyWorkspaces(ctx, q, ana.ID, []identity.WorkspaceAssignment{a}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	finViewer, opsViewer := role(fin.ID) == "viewer", role(ops.ID) == "viewer"
	if finViewer == opsViewer {
		t.Errorf("after concurrent sign-ins exactly one group role must hold: fin=%t ops=%t", finViewer, opsViewer)
	}
}
