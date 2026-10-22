package api

// WorkspaceHandlers serves workspaces, teams, spaces and their members (#365).
type WorkspaceHandlers struct {
	*core
}

func newWorkspaceHandlers(c *core) *WorkspaceHandlers { return &WorkspaceHandlers{core: c} }
