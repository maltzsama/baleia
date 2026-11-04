package authz

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestWorkspaceRoles_Postgres runs the role rules of #653 on the real schema:
// team defaults, explicit roles, personal sharing, the space role capped by
// the workspace role, and a member who leaves the team.
func TestWorkspaceRoles_Postgres(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := db.New(testdb.New(t))
	c := New(q)

	user := func(email string) db.User {
		h := "x"
		u, err := q.CreateUser(ctx, db.CreateUserParams{Name: email, Email: email, PasswordHash: &h, Role: "member", AuthProvider: "local"})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	owner, member, viewer, guest, outsider := user("owner@t"), user("member@t"), user("viewer@t"), user("guest@t"), user("outsider@t")
	team, err := q.CreateTeam(ctx, db.CreateTeamParams{Name: "data", CreatedBy: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	for u, r := range map[pgtype.UUID]string{owner.ID: "owner", member.ID: "member", viewer.ID: "member"} {
		if _, err := q.AddTeamMember(ctx, db.AddTeamMemberParams{TeamID: team.ID, UserID: u, Role: r}); err != nil {
			t.Fatal(err)
		}
	}
	shared, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "Data", Type: "shared", PolarisCatalogName: "ws_data", OwnerTeamID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "Mine", Type: "personal", PolarisCatalogName: "ws_mine", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	set := func(ws, u pgtype.UUID, role string) {
		t.Helper()
		if _, err := q.UpsertWorkspaceMember(ctx, db.UpsertWorkspaceMemberParams{WorkspaceID: ws, UserID: u, Role: role, Source: "manual"}); err != nil {
			t.Fatal(err)
		}
	}
	set(shared.ID, viewer.ID, "viewer")
	set(personal.ID, guest.ID, "editor")
	set(shared.ID, outsider.ID, "admin") // refused: not in the team (0 rows)

	role := func(ws pgtype.UUID, u db.User) WorkspaceRole {
		t.Helper()
		r, err := c.WorkspaceRole(ctx, Principal{UserID: u.ID, Role: u.Role}, ws)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, tt := range []struct {
		name string
		ws   pgtype.UUID
		u    db.User
		want WorkspaceRole
	}{
		{"team owner defaults to admin", shared.ID, owner, WorkspaceAdmin},
		{"team member defaults to editor", shared.ID, member, WorkspaceEditor},
		{"an explicit role wins", shared.ID, viewer, WorkspaceViewer},
		{"a role row without team membership grants nothing", shared.ID, outsider, WorkspaceNone},
		{"the personal owner is admin", personal.ID, owner, WorkspaceAdmin},
		{"a personal workspace shared with a user", personal.ID, guest, WorkspaceEditor},
		{"a personal workspace not shared", personal.ID, member, WorkspaceNone},
	} {
		if got := role(tt.ws, tt.u); got != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}

	// Space roles only narrow: the default space gives members "editor", a
	// viewer stays a viewer, and a manager entry cannot lift an editor.
	sp, err := q.CreateSpace(ctx, db.CreateSpaceParams{WorkspaceID: shared.ID, Name: "Finance", Slug: "finance", Visibility: "restricted", DefaultRole: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []db.User{member, viewer} {
		if _, err := q.SetSpaceMember(ctx, db.SetSpaceMemberParams{SpaceID: sp.ID, PrincipalType: "user", PrincipalID: u.ID, Role: "manager"}); err != nil {
			t.Fatal(err)
		}
	}
	spaceRole := func(u db.User) SpaceRole {
		t.Helper()
		r, err := c.EffectiveSpaceRole(ctx, Principal{UserID: u.ID}, shared.ID, sp.ID)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if got := spaceRole(member); got != SpaceEditor {
		t.Errorf("editor with a manager entry = %v, want editor", got)
	}
	if got := spaceRole(viewer); got != SpaceViewer {
		t.Errorf("viewer with a manager entry = %v, want viewer", got)
	}
	if got := spaceRole(owner); got != SpaceManager {
		t.Errorf("workspace admin in a restricted space = %v, want manager", got)
	}
	if got := spaceRole(outsider); got != SpaceNone {
		t.Errorf("non-member = %v, want none", got)
	}

	// An admin by role manages the workspace, but other members' executions
	// and the workspace's audit stay with its structural owner (#611).
	set(shared.ID, member.ID, "admin")
	if ok, err := q.IsWorkspaceOwner(ctx, db.IsWorkspaceOwnerParams{ID: shared.ID, UserID: member.ID}); err != nil || !ok {
		t.Errorf("admin by role is a workspace admin: %v, %v", ok, err)
	}
	if ok, err := q.IsWorkspaceStructuralOwner(ctx, db.IsWorkspaceStructuralOwnerParams{ID: shared.ID, UserID: member.ID}); err != nil || ok {
		t.Errorf("admin by role is not the structural owner: %v, %v", ok, err)
	}
	if ok, err := q.IsWorkspaceStructuralOwner(ctx, db.IsWorkspaceStructuralOwnerParams{ID: shared.ID, UserID: owner.ID}); err != nil || !ok {
		t.Errorf("team owner is the structural owner: %v, %v", ok, err)
	}
	if ok, err := q.IsWorkspaceOwner(ctx, db.IsWorkspaceOwnerParams{ID: personal.ID, UserID: member.ID}); err != nil || ok {
		t.Errorf("a non-member is not an owner (and no NULL scan error): %v, %v", ok, err)
	}

	// Leaving the team ends the membership, even with an explicit role.
	if _, err := q.RemoveTeamMember(ctx, db.RemoveTeamMemberParams{TeamID: team.ID, UserID: viewer.ID}); err != nil {
		t.Fatal(err)
	}
	if got := role(shared.ID, viewer); got != WorkspaceNone {
		t.Errorf("after leaving the team = %v", got)
	}

	// Listing follows the same rule.
	ws, err := q.ListWorkspacesByUser(ctx, guest.ID)
	if err != nil || len(ws) != 1 || ws[0].ID != personal.ID {
		t.Errorf("guest's workspaces = %v, %v", ws, err)
	}
	members, err := q.ListWorkspaceMembers(ctx, personal.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("personal members = %v, %v", members, err)
	}
	for _, m := range members {
		switch m.User.ID {
		case owner.ID:
			if m.Source != "owner" || m.Role != "admin" {
				t.Errorf("owner row = %+v", m)
			}
		case guest.ID:
			if m.Source != "shared" || m.Role != "editor" || !m.RoleSet {
				t.Errorf("guest row = %+v", m)
			}
		}
	}
}
