package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// fakeStore keeps refresh tokens and users in memory.
type fakeStore struct {
	tokens    map[string]*db.RefreshToken // by hash
	users     map[[16]byte]db.User
	revokeErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{tokens: map[string]*db.RefreshToken{}, users: map[[16]byte]db.User{}}
}

func (f *fakeStore) CreateRefreshToken(_ context.Context, arg db.CreateRefreshTokenParams) (db.RefreshToken, error) {
	rt := db.RefreshToken{ID: newID(), UserID: arg.UserID, TokenHash: arg.TokenHash, ExpiresAt: arg.ExpiresAt, FamilyID: arg.FamilyID}
	f.tokens[arg.TokenHash] = &rt
	return rt, nil
}

func (f *fakeStore) ConsumeRefreshToken(_ context.Context, hash string) (db.ConsumeRefreshTokenRow, error) {
	rt, ok := f.tokens[hash]
	if !ok || rt.Revoked {
		return db.ConsumeRefreshTokenRow{}, pgx.ErrNoRows
	}
	rt.Revoked = true
	return db.ConsumeRefreshTokenRow{ID: rt.ID, UserID: rt.UserID, FamilyID: rt.FamilyID}, nil
}

func (f *fakeStore) GetRefreshTokenByHashAny(_ context.Context, hash string) (db.RefreshToken, error) {
	rt, ok := f.tokens[hash]
	if !ok {
		return db.RefreshToken{}, pgx.ErrNoRows
	}
	return *rt, nil
}

func (f *fakeStore) RevokeRefreshFamily(_ context.Context, family pgtype.UUID) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	for _, rt := range f.tokens {
		if rt.FamilyID == family {
			rt.Revoked = true
		}
	}
	return nil
}

func (f *fakeStore) RevokeUserRefreshTokens(_ context.Context, user pgtype.UUID) error {
	for _, rt := range f.tokens {
		if rt.UserID == user {
			rt.Revoked = true
		}
	}
	return nil
}

