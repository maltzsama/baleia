package api

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/audit/trail"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// TestResolveAudit_UsersAreScopedToSharedWorkspaces: a member resolves
// themselves and the users they share a workspace with; a stranger's name
// and e-mail stay hidden, and an admin resolves everyone (#432).
func TestResolveAudit_UsersAreScopedToSharedWorkspaces(t *testing.T) {
	t.Parallel()
	f := newAuditFixture(t)
	ctx := context.Background()
	var peer, team string
	if err := f.pool.QueryRow(ctx, `INSERT INTO users (name, email, role, password_hash) VALUES ('peer', 'peer@x.io', 'member', 'x') RETURNING id::text`).Scan(&peer); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO teams (name, created_by) VALUES ('t', $1) RETURNING id::text`, f.owner).Scan(&team); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO team_members (team_id, user_id) VALUES ($1, $2), ($1, $3)`, team, f.owner, peer); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO workspaces (name, type, polaris_catalog_name, owner_team_id) VALUES ('shared', 'shared', 's', $1)`, team); err != nil {
		t.Fatal(err)
	}

	ids := []openapi_types.UUID{uuid.MustParse(f.owner), uuid.MustParse(peer), uuid.MustParse(f.stranger)}
	resolve := func(ctx context.Context) map[string]AuditResolvedUser {
		t.Helper()
		// Only users: the other lists are optional.
		resp, err := f.h.ResolveAudit(ctx, ResolveAuditRequestObject{Body: &AuditResolveRequest{Users: &ids}})
		ok, found := resp.(ResolveAudit200JSONResponse)
		if err != nil || !found {
			t.Fatalf("resolve = %T, %v", resp, err)
		}
		return *ok.Users
	}

	got := resolve(ctxAs(f.owner, "member"))
	if _, ok := got[f.stranger]; ok || len(got) != 2 || got[peer].Name != "peer" || got[f.owner].Name == "" {
		t.Errorf("member resolved %+v; want themselves and the peer, not the stranger", got)
	}
	if got := resolve(ctxAs(f.admin, "admin")); len(got) != 3 {
		t.Errorf("admin resolved %d users, want 3", len(got))
	}
}

// userLookup answers every user lookup with err.
type userLookup struct {
	*authzFake
	err error
}

func (q userLookup) GetUserByID(context.Context, pgtype.UUID) (db.User, error) {
	return db.User{}, q.err
}

// TestResolveAudit_LookupFailureIsAnError: an unknown id is left out of the
// answer, but a failed lookup is a server error, not a silent gap.
func TestResolveAudit_LookupFailureIsAnError(t *testing.T) {
	t.Parallel()
	h, q, _ := newAuthzHandlers(t)
	admin := ctxAs(ownerID, "admin")
	ids := []openapi_types.UUID{{0xAA}}
	body := &AuditResolveRequest{Users: &ids}

	h.trail = trail.New(userLookup{authzFake: q, err: pgx.ErrNoRows})
	resp, err := h.ResolveAudit(admin, ResolveAuditRequestObject{Body: body})
	ok, isOK := resp.(ResolveAudit200JSONResponse)
	if err != nil || !isOK || len(*ok.Users) != 0 {
		t.Fatalf("unknown user = %T, %v; want 200 without it", resp, err)
	}

	h.trail = trail.New(userLookup{authzFake: q, err: errors.New("connection reset")})
	if _, err := h.ResolveAudit(admin, ResolveAuditRequestObject{Body: body}); err == nil {
		t.Error("a failed lookup was answered as an unknown user")
	}
}
