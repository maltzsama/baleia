package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// TestUpdateTeamMemberRole: an admin can change a member role in place, and a
// non-admin is refused.
func TestUpdateTeamMemberRole(t *testing.T) {
	t.Parallel()
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

// TestTeamMembers_ErrorMapping: adding an unknown user is a 404 and adding a
// member twice a 409; removing someone who is not a member is a 404, not a
// silent success.
func TestTeamMembers_ErrorMapping(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	q := db.New(pool)
	h := testAPI(handlerFields{queries: q, pool: pool, tx: poolTx{pool: pool}, catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute)})
	ctx := context.Background()
	hash, err := auth.HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := q.CreateUser(ctx, db.CreateUserParams{Name: "a", Email: "admin@members.test", PasswordHash: &hash, Role: "admin", AuthProvider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	team, err := q.CreateTeam(ctx, db.CreateTeamParams{Name: "data", CreatedBy: admin.ID})
	if err != nil {
		t.Fatal(err)
	}
	asAdmin := ctxAs(admin.ID.String(), "admin")
	add := func(user [16]byte) AddTeamMemberResponseObject {
		t.Helper()
		resp, err := h.AddTeamMember(asAdmin, AddTeamMemberRequestObject{Id: team.ID.Bytes, Body: &AddTeamMemberRequest{UserId: user, Role: "member"}})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	if resp := add([16]byte{0xEE}); !isType[AddTeamMember404JSONResponse](resp) {
		t.Errorf("unknown user = %T, want 404", resp)
	}
	if _, ok := add(admin.ID.Bytes).(AddTeamMember201JSONResponse); !ok {
		t.Fatal("first add was not a 201")
	}
	if resp := add(admin.ID.Bytes); !isType[AddTeamMember409JSONResponse](resp) {
		t.Errorf("second add = %T, want 409", resp)
	}

	remove := func() RemoveTeamMemberResponseObject {
		t.Helper()
		resp, err := h.RemoveTeamMember(asAdmin, RemoveTeamMemberRequestObject{Id: team.ID.Bytes, UserId: admin.ID.Bytes})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if _, ok := remove().(RemoveTeamMember204Response); !ok {
		t.Fatal("removing a member was not a 204")
	}
	if resp := remove(); !isType[RemoveTeamMember404JSONResponse](resp) {
		t.Errorf("removing a non-member = %T, want 404", resp)
	}
}

// brokenTeams fails every team lookup.
type brokenTeams struct{ *db.Queries }

func (brokenTeams) GetTeamByID(context.Context, pgtype.UUID) (db.Team, error) {
	return db.Team{}, errors.New("connection reset")
}

// TestTeamLookup_StoreFailureIsAnError: a failed lookup is a server error,
// not "team not found".
func TestTeamLookup_StoreFailureIsAnError(t *testing.T) {
	t.Parallel()
	h := testAPI(handlerFields{queries: brokenTeams{}, catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute)})
	asAdmin := ctxAs(testUserID, "admin")
	id := openapi_types.UUID{0x7E}
	if _, err := h.DeleteTeam(asAdmin, DeleteTeamRequestObject{Id: id}); err == nil {
		t.Error("DeleteTeam answered a store failure with a response")
	}
	if _, err := h.AddTeamMember(asAdmin, AddTeamMemberRequestObject{Id: id, Body: &AddTeamMemberRequest{Role: "member"}}); err == nil {
		t.Error("AddTeamMember answered a store failure with a response")
	}
	if _, err := h.UpdateTeamMemberRole(asAdmin, UpdateTeamMemberRoleRequestObject{Id: id, Body: &UpdateTeamMemberRoleRequest{Role: "owner"}}); err == nil {
		t.Error("UpdateTeamMemberRole answered a store failure with a response")
	}
}

func isType[T any](v any) bool {
	_, ok := v.(T)
	return ok
}
