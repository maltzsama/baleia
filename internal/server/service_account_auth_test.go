package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
)

// fakeSessions is the account and session state a bearer check reads.
type fakeSessions struct {
	users map[[16]byte]db.User
	live  map[[16]byte]bool
	err   error
}

func (f *fakeSessions) GetUserByID(_ context.Context, id db.UUID) (db.User, error) {
	if f.err != nil {
		return db.User{}, f.err
	}
	u, ok := f.users[id.Bytes]
	if !ok {
		return db.User{}, db.ErrNoRows
	}
	return u, nil
}

func (f *fakeSessions) LiveRefreshFamily(_ context.Context, id db.UUID) (bool, error) {
	return f.live[id.Bytes], nil
}

const bearerSecret = "test-secret-0123456789abcdef-0123456789"

// newSession registers an active member with a live session and returns a
// token bound to it.
func newSession(t *testing.T, f *fakeSessions) (db.User, db.UUID, string) {
	t.Helper()
	user := db.User{ID: db.UUID{Bytes: uuid.New(), Valid: true}, Role: "member", Status: "active"}
	sid := db.UUID{Bytes: uuid.New(), Valid: true}
	f.users[user.ID.Bytes] = user
	f.live[sid.Bytes] = true
	token, err := auth.NewAccessTokenWithTTL(user.ID.String(), user.Role, sid.String(), bearerSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return user, sid, token
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{users: map[[16]byte]db.User{}, live: map[[16]byte]bool{}}
}

// TestBearerContext (#653): a Baleia access token authenticates as before; a
// token that is not one is offered to the service account authenticator,
// which signs the service account in or refuses.
func TestBearerContext(t *testing.T) {
	t.Parallel()
	f := newFakeSessions()
	user, _, baleia := newSession(t, f)
	sa := db.User{ID: db.UUID{Bytes: uuid.New(), Valid: true}, Role: "member", Kind: "service_account"}
	calls := 0
	authSA := func(_ context.Context, raw string) (db.User, error) {
		calls++
		if raw == "client-token" {
			return sa, nil
		}
		return db.User{}, errors.New("no")
	}

	ctx, ok, _ := bearerContext(context.Background(), baleia, bearerSecret, f, authSA)
	if !ok || auth.UserIDFromContext(ctx) != user.ID.String() || calls != 0 {
		t.Fatalf("Baleia token: ok=%t user=%q calls=%d", ok, auth.UserIDFromContext(ctx), calls)
	}
	ctx, ok, _ = bearerContext(context.Background(), "client-token", bearerSecret, f, authSA)
	if !ok || auth.UserIDFromContext(ctx) != sa.ID.String() {
		t.Fatalf("client token: ok=%t user=%q", ok, auth.UserIDFromContext(ctx))
	}
	if _, ok, _ := bearerContext(context.Background(), "garbage", bearerSecret, f, authSA); ok {
		t.Error("an unknown token was accepted")
	}
	if _, ok, _ := bearerContext(context.Background(), "client-token", bearerSecret, f, nil); ok {
		t.Error("without an authenticator only Baleia tokens are accepted")
	}
}

// TestBearerContext_SessionEnds (#1030): an access token stops working the
// moment its session ends, not when it expires.
func TestBearerContext_SessionEnds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := map[string]func(f *fakeSessions, u db.User, sid db.UUID){
		"logout or revoked family": func(f *fakeSessions, _ db.User, sid db.UUID) { delete(f.live, sid.Bytes) },
		"account removed":          func(f *fakeSessions, u db.User, _ db.UUID) { delete(f.users, u.ID.Bytes) },
		"account suspended": func(f *fakeSessions, u db.User, _ db.UUID) {
			u.Status = "suspended"
			f.users[u.ID.Bytes] = u
		},
	}
	for name, end := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFakeSessions()
			u, sid, token := newSession(t, f)
			if _, ok, _ := bearerContext(ctx, token, bearerSecret, f, nil); !ok {
				t.Fatal("a live session was refused")
			}
			end(f, u, sid)
			if _, ok, err := bearerContext(ctx, token, bearerSecret, f, nil); ok || err != nil {
				t.Errorf("ended session: ok=%t err=%v, want a plain refusal", ok, err)
			}
		})
	}
}

// TestBearerContext_RoleIsTheAccounts (#1030): a demoted admin is a member at
// once, whatever the token claims.
func TestBearerContext_RoleIsTheAccounts(t *testing.T) {
	t.Parallel()
	f := newFakeSessions()
	u, sid, _ := newSession(t, f)
	token, err := auth.NewAccessTokenWithTTL(u.ID.String(), "admin", sid.String(), bearerSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx, ok, _ := bearerContext(context.Background(), token, bearerSecret, f, nil)
	if !ok || auth.RoleFromContext(ctx) != "member" {
		t.Errorf("ok=%t role=%q, want the account's member role", ok, auth.RoleFromContext(ctx))
	}
}

// TestBearerContext_NoSessionIDIsRefused (#1030): a token minted before the
// session id existed cannot be tied to a session.
func TestBearerContext_NoSessionIDIsRefused(t *testing.T) {
	t.Parallel()
	f := newFakeSessions()
	u, _, _ := newSession(t, f)
	token, err := auth.NewAccessTokenWithTTL(u.ID.String(), "member", "", bearerSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := bearerContext(context.Background(), token, bearerSecret, f, nil); ok {
		t.Error("a token without a session id was accepted")
	}
}

// TestBearerContext_StoreFailureIsNotARefusal (#1030): an outage is reported as
// such, so the client does not treat it as an expired session.
func TestBearerContext_StoreFailureIsNotARefusal(t *testing.T) {
	t.Parallel()
	f := newFakeSessions()
	_, _, token := newSession(t, f)
	f.err = errors.New("connection refused")
	if _, ok, err := bearerContext(context.Background(), token, bearerSecret, f, nil); ok || !errors.Is(err, errSessionCheck) {
		t.Errorf("ok=%t err=%v, want errSessionCheck", ok, err)
	}
}
