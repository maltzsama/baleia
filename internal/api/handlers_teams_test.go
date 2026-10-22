package api

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// TestUpdateTeamMemberRole: an admin can change a member role in place, and a
// non-admin is refused.
func TestUpdateTeamMemberRole(t *testing.T) {
	pool := testdb.New(t)
	q := db.New(pool)
	h := testAPI(handlerFields{queries: q, pool: pool, tx: poolTx{pool: pool}, catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute)})
	ctx := context.Background()

	hash, err := auth.HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	createUser := func(email, role string) db.User {
		t.Helper()
		u, err := q.CreateUser(ctx, db.CreateUserParams{
			Name: email, Email: email, PasswordHash: &hash, Role: role, AuthProvider: "local",
		})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	admin := createUser("admin@teams.test", "admin")
	member := createUser("member@teams.test", "member")

	team, err := q.CreateTeam(ctx, db.CreateTeamParams{Name: "engineering", CreatedBy: admin.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.AddTeamMember(ctx, db.AddTeamMemberParams{TeamID: team.ID, UserID: member.ID, Role: "member"}); err != nil {
		t.Fatal(err)
	}

	resp, err := h.UpdateTeamMemberRole(ctxAs(admin.ID.String(), "admin"), UpdateTeamMemberRoleRequestObject{
		Id:     openapi_types.UUID(team.ID.Bytes),
		UserId: openapi_types.UUID(member.ID.Bytes),
		Body:   &UpdateTeamMemberRoleRequest{Role: UpdateTeamMemberRoleRequestRole("owner")},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := resp.(UpdateTeamMemberRole200JSONResponse)
	if !ok {
		t.Fatalf("update = %T, want 200", resp)
	}
	if updated.Role != TeamMemberRoleOwner {
		t.Errorf("role = %q, want owner", updated.Role)
	}
	if updated.User.Status != UserPublicStatus(auth.StatusActive) {
		t.Errorf("user status = %q, want active", updated.User.Status)
	}

	invalid, err := h.UpdateTeamMemberRole(ctxAs(admin.ID.String(), "admin"), UpdateTeamMemberRoleRequestObject{
		Id:     openapi_types.UUID(team.ID.Bytes),
		UserId: openapi_types.UUID(member.ID.Bytes),
		Body:   &UpdateTeamMemberRoleRequest{Role: UpdateTeamMemberRoleRequestRole("admin")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := invalid.(UpdateTeamMemberRole422JSONResponse); !ok {
		t.Fatalf("invalid role = %T, want 422", invalid)
	}

	denied, err := h.UpdateTeamMemberRole(ctxAs(member.ID.String(), "member"), UpdateTeamMemberRoleRequestObject{
		Id:     openapi_types.UUID(team.ID.Bytes),
		UserId: openapi_types.UUID(member.ID.Bytes),
		Body:   &UpdateTeamMemberRoleRequest{Role: UpdateTeamMemberRoleRequestRole("member")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := denied.(UpdateTeamMemberRole403JSONResponse); !ok {
		t.Fatalf("member update = %T, want 403", denied)
	}
}
