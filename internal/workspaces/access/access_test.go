package access

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

type fakeStore struct {
	Store
	member, bound bool
	users         []db.ListWorkspacePrincipalUsersRow
	teams         []db.Team
}

func (f *fakeStore) CheckWorkspaceAccess(context.Context, db.CheckWorkspaceAccessParams) (bool, error) {
	return f.member, nil
}

func (f *fakeStore) AgentWorkspaceBound(context.Context, db.AgentWorkspaceBoundParams) (bool, error) {
	return f.bound, nil
}

func (f *fakeStore) ListWorkspacePrincipalUsers(context.Context, db.ListWorkspacePrincipalUsersParams) ([]db.ListWorkspacePrincipalUsersRow, error) {
	return f.users, nil
}

func (f *fakeStore) ListTeamsByUser(context.Context, pgtype.UUID) ([]db.Team, error) {
	return f.teams, nil
}

func (f *fakeStore) ListTeamMembers(context.Context, pgtype.UUID) ([]db.ListTeamMembersRow, error) {
	return make([]db.ListTeamMembersRow, 3), nil
}

// TestCheck: a member passes; an agent also needs to be bound to the
// workspace (#59).
func TestCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &fakeStore{}
	s := New(st)
	user := Actor{User: newID()}
	agent := Actor{User: user.User, Agent: newID()}
	if err := s.Check(ctx, newID(), user); !errors.Is(err, ErrNotFound) {
		t.Errorf("non-member = %v", err)
	}
	st.member = true
	if err := s.Check(ctx, newID(), user); err != nil {
		t.Errorf("member = %v", err)
	}
	if err := s.Check(ctx, newID(), agent); !errors.Is(err, ErrNotFound) {
		t.Errorf("unbound agent = %v", err)
	}
	st.bound = true
	if err := s.Check(ctx, newID(), agent); err != nil {
		t.Errorf("bound agent = %v", err)
	}
}

func TestPrincipals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &fakeStore{
		users: []db.ListWorkspacePrincipalUsersRow{{Name: "Ana", Email: "ana@x.io"}, {Name: "Bruno", Email: "bruno@x.io"}},
		teams: []db.Team{{Name: "Data"}, {Name: "Ops"}},
	}
	s := New(st)
	got, err := s.Principals(ctx, newID(), newID(), PrincipalFilter{Q: "AN", Limit: 10})
	if err != nil || len(got) != 1 || got[0].Name != "Ana" {
		t.Fatalf("q = %+v, %v; want Ana only", got, err)
	}
	got, _ = s.Principals(ctx, newID(), newID(), PrincipalFilter{Kind: "team", Limit: 10})
	if len(got) != 2 || got[0].Members != 3 {
		t.Errorf("teams = %+v; want both with their member count", got)
	}
	if got, _ = s.Principals(ctx, newID(), newID(), PrincipalFilter{Limit: 1}); len(got) != 1 {
		t.Errorf("limit = %d principals, want 1", len(got))
	}
}
