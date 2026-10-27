package teams

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

// fakeStore keeps teams, users and memberships as the database would,
// constraints included.
type fakeStore struct {
	teams, users map[[16]byte]bool
	members      map[[2][16]byte]db.TeamMember
	lookupErr    error
}

func newStore() *fakeStore {
	return &fakeStore{teams: map[[16]byte]bool{}, users: map[[16]byte]bool{}, members: map[[2][16]byte]db.TeamMember{}}
}

func key(team, user pgtype.UUID) [2][16]byte { return [2][16]byte{team.Bytes, user.Bytes} }

func (f *fakeStore) ListTeams(context.Context) ([]db.Team, error) { return nil, nil }

func (f *fakeStore) ListTeamsByUser(context.Context, pgtype.UUID) ([]db.Team, error) {
	return nil, nil
}

func (f *fakeStore) GetTeamByID(_ context.Context, id pgtype.UUID) (db.Team, error) {
	if f.lookupErr != nil {
		return db.Team{}, f.lookupErr
	}
	if !f.teams[id.Bytes] {
		return db.Team{}, pgx.ErrNoRows
	}
	return db.Team{ID: id}, nil
}

func (f *fakeStore) ListTeamMembers(context.Context, pgtype.UUID) ([]db.ListTeamMembersRow, error) {
	return nil, nil
}

func (f *fakeStore) GetUserByID(_ context.Context, id pgtype.UUID) (db.User, error) {
	return db.User{ID: id}, nil
}

func (f *fakeStore) CreateTeam(context.Context, db.CreateTeamParams) (db.Team, error) {
	id := newID()
	f.teams[id.Bytes] = true
	return db.Team{ID: id}, nil
}

func (f *fakeStore) DeleteTeam(_ context.Context, id pgtype.UUID) error {
	delete(f.teams, id.Bytes)
	return nil
}

func (f *fakeStore) AddTeamMember(_ context.Context, arg db.AddTeamMemberParams) (db.TeamMember, error) {
	if !f.users[arg.UserID.Bytes] {
		return db.TeamMember{}, &pgconn.PgError{Code: "23503"}
	}
	if _, ok := f.members[key(arg.TeamID, arg.UserID)]; ok {
		return db.TeamMember{}, &pgconn.PgError{Code: "23505"}
	}
	m := db.TeamMember{TeamID: arg.TeamID, UserID: arg.UserID, Role: arg.Role}
	f.members[key(arg.TeamID, arg.UserID)] = m
	return m, nil
}

func (f *fakeStore) UpdateTeamMemberRole(_ context.Context, arg db.UpdateTeamMemberRoleParams) (db.TeamMember, error) {
	m, ok := f.members[key(arg.TeamID, arg.UserID)]
	if !ok {
		return db.TeamMember{}, pgx.ErrNoRows
	}
	m.Role = arg.Role
	f.members[key(arg.TeamID, arg.UserID)] = m
	return m, nil
}

func (f *fakeStore) RemoveTeamMember(_ context.Context, arg db.RemoveTeamMemberParams) (int64, error) {
	if _, ok := f.members[key(arg.TeamID, arg.UserID)]; !ok {
		return 0, nil
	}
	delete(f.members, key(arg.TeamID, arg.UserID))
	return 1, nil
}

func TestMembers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st)
	team, _ := s.Create(ctx, "data", nil, newID())
	user := newID()
	st.users[user.Bytes] = true

	if _, err := s.AddMember(ctx, newID(), user, RoleMember); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown team = %v, want ErrNotFound", err)
	}
	if _, err := s.AddMember(ctx, team.ID, newID(), RoleMember); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("unknown user = %v, want ErrUserNotFound", err)
	}
	if _, err := s.AddMember(ctx, team.ID, user, RoleMember); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMember(ctx, team.ID, user, RoleMember); !errors.Is(err, ErrAlreadyMember) {
		t.Errorf("second add = %v, want ErrAlreadyMember", err)
	}

	if _, err := s.SetRole(ctx, team.ID, user, "admin"); !errors.Is(err, ErrInvalidRole) {
		t.Errorf("bad role = %v, want ErrInvalidRole", err)
	}
	if m, err := s.SetRole(ctx, team.ID, user, RoleOwner); err != nil || m.Role != RoleOwner {
		t.Errorf("SetRole = %+v, %v", m, err)
	}
	if _, err := s.SetRole(ctx, team.ID, newID(), RoleOwner); !errors.Is(err, ErrMemberNotFound) {
		t.Errorf("non-member role = %v, want ErrMemberNotFound", err)
	}

	if err := s.RemoveMember(ctx, team.ID, user); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMember(ctx, team.ID, user); !errors.Is(err, ErrMemberNotFound) {
		t.Errorf("second remove = %v, want ErrMemberNotFound", err)
	}
}

// TestLookupFailure: a failed team lookup is returned, not taken for a
// missing team.
func TestLookupFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	st.lookupErr = errors.New("connection reset")
	s := New(st)
	if err := s.Delete(ctx, newID()); !errors.Is(err, st.lookupErr) {
		t.Errorf("Delete = %v, want the lookup error", err)
	}
	if _, err := s.SetRole(ctx, newID(), newID(), RoleOwner); !errors.Is(err, st.lookupErr) {
		t.Errorf("SetRole = %v, want the lookup error", err)
	}
}
