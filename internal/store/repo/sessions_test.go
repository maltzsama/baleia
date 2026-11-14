package repo

import (
	"context"
	"errors"
	"fmt"
	"sync"
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

// TestSessions_ConcurrentRotationHasOneWinner (#1030): two refreshes of the
// same live token race on separate connections; exactly one wins, the other is
// refused, and exactly one successor exists.
func TestSessions_ConcurrentRotationHasOneWinner(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	ctx := context.Background()
	store := NewSessions(gdb)
	user := createAPIKeyUser(t, gdb, "race@example.test")
	family := PGUUID(NewUUID())
	later := db.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
	if _, err := store.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID: PGUUID(string(user.ID)), TokenHash: "live", ExpiresAt: later, FamilyID: family,
	}); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, next := range []string{"next-a", "next-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = store.RotateRefreshToken(ctx, db.RotateRefreshTokenParams{TokenHash: "live", NewTokenHash: next, NewExpiresAt: later})
		}()
	}
	close(start)
	wg.Wait()

	wins, refused := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, db.ErrNoRows):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 || refused != 1 {
		t.Fatalf("wins=%d refused=%d, want 1 and 1", wins, refused)
	}
	var rows int64
	if err := gdb.Table("refresh_tokens").Where("family_id = ?", UUIDString(family)).Count(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Errorf("family has %d tokens, want the original and one successor", rows)
	}
}

// TestSessions_RevokeNeverLeavesASuccessor (#1030): a revocation racing a
// rotation must leave the family dead whichever runs first. Without the
// owner's row lock a revoke that waited on the rotating token missed the
// successor, which stayed live.
func TestSessions_RevokeNeverLeavesASuccessor(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	ctx := context.Background()
	store := NewSessions(gdb)
	user := createAPIKeyUser(t, gdb, "revoke-race@example.test")
	userID := PGUUID(string(user.ID))
	later := db.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}

	for i := range 40 {
		family := PGUUID(NewUUID())
		hash := fmt.Sprintf("tok-%d", i)
		if _, err := store.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{UserID: userID, TokenHash: hash, ExpiresAt: later, FamilyID: family}); err != nil {
			t.Fatal(err)
		}
		revoke := store.RevokeRefreshFamily
		if i%2 == 1 {
			revoke = func(ctx context.Context, family db.UUID) error { return store.RevokeUserRefreshTokens(ctx, userID) }
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			if _, err := store.RotateRefreshToken(ctx, db.RotateRefreshTokenParams{TokenHash: hash, NewTokenHash: hash + "-next", NewExpiresAt: later}); err != nil && !errors.Is(err, db.ErrNoRows) {
				t.Errorf("rotate: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			if err := revoke(ctx, family); err != nil {
				t.Errorf("revoke: %v", err)
			}
		}()
		close(start)
		wg.Wait()
		if live, err := store.LiveRefreshFamily(ctx, family); err != nil || live {
			t.Fatalf("round %d: the family is live=%v err=%v after the revoke", i, live, err)
		}
	}
}
