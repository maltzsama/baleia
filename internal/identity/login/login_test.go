package login

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity"
	identityldap "github.com/galileostudio/baleia/internal/identity/ldap"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

type fakeStore struct {
	byEmail    map[string]db.User
	byExternal map[string]db.User
	providers  map[[16]byte]db.AuthProvider
	roles      map[[16]byte]string
	teams      []db.UpsertTeamMemberParams
}

func (f *fakeStore) GetUserByEmail(_ context.Context, email string) (db.User, error) {
	if u, ok := f.byEmail[email]; ok {
		return u, nil
	}
	return db.User{}, pgx.ErrNoRows
}

func (f *fakeStore) GetAuthProviderByID(_ context.Context, id pgtype.UUID) (db.AuthProvider, error) {
	if ap, ok := f.providers[id.Bytes]; ok {
		return ap, nil
	}
	return db.AuthProvider{}, pgx.ErrNoRows
}

func (f *fakeStore) GetUserByExternalID(_ context.Context, arg db.GetUserByExternalIDParams) (db.User, error) {
	if u, ok := f.byExternal[*arg.ExternalID]; ok && u.AuthProviderID == arg.AuthProviderID {
		return u, nil
	}
	return db.User{}, pgx.ErrNoRows
}

func (f *fakeStore) UpdateUserRole(_ context.Context, arg db.UpdateUserRoleParams) error {
	f.roles[arg.ID.Bytes] = arg.Role
	return nil
}

func (f *fakeStore) UpsertTeamMember(_ context.Context, arg db.UpsertTeamMemberParams) error {
	f.teams = append(f.teams, arg)
	return nil
}

type fakeDirectory struct{ users map[string]*identityldap.User } // by "user:password"

func (d fakeDirectory) Authenticate(_ context.Context, _ identityldap.Config, username, password string) (*identityldap.User, error) {
	if u, ok := d.users[username+":"+password]; ok {
		return u, nil
	}
	return nil, errors.New("bind failed")
}

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

func hash(t *testing.T, password string) *string {
	t.Helper()
	h, err := auth.HashPasswordWithCost(password, bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return &h
}

func newStore() *fakeStore {
	return &fakeStore{byEmail: map[string]db.User{}, byExternal: map[string]db.User{}, providers: map[[16]byte]db.AuthProvider{}, roles: map[[16]byte]string{}}
}

func TestPassword(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	st.byEmail["ana@x.io"] = db.User{ID: newID(), Email: "ana@x.io", AuthProvider: "local", PasswordHash: hash(t, "right"), Status: "active"}
	st.byEmail["sso@x.io"] = db.User{ID: newID(), Email: "sso@x.io", AuthProvider: "oidc", Status: "active"}
	st.byEmail["off@x.io"] = db.User{ID: newID(), Email: "off@x.io", AuthProvider: "local", PasswordHash: hash(t, "right"), Status: "suspended"}
	s := New(st, fakeDirectory{}, nil, bcrypt.MinCost)

	if u, err := s.Password(ctx, "ana@x.io", "right"); err != nil || u.Email != "ana@x.io" {
		t.Fatalf("right password = %v, %v", u.Email, err)
	}
	for _, c := range []struct{ name, email, password string }{
		{"wrong password", "ana@x.io", "wrong"},
		{"unknown account", "nobody@x.io", "right"},
		{"SSO-only account", "sso@x.io", "right"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if _, err := s.Password(ctx, c.email, c.password); !errors.Is(err, ErrInvalidCredentials) {
				t.Errorf("err = %v, want ErrInvalidCredentials", err)
			}
		})
	}
	if _, err := s.Password(ctx, "off@x.io", "right"); !errors.Is(err, ErrSuspended) {
		t.Errorf("suspended = %v, want ErrSuspended", err)
	}
	if _, err := s.Password(ctx, "off@x.io", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("suspended with a wrong password = %v, want ErrInvalidCredentials", err)
	}
}

func TestDirectory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	kr, err := crypto.NewKeyring("test-secret-0123456789abcdef-0123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	ap := db.AuthProvider{ID: newID(), Type: "ldap", Enabled: true}
	ap.ConfigEnc, err = identityldap.SealConfig(kr, ap.ID.String(), identityldap.Config{
		URL: "ldaps://ldap.example.test:636", BindDN: "cn=svc", BindPassword: "s", BaseDN: "ou=people", UserFilter: "(uid={{username}})",
	}.Normalize())
	if err != nil {
		t.Fatal(err)
	}
	disabled := db.AuthProvider{ID: newID(), Type: "ldap", Enabled: false, ConfigEnc: ap.ConfigEnc}

	team := uuid.NewString()
	st := newStore()
	st.providers[ap.ID.Bytes], st.providers[disabled.ID.Bytes] = ap, disabled
	ana := db.User{ID: newID(), AuthProvider: "ldap", AuthProviderID: ap.ID, Role: "member", Status: "active"}
	off := db.User{ID: newID(), AuthProvider: "ldap", AuthProviderID: ap.ID, Role: "member", Status: "suspended"}
	st.byExternal["uid=ana"], st.byExternal["uid=off"] = ana, off
	dir := fakeDirectory{users: map[string]*identityldap.User{
		"ana:right": {ExternalID: "uid=ana", Role: "admin", Teams: []identity.TeamAssignment{{TeamID: team, TeamRole: "member"}}},
		"off:right": {ExternalID: "uid=off", Role: "member"},
	}}
	s := New(st, dir, kr, bcrypt.MinCost)

	u, err := s.Directory(ctx, ap.ID, "ana", "right")
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "admin" || st.roles[ana.ID.Bytes] != "admin" {
		t.Errorf("role = %q (stored %q), want the directory's admin", u.Role, st.roles[ana.ID.Bytes])
	}
	if len(st.teams) != 1 || st.teams[0].UserID != ana.ID {
		t.Errorf("teams = %+v, want the mapped team applied", st.teams)
	}

	for _, c := range []struct {
		name               string
		provider           pgtype.UUID
		username, password string
	}{
		{"wrong password", ap.ID, "ana", "wrong"},
		{"empty username", ap.ID, "", "right"},
		{"disabled provider", disabled.ID, "ana", "right"},
		{"unknown provider", newID(), "ana", "right"},
		// A suspended directory account looks like bad credentials (#284).
		{"suspended account", ap.ID, "off", "right"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if _, err := s.Directory(ctx, c.provider, c.username, c.password); !errors.Is(err, ErrInvalidCredentials) {
				t.Errorf("err = %v, want ErrInvalidCredentials", err)
			}
		})
	}
}

// brokenStore fails every read the way a lost connection does.
type brokenStore struct{ *fakeStore }

var errDown = errors.New("connection refused")

func (brokenStore) GetUserByEmail(context.Context, string) (db.User, error) {
	return db.User{}, errDown
}

func (brokenStore) GetAuthProviderByID(context.Context, pgtype.UUID) (db.AuthProvider, error) {
	return db.AuthProvider{}, errDown
}

// TestStoreFailureIsNotInvalidCredentials: a database outage is an error, not
// a failed login that is audited as denied and slows the account down.
func TestStoreFailureIsNotInvalidCredentials(t *testing.T) {
	t.Parallel()
	s := New(brokenStore{newStore()}, fakeDirectory{}, nil, bcrypt.MinCost)
	if _, err := s.Password(context.Background(), "ana@x.io", "right"); !errors.Is(err, errDown) || errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Password = %v, want the store error", err)
	}
	if _, err := s.Directory(context.Background(), newID(), "ana", "right"); !errors.Is(err, errDown) || errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Directory = %v, want the store error", err)
	}
}
