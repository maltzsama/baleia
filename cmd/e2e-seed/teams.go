//go:build e2e

package main

import (
	"context"
	"strings"

	"github.com/galileostudio/baleia/internal/db"
)

func ensureTeam(ctx context.Context, q db.Querier, name string, createdBy db.UUID) (db.Team, error) {
	teams, err := q.ListTeams(ctx)
	if err == nil {
		for _, team := range teams {
			if team.Name == name {
				return team, nil
			}
		}
	}
	return q.CreateTeam(ctx, db.CreateTeamParams{Name: name, CreatedBy: createdBy})
}

func ensureTeamMember(ctx context.Context, q db.Querier, teamID, userID db.UUID, role string) error {
	_, err := q.AddTeamMember(ctx, db.AddTeamMemberParams{
		TeamID: teamID,
		UserID: userID,
		Role:   role,
	})
	if err != nil && strings.Contains(err.Error(), "duplicate key") {
		return nil
	}
	return err
}

func ensureSharedWorkspace(ctx context.Context, q db.Querier, name string, teamID db.UUID) (db.Workspace, error) {
	polarisName := "e2e_shared"
	if ws, err := q.GetWorkspaceByPolarisName(ctx, polarisName); err == nil {
		return ws, nil
	}
	return q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name:               name,
		Type:               "shared",
		PolarisCatalogName: polarisName,
		OwnerTeamID:        teamID,
	})
}
