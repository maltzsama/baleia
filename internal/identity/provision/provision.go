// Package provision applies a mapped directory identity to Baleia accounts:
// the team memberships an LDAP or OIDC login grants.
package provision

import (
	"context"
	"log/slog"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity"
	"github.com/jackc/pgx/v5/pgtype"
)

// TeamMembers writes team memberships; *db.Queries implements it.
type TeamMembers interface {
	UpsertTeamMember(ctx context.Context, arg db.UpsertTeamMemberParams) error
}

// ApplyTeams adds userID to every mapped team with its mapped role. A team
// that cannot be applied is logged and skipped: the login still succeeds.
func ApplyTeams(ctx context.Context, store TeamMembers, userID pgtype.UUID, teams []identity.TeamAssignment) {
	for _, t := range teams {
		var teamID pgtype.UUID
		if err := teamID.Scan(t.TeamID); err != nil {
			continue
		}
		if err := store.UpsertTeamMember(ctx, db.UpsertTeamMemberParams{
			TeamID: teamID,
			UserID: userID,
			Role:   t.TeamRole,
		}); err != nil {
			slog.Warn("team mapping failed", "team_id", t.TeamID, "err", err)
		}
	}
}
