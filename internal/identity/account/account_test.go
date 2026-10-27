package account

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

type fakeStore struct {
	users    map[[16]byte]db.User
	personal map[[16]byte]pgtype.UUID
}

func (f *fakeStore) GetUserByID(_ context.Context, id pgtype.UUID) (db.User, error) {
	if u, ok := f.users[id.Bytes]; ok {
		return u, nil
	}
	return db.User{}, pgx.ErrNoRows
}

func (f *fakeStore) UpdateUser(_ context.Context, arg db.UpdateUserParams) (db.User, error) {
	u := f.users[arg.ID.Bytes]
	u.Name, u.Email, u.Locale = arg.Name, arg.Email, arg.Locale
	f.users[arg.ID.Bytes] = u
	return u, nil
}

func (f *fakeStore) UpdateUserPassword(_ context.Context, arg db.UpdateUserPasswordParams) error {
	u := f.users[arg.ID.Bytes]
	u.PasswordHash = arg.PasswordHash
	f.users[arg.ID.Bytes] = u
	return nil
}

func (f *fakeStore) PersonalWorkspaceID(_ context.Context, id pgtype.UUID) (pgtype.UUID, error) {
	if ws, ok := f.personal[id.Bytes]; ok {
		return ws, nil
	}
	return pgtype.UUID{}, pgx.ErrNoRows
}

type fakeSessions struct{ revoked []pgtype.UUID }

func (s *fakeSessions) RevokeAll(_ context.Context, id pgtype.UUID) {
	s.revoked = append(s.revoked, id)
}

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

func setup(t *testing.T) (*Service, *fakeStore, *fakeSessions, db.User) {
	t.Helper()
	h, err := auth.HashPasswordWithCost("old-password", bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	u := db.User{ID: newID(), Name: "Ana", Email: "ana@x.io", PasswordHash: &h}
	st := &fakeStore{users: map[[16]byte]db.User{u.ID.Bytes: u}, personal: map[[16]byte]pgtype.UUID{}}
	ss := &fakeSessions{}
	return New(st, ss, Config{PasswordCost: bcrypt.MinCost, MinPasswordLength: 8}), st, ss, u
}

func TestGet(t *testing.T) {
	t.Parallel()
	s, st, _, u := setup(t)
	if got, err := s.Get(context.Background(), u.ID); err != nil || got.Email != u.Email {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if _, err := s.Get(context.Background(), newID()); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown = %v, want ErrNotFound", err)
	}
	if _, ok, err := s.PersonalWorkspace(context.Background(), u.ID); ok || err != nil {
		t.Error("no personal workspace yet")
	}
	ws := newID()
	st.personal[u.ID.Bytes] = ws
	if got, ok, err := s.PersonalWorkspace(context.Background(), u.ID); !ok || err != nil || got != ws {
		t.Errorf("personal workspace = %v, %v", got, ok)
	}
}

func TestUpdateProfile_KeepsUnsetFields(t *testing.T) {
	t.Parallel()
	s, _, _, u := setup(t)
	pt := "pt-BR"
	got, err := s.UpdateProfile(context.Background(), u.ID, Profile{Locale: &pt})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != u.Name || got.Email != u.Email || got.Locale == nil || *got.Locale != pt {
		t.Fatalf("updated = %+v", got)
	}
	name := "Ana Maria"
	if got, _ = s.UpdateProfile(context.Background(), u.ID, Profile{Name: &name}); got.Name != name || got.Locale == nil {
		t.Fatalf("updated = %+v; the locale must be kept", got)
	}
}

func TestChangePassword(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, st, ss, u := setup(t)

	if err := s.ChangePassword(ctx, u.ID, "wrong", "new-password"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong current = %v, want ErrWrongPassword", err)
	}
	// The current password is checked before the new one's length.
	if err := s.ChangePassword(ctx, u.ID, "wrong", "short"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong current and short new = %v, want ErrWrongPassword", err)
	}
	if err := s.ChangePassword(ctx, u.ID, "old-password", "short"); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("short = %v, want ErrPasswordTooShort", err)
	}
	if len(ss.revoked) != 0 {
		t.Fatal("a rejected change must not end sessions")
	}

	if err := s.ChangePassword(ctx, u.ID, "old-password", "new-password"); err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPassword(*st.users[u.ID.Bytes].PasswordHash, "new-password") {
		t.Error("the new password must be stored")
	}
	if len(ss.revoked) != 1 || ss.revoked[0] != u.ID {
		t.Errorf("revoked = %v, want every session ended", ss.revoked)
	}

	sso := db.User{ID: newID()}
	st.users[sso.ID.Bytes] = sso
	if err := s.ChangePassword(ctx, sso.ID, "", "new-password"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("account without a password = %v, want ErrWrongPassword", err)
	}
}

// brokenStore fails every read the way a lost connection does.
type brokenStore struct{ *fakeStore }

var errDown = errors.New("connection refused")

func (brokenStore) GetUserByID(context.Context, pgtype.UUID) (db.User, error) {
	return db.User{}, errDown
}

func (brokenStore) PersonalWorkspaceID(context.Context, pgtype.UUID) (pgtype.UUID, error) {
	return pgtype.UUID{}, errDown
}

// TestStoreFailureIsNotNotFound: a database outage is an error, never a
// missing account or workspace.
func TestStoreFailureIsNotNotFound(t *testing.T) {
	t.Parallel()
	s := New(brokenStore{&fakeStore{}}, &fakeSessions{}, Config{})
	if _, err := s.Get(context.Background(), newID()); !errors.Is(err, errDown) || errors.Is(err, ErrNotFound) {
		t.Errorf("Get = %v, want the store error", err)
	}
	if _, ok, err := s.PersonalWorkspace(context.Background(), newID()); ok || !errors.Is(err, errDown) {
		t.Errorf("PersonalWorkspace = %v, %v; want the store error", ok, err)
	}
}
