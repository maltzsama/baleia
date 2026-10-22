package provision

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity"
	"github.com/google/uuid"
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
