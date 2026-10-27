package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// Allowed actions (#104): each list is computed by the same authz predicates
// that enforce the action, so the UI never contradicts the server. The checker
// is the single implementation; these helpers only classify its outcome.

func (h *core) catalogActions(ctx context.Context, id pgtype.UUID) []CatalogAllowedAction {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return nil
	}
	if _, _, err := h.authz().Catalog(ctx, p, id, authz.ActRead); err != nil {
		return nil
	}
	acts := []CatalogAllowedAction{CatalogAllowedActionView, CatalogAllowedActionBrowse}
	if _, _, err := h.authz().Catalog(ctx, p, id, authz.ActWrite); err == nil {
		acts = append(acts, CatalogAllowedActionUpdate, CatalogAllowedActionDelete,
			CatalogAllowedActionSyncMetadata, CatalogAllowedActionGrant, CatalogAllowedActionCreateBinding)
	}
	return acts
}

func (h *core) catalogBindingActions(ctx context.Context, b dbCatalogBindingRef) []CatalogBindingAllowedAction {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return nil
	}
	if _, _, err := h.authz().Catalog(ctx, p, b.catalogID, authz.ActRead); err != nil {
		return nil
	}
	acts := []CatalogBindingAllowedAction{CatalogBindingAllowedActionView}
	if _, _, err := h.authz().Catalog(ctx, p, b.catalogID, authz.ActWrite); err == nil {
		acts = append(acts, CatalogBindingAllowedActionUpdate, CatalogBindingAllowedActionDelete, CatalogBindingAllowedActionSync)
	}
	return acts
}

type dbCatalogBindingRef struct{ catalogID pgtype.UUID }

func (h *core) catalogConnectionActions(ctx context.Context, id pgtype.UUID) []CatalogConnectionAllowedAction {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return nil
	}
	if _, err := h.authz().Connection(ctx, p, id, authz.ActRead); err != nil {
		return nil
	}
	acts := []CatalogConnectionAllowedAction{CatalogConnectionAllowedActionView}
	if _, err := h.authz().Connection(ctx, p, id, authz.ActWrite); err == nil {
		acts = append(acts, CatalogConnectionAllowedActionUpdate, CatalogConnectionAllowedActionDelete,
			CatalogConnectionAllowedActionTest, CatalogConnectionAllowedActionRegisterEngine, CatalogConnectionAllowedActionCreateCatalog)
	}
	return acts
}

func (h *core) engineActions(ctx context.Context, id pgtype.UUID) []EngineAllowedAction {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return nil
	}
	if _, _, err := h.authz().Engine(ctx, p, id, authz.ActRead); err != nil {
		return nil
	}
	acts := []EngineAllowedAction{EngineAllowedActionView}
	if _, _, err := h.authz().Engine(ctx, p, id, authz.ActWrite); err == nil {
		acts = append(acts, EngineAllowedActionUpdate, EngineAllowedActionDelete,
			EngineAllowedActionTest, EngineAllowedActionReconcile, EngineAllowedActionBindInto)
	}
	return acts
}

func (h *core) workspaceActions(ctx context.Context, id pgtype.UUID) []WorkspaceAllowedAction {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return nil
	}
	if _, err := h.authz().Workspace(ctx, p, id, authz.ActRead); err != nil {
		return nil
	}
	acts := []WorkspaceAllowedAction{WorkspaceAllowedActionView, WorkspaceAllowedActionRun}
	if _, err := h.authz().Workspace(ctx, p, id, authz.ActWrite); err == nil {
		acts = append(acts, WorkspaceAllowedActionUpdate, WorkspaceAllowedActionDelete,
			WorkspaceAllowedActionManageGrants, WorkspaceAllowedActionSetDefaults, WorkspaceAllowedActionCreateSpace)
	}
	return acts
}

func (h *core) withConnectionActions(ctx context.Context, c db.CatalogConnection) CatalogConnection {
	out := dbCatalogConnectionToPublic(h.logger, c)
	if acts := h.catalogConnectionActions(ctx, c.ID); acts != nil {
		out.AllowedActions = &acts
	}
	return out
}

func (h *core) withWorkspaceActions(ctx context.Context, w db.Workspace) Workspace {
	out := dbWorkspaceToPublic(w)
	if acts := h.workspaceActions(ctx, w.ID); acts != nil {
		out.AllowedActions = &acts
	}
	return out
}

func (h *core) withEngineActions(ctx context.Context, e db.Engine) Engine {
	out := engineToPublic(e)
	if acts := h.engineActions(ctx, e.ID); acts != nil {
		out.AllowedActions = &acts
	}
	return out
}
