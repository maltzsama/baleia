package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
)

// TestSessions_RotateKeepsTheFamilyLive (#1030): a rotation revokes the old
// token and creates the next in one transaction, so the family a session is
// bound to is live before, during and after it; a replay finds no live row.
func TestSessions_RotateKeepsTheFamilyLive(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	ctx := context.Background()
	store := NewSessions(gdb)
	user := createAPIKeyUser(t, gdb, "rotate@example.test")
	family := PGUUID(NewUUID())
	later := db.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}

	if live, err := store.LiveRefreshFamily(ctx, family); err != nil || live {
		t.Fatalf("an unknown family is live=%v err=%v", live, err)
	}
	if _, err := store.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID: PGUUID(string(user.ID)), TokenHash: "first", ExpiresAt: later, FamilyID: family,
	}); err != nil {
		t.Fatal(err)
	}
	if live, err := store.LiveRefreshFamily(ctx, family); err != nil || !live {
		t.Fatalf("a fresh family is live=%v err=%v", live, err)
	}

	row, err := store.RotateRefreshToken(ctx, db.RotateRefreshTokenParams{TokenHash: "first", NewTokenHash: "second", NewExpiresAt: later})
	if err != nil {
		t.Fatal(err)
	}
	if row.FamilyID != family {
		t.Errorf("the successor must stay in the family")
	}
	if live, err := store.LiveRefreshFamily(ctx, family); err != nil || !live {
		t.Fatalf("after a rotation live=%v err=%v", live, err)
	}
	if _, err := store.RotateRefreshToken(ctx, db.RotateRefreshTokenParams{TokenHash: "first", NewTokenHash: "third", NewExpiresAt: later}); !errors.Is(err, db.ErrNoRows) {
		t.Errorf("replay err = %v, want ErrNoRows", err)
	}
	if _, err := store.GetRefreshTokenByHashAny(ctx, "third"); !errors.Is(err, db.ErrNoRows) {
		t.Errorf("a refused rotation must not create a token: %v", err)
	}

	if err := store.RevokeRefreshFamily(ctx, family); err != nil {
		t.Fatal(err)
	}
	if live, err := store.LiveRefreshFamily(ctx, family); err != nil || live {
		t.Errorf("a revoked family is live=%v err=%v", live, err)
	}
}

// TestSessions_ExpiredFamilyIsNotLive (#1030).
func TestSessions_ExpiredFamilyIsNotLive(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	ctx := context.Background()
	store := NewSessions(gdb)
	user := createAPIKeyUser(t, gdb, "expired@example.test")
	family := PGUUID(NewUUID())
	if _, err := store.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID: PGUUID(string(user.ID)), TokenHash: "old", FamilyID: family,
		ExpiresAt: db.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if live, err := store.LiveRefreshFamily(ctx, family); err != nil || live {
		t.Errorf("an expired family is live=%v err=%v", live, err)
	}
}
