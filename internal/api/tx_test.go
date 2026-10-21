package api

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// failWrites makes every write matching event (e.g. "INSERT ON
// password_reset_tokens") fail, so a test can break the second write of a
// handler and check the first one was rolled back.
func failWrites(t *testing.T, pool *pgxpool.Pool, event string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION test_fail_write() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'test: write refused'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER test_fail_write BEFORE `+event+` FOR EACH ROW EXECUTE FUNCTION test_fail_write()`); err != nil {
		t.Fatal(err)
	}
}

// TestInviteUser_TokenFailureLeavesNoUser: when the invite token cannot be
// stored, the user is not created either, so the invite can be retried (#376).
func TestInviteUser_TokenFailureLeavesNoUser(t *testing.T) {
	f := newAuditFixture(t)
	failWrites(t, f.pool, "INSERT ON password_reset_tokens")

	if _, err := f.h.InviteUser(ctxAs(f.admin, "admin"), InviteUserRequestObject{Body: &InviteUserRequest{Email: "new@x.io", Role: "member"}}); err == nil {
		t.Fatal("invite with a failed token write returned no error")
	}
	if _, err := db.New(f.pool).GetUserByEmail(context.Background(), "new@x.io"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("user after a failed invite: err = %v, want no rows", err)
	}
}
