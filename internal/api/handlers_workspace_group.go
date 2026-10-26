package api

import "github.com/galileostudio/baleia/internal/workspaces/teams"

// WorkspaceHandlers serves workspaces, teams, spaces and their members (#365).
type WorkspaceHandlers struct {
	*core
	// teams stores teams and their members (#33).
	teams *teams.Service
}

func newWorkspaceHandlers(c *core) *WorkspaceHandlers {
	return &WorkspaceHandlers{core: c, teams: teams.New(c.queries)}
}
