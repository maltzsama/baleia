package api

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// TestWorkspaceMembers (#653): a workspace admin sets team members' roles and
// shares a personal workspace by e-mail; editors and viewers may not; the
// rules for team membership and the personal owner hold.
func TestWorkspaceMembers(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	q := repo.New(pool)
	ctx := context.Background()
	h := testAPI(handlerFields{queries: q, tx: storeTx{db: pool}, catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute)})

	user := func(email string) db.User {
		pw := "x"
		u, err := q.CreateUser(ctx, db.CreateUserParams{Name: email, Email: email, PasswordHash: &pw, Role: "member", AuthProvider: "local"})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	owner, member, outsider := user("owner@t"), user("member@t"), user("outsider@t")
	team, err := q.CreateTeam(ctx, db.CreateTeamParams{Name: "data", CreatedBy: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	for u, r := range map[db.UUID]string{owner.ID: "owner", member.ID: "member"} {
		if _, err := q.AddTeamMember(ctx, db.AddTeamMemberParams{TeamID: team.ID, UserID: u, Role: r}); err != nil {
			t.Fatal(err)
		}
	}
	shared, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "Data", Type: "shared", PolarisCatalogName: "ws_m_data", OwnerTeamID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "Mine", Type: "personal", PolarisCatalogName: "ws_m_mine", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	as := func(u db.User) context.Context { return ctxAs(idString(u.ID), u.Role) }
	path := func(id db.UUID) PathId { return PathId(id.Bytes) }
	set := func(caller db.User, ws, target db.UUID, role WorkspaceRole) SetWorkspaceMemberResponseObject {
		t.Helper()
		resp, err := h.SetWorkspaceMember(as(caller), SetWorkspaceMemberRequestObject{Id: path(ws), UserId: openapi_types.UUID(target.Bytes), Body: &SetWorkspaceMemberJSONRequestBody{Role: role}})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// The team owner (admin by default) makes the member a viewer.
	if got, ok := set(owner, shared.ID, member.ID, WorkspaceRoleViewer).(SetWorkspaceMember200JSONResponse); !ok || got.Role != WorkspaceRoleViewer || got.Source != WorkspaceMemberSourceTeam || !got.RoleSet {
		t.Fatalf("set viewer = %+v", got)
	}
	// A viewer may not change members.
	if _, ok := set(member, shared.ID, owner.ID, WorkspaceRoleViewer).(SetWorkspaceMember403JSONResponse); !ok {
		t.Error("a viewer changed a member")
	}
	// A shared workspace's members come from its team.
	if got, ok := set(owner, shared.ID, outsider.ID, WorkspaceRoleEditor).(SetWorkspaceMember422JSONResponse); !ok || got.Code != ErrorCodeWorkspaceMemberNotInTeam {
		t.Errorf("outsider = %+v", got)
	}
	// The personal owner stays admin.
	if got, ok := set(owner, personal.ID, owner.ID, WorkspaceRoleViewer).(SetWorkspaceMember422JSONResponse); !ok || got.Code != ErrorCodeWorkspaceOwnerRoleFixed {
		t.Errorf("owner role = %+v", got)
	}
	// An outsider does not see the workspace at all.
	if _, ok := set(outsider, personal.ID, member.ID, WorkspaceRoleViewer).(SetWorkspaceMember404JSONResponse); !ok {
		t.Error("an outsider reached a personal workspace")
	}

	// Sharing a personal workspace by e-mail.
	add, err := h.AddWorkspaceMember(as(owner), AddWorkspaceMemberRequestObject{Id: path(personal.ID), Body: &AddWorkspaceMemberJSONRequestBody{Email: openapi_types.Email(outsider.Email), Role: WorkspaceRoleEditor}})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := add.(AddWorkspaceMember200JSONResponse); !ok || got.Source != WorkspaceMemberSourceShared || got.Role != WorkspaceRoleEditor {
		t.Fatalf("share = %+v", add)
	}
	unknown, err := h.AddWorkspaceMember(as(owner), AddWorkspaceMemberRequestObject{Id: path(personal.ID), Body: &AddWorkspaceMemberJSONRequestBody{Email: "nobody@t", Role: WorkspaceRoleViewer}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := unknown.(AddWorkspaceMember404JSONResponse); !ok {
		t.Errorf("unknown e-mail = %T", unknown)
	}

	// The shared user now sees it, with its role in allowed_actions.
	ws, err := h.GetWorkspace(as(outsider), GetWorkspaceRequestObject{Id: path(personal.ID)})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := ws.(GetWorkspace200JSONResponse)
	if !ok || got.Role == nil || *got.Role != WorkspaceRoleEditor {
		t.Fatalf("shared workspace = %+v", ws)
	}
	has := func(a WorkspaceAllowedAction) bool {
		if got.AllowedActions == nil {
			return false
		}
		for _, x := range *got.AllowedActions {
			if x == a {
				return true
			}
		}
		return false
	}
	if !has(WorkspaceAllowedActionEditContent) || has(WorkspaceAllowedActionManageMembers) {
		t.Errorf("editor actions = %v", got.AllowedActions)
	}

	// Members list, then stop sharing.
	list, err := h.ListWorkspaceMembers(as(outsider), ListWorkspaceMembersRequestObject{Id: path(personal.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if rows, ok := list.(ListWorkspaceMembers200JSONResponse); !ok || len(rows) != 2 {
		t.Fatalf("members = %+v", list)
	}
	rm, err := h.RemoveWorkspaceMember(as(owner), RemoveWorkspaceMemberRequestObject{Id: path(personal.ID), UserId: openapi_types.UUID(outsider.ID.Bytes)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rm.(RemoveWorkspaceMember204Response); !ok {
		t.Fatalf("remove = %T", rm)
	}
	if after, _ := h.GetWorkspace(as(outsider), GetWorkspaceRequestObject{Id: path(personal.ID)}); after == nil {
		t.Fatal("nil response")
	} else if _, visible := after.(GetWorkspace200JSONResponse); visible {
		t.Error("the workspace stayed visible after sharing ended")
	}
}
