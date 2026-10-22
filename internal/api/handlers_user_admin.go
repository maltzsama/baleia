package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity/users"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// SuspendUser — POST /users/{id}/suspend (#258). The account is suspended,
// never deleted; its refresh tokens and API keys die immediately.
func (h *AuthHandlers) SuspendUser(ctx context.Context, request SuspendUserRequestObject) (SuspendUserResponseObject, error) {
	uid := pgtype.UUID{Bytes: request.Id, Valid: true}
	ev := audit.Event{Action: audit.UserSuspend, ResourceType: "user", ResourceID: idString(uid)}
	if auth.RoleFromContext(ctx) != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return SuspendUser403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "admin only"}}, nil
	}

	asked := ""
	if request.Body != nil && request.Body.Reason != nil {
		asked = string(*request.Body.Reason)
	}
	// The suspension, the end of its sessions and the deletion of its API keys
	// commit together.
	user, updated, reason, err := h.users.Suspend(ctx, uid, asked)
	if errors.Is(err, users.ErrNotFound) {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return SuspendUser404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "user not found"}}, nil
	}
	if err != nil {
		return nil, err
	}

	ev.Changes = map[string]audit.Change{"status": {From: user.Status, To: auth.StatusSuspended}}
	h.audited(ctx, ev.Allowed())
	h.metrics.directory.Suspended(reason)
	return SuspendUser200JSONResponse(dbUserToPublic(updated)), nil
}

// ReinstateUser — POST /users/{id}/reinstate (#258). An admin override can
// lift any suspension, including a directory one.
func (h *AuthHandlers) ReinstateUser(ctx context.Context, request ReinstateUserRequestObject) (ReinstateUserResponseObject, error) {
	uid := pgtype.UUID{Bytes: request.Id, Valid: true}
	ev := audit.Event{Action: audit.UserReinstate, ResourceType: "user", ResourceID: idString(uid)}
	if auth.RoleFromContext(ctx) != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return ReinstateUser403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "admin only"}}, nil
	}

	user, updated, err := h.users.Reinstate(ctx, uid)
	if errors.Is(err, users.ErrNotFound) {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return ReinstateUser404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "user not found"}}, nil
	}
	if err != nil {
		return nil, err
	}

	ev.Changes = map[string]audit.Change{"status": {From: user.Status, To: auth.StatusActive}}
	h.audited(ctx, ev.Allowed())
	return ReinstateUser200JSONResponse(dbUserToPublic(updated)), nil
}

// ListDirectoryReconcileRuns — GET /admin/directory-reconcile-runs (#258).
func (h *AuthHandlers) ListDirectoryReconcileRuns(ctx context.Context, request ListDirectoryReconcileRunsRequestObject) (ListDirectoryReconcileRunsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListDirectoryReconcileRuns401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if err := p.RequireAdmin(); err != nil {
		return ListDirectoryReconcileRuns403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "administrator role required"}}, nil
	}

	limit := 20
	if request.Params.Limit != nil {
		if *request.Params.Limit < 1 || *request.Params.Limit > 100 {
			return ListDirectoryReconcileRuns422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "limit must be between 1 and 100")}, nil
		}
		limit = *request.Params.Limit
	}
	var providerID pgtype.UUID
	if request.Params.ProviderId != nil {
		providerID = pgtype.UUID{Bytes: *request.Params.ProviderId, Valid: true}
	}

	rows, err := h.users.ReconcileRuns(ctx, providerID, int32(limit))
	if err != nil {
		return nil, err
	}

	out := make(ListDirectoryReconcileRuns200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, directoryRunToPublic(r))
	}
	return out, nil
}

func directoryRunToPublic(r db.DirectoryReconcileRun) DirectoryReconcileRun {
	out := DirectoryReconcileRun{
		Id:         openapi_types.UUID(r.ID.Bytes),
		ProviderId: openapi_types.UUID(r.ProviderID.Bytes),
		StartedAt:  pgTimestampToTime(r.StartedAt),
		Seen:       int(r.Seen),
		Suspended:  int(r.Suspended),
		Reinstated: int(r.Reinstated),
		Actions:    []DirectoryReconcileAction{},
	}
	if r.FinishedAt.Valid {
		t := r.FinishedAt.Time
		out.FinishedAt = &t
	}
	if r.Outcome != nil {
		out.Outcome = DirectoryReconcileRunOutcome(*r.Outcome)
	}
	out.Error = r.Error
	if len(r.Actions) > 0 {
		if err := json.Unmarshal(r.Actions, &out.Actions); err != nil {
			out.Actions = []DirectoryReconcileAction{}
		}
	}
	return out
}
