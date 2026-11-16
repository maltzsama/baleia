package api

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/authz"
)

// GetWorkspaceBIStatus — GET /workspaces/{id}/bi-status (#1102). Any workspace
// member may read the workspace's BI readiness: the global gateway facts, the
// workspace switch, the available Trino targets and the caller's key
// permissions (ADR 0017). It exposes no secret or host internals.
func (h *WorkspaceHandlers) GetWorkspaceBIStatus(ctx context.Context, request GetWorkspaceBIStatusRequestObject) (GetWorkspaceBIStatusResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); errors.Is(err, ErrWorkspaceNotFound) {
		return GetWorkspaceBIStatus404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetWorkspaceBIStatus401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	role, err := h.authz().WorkspaceRole(ctx, p, wsID)
	if errors.Is(err, authz.ErrNotFound) {
		return GetWorkspaceBIStatus404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	ws, err := h.access.Workspace(ctx, wsID)
	if err != nil {
		return nil, err
	}
	readiness, err := h.biGatewayReadiness(ctx)
	if err != nil {
		return nil, err
	}
	targets, err := h.access.Targets(ctx, wsID)
	if err != nil {
		return nil, err
	}
	trinoTargets := 0
	for _, target := range targets {
		if target.EngineKind == "trino" && usable(target) {
			trinoTargets++
		}
	}

	// The blockers combine the global gateway state with the workspace switch
	// and the availability of a Trino target to point a key at.
	blocked := make([]WorkspaceBIGatewayBlocker, 0, len(readiness.Blockers)+2)
	for _, blocker := range readiness.Blockers {
		blocked = append(blocked, WorkspaceBIGatewayBlocker(blocker))
	}
	if !ws.BIEnabled {
		blocked = append(blocked, WorkspaceBIGatewayBlockerWorkspaceBiDisabled)
	}
	if trinoTargets == 0 {
		blocked = append(blocked, WorkspaceBIGatewayBlockerNoTrinoTargets)
	}
	state := WorkspaceBIStatusStateReady
	if len(blocked) > 0 {
		state = WorkspaceBIStatusStateBlocked
	}

	return GetWorkspaceBIStatus200JSONResponse(WorkspaceBIStatus{
		WorkspaceBiEnabled: ws.BIEnabled,
		Gateway:            readiness.toAPI(),
		TrinoTargets:       WorkspaceBITrinoTargets{Available: trinoTargets > 0, Count: trinoTargets},
		// An agent cannot create BI keys; a member creates keys for themselves
		// and needs a Trino target to point at.
		CanCreateKeys: !p.IsAgent() && trinoTargets > 0,
		// Only a workspace admin administers every key of the workspace.
		CanManageKeys: role >= authz.WorkspaceAdmin,
		State:         state,
		BlockedBy:     blocked,
	}), nil
}