func (f *fakeStore) GetUserByID(_ context.Context, id pgtype.UUID) (db.User, error) {
	u, ok := f.users[id.Bytes]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func (f *fakeStore) active(hash string) bool { return !f.tokens[hash].Revoked }

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

func setup(t *testing.T, status string) (*Service, *fakeStore, db.User) {
	t.Helper()
	f := newFakeStore()
	u := db.User{ID: newID(), Role: "member", Status: status}
	f.users[u.ID.Bytes] = u
	return New(f, f, Config{JWTSecret: "0123456789abcdef0123456789abcdef", AccessTTL: time.Minute, RefreshTTL: time.Hour}), f, u
}

func TestIssue_StartsAFamily(t *testing.T) {
	s, f, u := setup(t, "active")
	a, err := s.Issue(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Issue(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	if a.AccessToken == "" || a.RefreshToken == "" || !a.ExpiresAt.After(time.Now()) {
		t.Fatalf("pair = %+v", a)
	}
	ra, rb := f.tokens[auth.HashRefreshToken(a.RefreshToken)], f.tokens[auth.HashRefreshToken(b.RefreshToken)]
	if ra.FamilyID == rb.FamilyID {
		t.Error("every sign-in starts a new family")
	}
}

func TestRotate(t *testing.T) {
	ctx := context.Background()
	s, f, u := setup(t, "active")
	first, err := s.Issue(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	next, got, err := s.Rotate(ctx, first.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != u.ID || next.RefreshToken == first.RefreshToken {
		t.Fatalf("rotate = %+v for %v", next, got.ID)
	}
	if f.tokens[auth.HashRefreshToken(next.RefreshToken)].FamilyID != f.tokens[auth.HashRefreshToken(first.RefreshToken)].FamilyID {
		t.Error("a rotation stays in its family")
	}

	// Replaying the consumed token revokes the family, including the new token.
	if _, _, err := s.Rotate(ctx, first.RefreshToken); !errors.Is(err, ErrReused) {
		t.Fatalf("replay = %v, want ErrReused", err)
	}
	if f.active(auth.HashRefreshToken(next.RefreshToken)) {
		t.Error("the replayed family must be revoked")
	}

	if _, _, err := s.Rotate(ctx, "unknown"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown token = %v, want ErrInvalid", err)
	}
}

func TestRotate_ReuseNotContained(t *testing.T) {
	ctx := context.Background()
	s, f, u := setup(t, "active")
	first, _ := s.Issue(ctx, u)
	if _, _, err := s.Rotate(ctx, first.RefreshToken); err != nil {
		t.Fatal(err)
	}
	f.revokeErr = errors.New("db down")
	_, _, err := s.Rotate(ctx, first.RefreshToken)
	if !errors.Is(err, ErrReuseNotContained) || errors.Is(err, ErrReused) {
		t.Fatalf("err = %v, want ErrReuseNotContained only", err)
	}
}

func TestRotate_Suspended(t *testing.T) {
	ctx := context.Background()
	s, f, u := setup(t, "suspended")
	first, _ := s.Issue(ctx, u)
	other, _ := s.Issue(ctx, u)
	if _, _, err := s.Rotate(ctx, first.RefreshToken); !errors.Is(err, ErrSuspended) {
		t.Fatalf("err = %v, want ErrSuspended", err)
	}
	if f.active(auth.HashRefreshToken(other.RefreshToken)) {
		t.Error("a suspension revokes every session")
	}
}

func TestRotate_UserGone(t *testing.T) {
	ctx := context.Background()
	s, f, u := setup(t, "active")
	first, _ := s.Issue(ctx, u)
	delete(f.users, u.ID.Bytes)
	if _, _, err := s.Rotate(ctx, first.RefreshToken); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
}

func TestEnd(t *testing.T) {
	ctx := context.Background()
	s, f, u := setup(t, "active")
	first, _ := s.Issue(ctx, u)
	next, _, _ := s.Rotate(ctx, first.RefreshToken)

	id, found, err := s.End(ctx, first.RefreshToken)
	if err != nil || !found || id != u.ID {
		t.Fatalf("End = %v %v %v", id, found, err)
	}
	if f.active(auth.HashRefreshToken(next.RefreshToken)) {
		t.Error("logout ends the whole family")
	}
	if _, found, err := s.End(ctx, "unknown"); found || err != nil {
		t.Errorf("unknown token = %v %v, want not found", found, err)
	}
}

// brokenTokens fails every read the way a lost connection does.
type brokenTokens struct{ *fakeStore }

var errDown = errors.New("connection refused")

func (brokenTokens) ConsumeRefreshToken(context.Context, string) (db.ConsumeRefreshTokenRow, error) {
	return db.ConsumeRefreshTokenRow{}, errDown
}

func (brokenTokens) GetRefreshTokenByHashAny(context.Context, string) (db.RefreshToken, error) {
	return db.RefreshToken{}, errDown
}

// TestStoreFailureIsNotInvalid: a database outage is an error, never an
// invalid token or a logout that did not happen.
func TestStoreFailureIsNotInvalid(t *testing.T) {
	f := newFakeStore()
	s := New(brokenTokens{f}, f, Config{JWTSecret: "0123456789abcdef0123456789abcdef", AccessTTL: time.Minute, RefreshTTL: time.Hour})
	if _, _, err := s.Rotate(context.Background(), "any"); !errors.Is(err, errDown) || errors.Is(err, ErrInvalid) {
		t.Errorf("Rotate = %v, want the store error", err)
	}
	if _, found, err := s.End(context.Background(), "any"); found || !errors.Is(err, errDown) {
		t.Errorf("End = %v, %v; want the store error", found, err)
	}
}

// lostLookup has no live row (a replay or an unknown token) and then fails
// the lookup that decides which.
type lostLookup struct{ *fakeStore }

func (lostLookup) ConsumeRefreshToken(context.Context, string) (db.ConsumeRefreshTokenRow, error) {
	return db.ConsumeRefreshTokenRow{}, pgx.ErrNoRows
}

func (lostLookup) GetRefreshTokenByHashAny(context.Context, string) (db.RefreshToken, error) {
	return db.RefreshToken{}, errDown
}

// TestRotate_ReuseLookupFailureIsAnError: when the reuse check cannot run,
// that is a server error, not an invalid token that skips the family revoke.
func TestRotate_ReuseLookupFailureIsAnError(t *testing.T) {
	f := newFakeStore()
	s := New(lostLookup{f}, f, Config{JWTSecret: "0123456789abcdef0123456789abcdef", AccessTTL: time.Minute, RefreshTTL: time.Hour})
	if _, _, err := s.Rotate(context.Background(), "any"); !errors.Is(err, errDown) || errors.Is(err, ErrInvalid) {
		t.Errorf("Rotate = %v, want the store error", err)
	}
}
