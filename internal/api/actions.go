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

// tableMaintenanceActions classifies the same check the maintenance handlers
// run (#500): reading the catalog's monitoring, and writing it.
func (h *core) tableMaintenanceActions(ctx context.Context, connectionID, catalogID pgtype.UUID) []TableMaintenanceAllowedAction {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return nil
	}
	acts := []TableMaintenanceAllowedAction{TableMaintenanceAllowedActionView}
	if _, _, err := h.authz().CatalogOfConnection(ctx, p, connectionID, catalogID, authz.ActWrite); err == nil {
		acts = append(acts, TableMaintenanceAllowedActionUpdate)
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

func (h *core) workspaceActions(ctx context.Context, id pgtype.UUID) ([]WorkspaceAllowedAction, authz.WorkspaceRole) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return nil, authz.WorkspaceNone
	}
	role, err := h.authz().WorkspaceRole(ctx, p, id)
	if err != nil || role == authz.WorkspaceNone {
		return nil, authz.WorkspaceNone
	}
	acts := []WorkspaceAllowedAction{WorkspaceAllowedActionView, WorkspaceAllowedActionRun}
	if role >= authz.WorkspaceEditor {
		acts = append(acts, WorkspaceAllowedActionEditContent)
	}
	if role >= authz.WorkspaceAdmin {
		acts = append(acts, WorkspaceAllowedActionUpdate, WorkspaceAllowedActionDelete,
			WorkspaceAllowedActionManageGrants, WorkspaceAllowedActionSetDefaults, WorkspaceAllowedActionCreateSpace,
			WorkspaceAllowedActionManageMembers)
	}
	return acts, role
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
	if acts, role := h.workspaceActions(ctx, w.ID); acts != nil {
		out.AllowedActions = &acts
		r := WorkspaceRole(role.String())
		out.Role = &r
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
