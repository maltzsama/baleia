package provision

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeMembers struct {
	added []db.UpsertTeamMemberParams
	fail  map[string]bool
}

func (f *fakeMembers) UpsertTeamMember(_ context.Context, arg db.UpsertTeamMemberParams) error {
	if f.fail[uuid.UUID(arg.TeamID.Bytes).String()] {
		return errors.New("team gone")
	}
	f.added = append(f.added, arg)
	return nil
}

func TestApplyTeams(t *testing.T) {
	user := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	ok, broken, failing := uuid.NewString(), "not-a-uuid", uuid.NewString()
	f := &fakeMembers{fail: map[string]bool{failing: true}}

	ApplyTeams(context.Background(), f, user, []identity.TeamAssignment{
		{TeamID: ok, TeamRole: "owner"},
		{TeamID: broken, TeamRole: "member"},
		{TeamID: failing, TeamRole: "member"},
	})

	if len(f.added) != 1 || uuid.UUID(f.added[0].TeamID.Bytes).String() != ok || f.added[0].Role != "owner" || f.added[0].UserID != user {
		t.Fatalf("added = %+v; want only the valid team, the rest skipped", f.added)
	}
}

// fakeAccounts keeps accounts by OIDC subject. raceSubject simulates a
// concurrent login creating the subject between the lookup and the insert.
type fakeAccounts struct {
	fakeMembers
	bySubject   map[string]db.User
	roles       map[[16]byte]string
	raceSubject string
}

func (f *fakeAccounts) GetUserByExternalID(_ context.Context, arg db.GetUserByExternalIDParams) (db.User, error) {
	if u, ok := f.bySubject[*arg.ExternalID]; ok {
		return u, nil
	}
	return db.User{}, pgx.ErrNoRows
}

func (f *fakeAccounts) CreateUser(_ context.Context, arg db.CreateUserParams) (db.User, error) {
	u := db.User{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Email: arg.Email, Role: arg.Role, AuthProvider: arg.AuthProvider, ExternalID: arg.ExternalID}
	if *arg.ExternalID == f.raceSubject {
		f.bySubject[f.raceSubject] = u
		return db.User{}, &pgconn.PgError{Code: "23505"}
	}
	f.bySubject[*arg.ExternalID] = u
	return u, nil
}

func (f *fakeAccounts) UpdateUserRole(_ context.Context, arg db.UpdateUserRoleParams) error {
	f.roles[arg.ID.Bytes] = arg.Role
	return nil
}

func newAccounts() *fakeAccounts {
	return &fakeAccounts{bySubject: map[string]db.User{}, roles: map[[16]byte]string{}}
}

func TestOIDCUser(t *testing.T) {
	ctx := context.Background()
	provider := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	team := uuid.NewString()
	cfg := auth.OIDCProviderConfig{
		AutoCreate:          true,
		AllowedEmailDomains: []string{"@X.io"},
		RoleMapping:         []identity.RoleMapping{{Group: "admins", Role: identity.RoleAdmin}},
		TeamMapping:         []identity.TeamMapping{{Group: "data", TeamID: team, TeamRole: "member"}},
	}
	info := func(subject, email string, verified bool, groups ...string) *auth.OIDCUserInfo {
		return &auth.OIDCUserInfo{Subject: subject, Email: email, EmailVerified: verified, Groups: groups}
	}

	t.Run("creates an allowed verified account with its teams", func(t *testing.T) {
		st := newAccounts()
		u, err := OIDCUser(ctx, st, provider, cfg, info("s1", "ana@x.io", true, "data"))
		if err != nil || u.AuthProvider != "oidc" || u.Role != identity.RoleMember {
			t.Fatalf("user = %+v, %v", u, err)
		}
		if len(st.added) != 1 || st.added[0].UserID != u.ID {
			t.Errorf("teams = %+v", st.added)
		}
	})

	for _, c := range []struct {
		name string
		cfg  auth.OIDCProviderConfig
		info *auth.OIDCUserInfo
	}{
		{"auto-create off", auth.OIDCProviderConfig{AllowedEmailDomains: cfg.AllowedEmailDomains}, info("s2", "ana@x.io", true)},
		{"unverified e-mail", cfg, info("s3", "ana@x.io", false)},
		{"domain not allowed", cfg, info("s4", "ana@evil.io", true)},
		{"no allowed domains", auth.OIDCProviderConfig{AutoCreate: true}, info("s5", "ana@x.io", true)},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newAccounts()
			if _, err := OIDCUser(ctx, st, provider, c.cfg, c.info); !errors.Is(err, ErrNotProvisioned) {
				t.Errorf("err = %v, want ErrNotProvisioned", err)
			}
			if len(st.bySubject) != 0 {
				t.Error("no account may be created")
			}
		})
	}

	t.Run("an existing subject gets the mapped role", func(t *testing.T) {
		st := newAccounts()
		existing := db.User{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Role: identity.RoleMember}
		st.bySubject["s6"] = existing
		u, err := OIDCUser(ctx, st, provider, auth.OIDCProviderConfig{RoleMapping: cfg.RoleMapping}, info("s6", "ana@x.io", true, "admins"))
		if err != nil || u.Role != identity.RoleAdmin || st.roles[existing.ID.Bytes] != identity.RoleAdmin {
			t.Fatalf("user = %+v (stored %q), %v", u, st.roles[existing.ID.Bytes], err)
		}
	})

	t.Run("a concurrent login's account is used", func(t *testing.T) {
		st := newAccounts()
		st.raceSubject = "s7"
		u, err := OIDCUser(ctx, st, provider, cfg, info("s7", "ana@x.io", true))
		if err != nil || u.ExternalID == nil || *u.ExternalID != "s7" {
			t.Fatalf("user = %+v, %v", u, err)
		}
	})
}

func TestDomainAllowed(t *testing.T) {
	for _, c := range []struct {
		email string
		want  bool
	}{
		{"ana@x.io", true},
		{"ANA@X.IO", true},
		{"ana@sub.x.io", false},
		{"ana@", false},
		{"@x.io", false},
		{"ana", false},
	} {
		if got := DomainAllowed(c.email, []string{"@x.io", "y.org"}); got != c.want {
			t.Errorf("DomainAllowed(%q) = %v, want %v", c.email, got, c.want)
		}
	}
}
