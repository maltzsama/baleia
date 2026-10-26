package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	wsmanage "github.com/galileostudio/baleia/internal/workspaces/manage"
	"github.com/galileostudio/baleia/internal/workspaces/teams"
)

// WorkspaceHandlers serves workspaces, teams, spaces and their members (#365).
type WorkspaceHandlers struct {
	*core
	// teams stores teams and their members (#33).
	teams *teams.Service
	// workspaces stores workspaces and their catalog grants (#33, #36).
	workspaces *wsmanage.Service
}

func newWorkspaceHandlers(c *core) *WorkspaceHandlers {
	tx := func(ctx context.Context, fn func(wsmanage.TxStore) error) error {
		return c.inTx(ctx, func(q db.Querier) error { return fn(q) })
	}
	return &WorkspaceHandlers{core: c, teams: teams.New(c.queries), workspaces: wsmanage.New(c.queries, tx)}
}
