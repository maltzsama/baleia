// Package execscope computes the workspace catalog scope a user-identity
// session may reach (ADR 0018 §5). It is the only place outside queryengine
// that builds a session's AllowedCatalogsSource; call sites pass its result
// into SessionSpec instead of setting Identity.UserID without a scope.
package execscope

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store lists the workspace's targets; *db.Queries implements it.
type Store interface {
	ListWorkspaceTargets(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListWorkspaceTargetsRow, error)
}

// For returns engine_scope(workspace, engine): the engine-side catalog names
// of every enabled, active binding the workspace may use on that engine. The
// source re-reads the scope before each statement, so a revocation stops the
// next statement instead of the next run.
func For(store Store, workspaceID, engineID pgtype.UUID) func(context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		rows, err := store.ListWorkspaceTargets(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		var scope []string
		for _, r := range rows {
			if r.EngineID.Bytes == engineID.Bytes && r.BindingEnabled && r.EngineEnabled && r.CatalogEnabled {
				scope = append(scope, r.EngineCatalogName)
			}
		}
		return scope, nil
	}
}
