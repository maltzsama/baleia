package api

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// targetError is a target that cannot be used: 404 when it is not a target of
// the workspace (no existence oracle), else 422 with a code.
type targetError struct {
	notFound bool
	code     ErrorCode
	message  string
}

func (e *targetError) Error() string { return string(e.code) + ": " + e.message }

func (e *targetError) unprocessable() UnprocessableEntityJSONResponse {
	field, msg := "target", e.message
	return UnprocessableEntityJSONResponse{
		Code:    e.code,
		Message: e.message,
		Errors: []struct {
			Field   *string `json:"field,omitempty"`
			Message *string `json:"message,omitempty"`
		}{{Field: &field, Message: &msg}},
	}
}

var errTargetNotFound = &targetError{notFound: true, code: ErrorCodeNotFound, message: "target not found"}

func usable(r db.ListWorkspaceTargetsRow) bool {
	return r.BindingEnabled && r.EngineEnabled && r.CatalogEnabled
}

func rowTarget(r db.ListWorkspaceTargetsRow, schema string) queryengine.Target {
	return queryengine.Target{
		EngineID:      r.EngineID,
		EngineKind:    r.EngineKind,
		EngineName:    r.EngineName,
		BindingID:     r.BindingID,
		CatalogID:     r.CatalogID,
		CatalogName:   r.CatalogName,
		EngineCatalog: r.EngineCatalogName,
		Schema:        schema,
		ReadOnly:      r.Permission == "read",
	}
}

// ResolveTarget decides where a run executes (#13):
//
//  1. an explicit binding, or a catalog (with engine_id when it is bound to
//     several engines);
//  2. else the workspace default target;
//  3. else the workspace's only target (one catalog with one binding);
//  4. else 422 target_required.
//
// The binding's catalog must be one the workspace may use (#16), else 404; the
// binding, engine and catalog must be enabled, else 422 target_unavailable. A
// read grant makes the target read-only. There is no fallback to the
// workspace owner's first catalog or to polaris_catalog_name.
func (h *core) ResolveTarget(ctx context.Context, ws db.Workspace, req *TargetRequest) (queryengine.Target, error) {
	rows, err := h.queries.ListWorkspaceTargets(ctx, ws.ID)
	if err != nil {
		return queryengine.Target{}, err
	}
	schema := ""
	if req != nil && req.Schema != nil {
		schema = *req.Schema
	}
	unavailable := &targetError{code: ErrorCodeTargetUnavailable, message: "the target's binding, engine or catalog is disabled"}

	switch {
	case req != nil && req.BindingId != nil:
		for _, r := range rows {
			if r.BindingID.Bytes == *req.BindingId {
				if !usable(r) {
					return queryengine.Target{}, unavailable
				}
				return rowTarget(r, schema), nil
			}
		}
		return queryengine.Target{}, errTargetNotFound

	case req != nil && req.CatalogId != nil:
		var bound, live []db.ListWorkspaceTargetsRow
		for _, r := range rows {
			if r.CatalogID.Bytes != *req.CatalogId {
				continue
			}
			if req.EngineId != nil && r.EngineID.Bytes != *req.EngineId {
				continue
			}
			bound = append(bound, r)
			if usable(r) {
				live = append(live, r)
			}
		}
		switch {
		case len(bound) == 0:
			return queryengine.Target{}, errTargetNotFound
		case len(live) == 0:
			return queryengine.Target{}, unavailable
		case len(live) > 1:
			return queryengine.Target{}, &targetError{code: ErrorCodeEngineRequired, message: "the catalog is bound to several engines; set engine_id or binding_id"}
		}
		return rowTarget(live[0], schema), nil
	}

	if ws.DefaultBindingID.Valid {
		if schema == "" && ws.DefaultSchema != nil {
			schema = *ws.DefaultSchema
		}
		for _, r := range rows {
			if r.BindingID == ws.DefaultBindingID && usable(r) {
				return rowTarget(r, schema), nil
			}
		}
		return queryengine.Target{}, &targetError{code: ErrorCodeTargetUnavailable, message: "the workspace default target is no longer available"}
	}

	var live []db.ListWorkspaceTargetsRow
	catalogs := map[[16]byte]bool{}
	for _, r := range rows {
		if usable(r) {
			live = append(live, r)
			catalogs[r.CatalogID.Bytes] = true
		}
	}
	if len(live) == 1 && len(catalogs) == 1 {
		return rowTarget(live[0], schema), nil
	}
	return queryengine.Target{}, &targetError{code: ErrorCodeTargetRequired, message: "choose a target: the workspace has no default and more than one, or no, target"}
}

// ListWorkspaceTargets returns the usable targets of a workspace to its members.
func (h *WorkspaceHandlers) ListWorkspaceTargets(ctx context.Context, request ListWorkspaceTargetsRequestObject) (ListWorkspaceTargetsResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, request.Id); errors.Is(err, ErrWorkspaceNotFound) {
		return ListWorkspaceTargets404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	wsID := pgtype.UUID{Bytes: request.Id, Valid: true}
	ws, err := h.queries.GetWorkspaceByID(ctx, wsID)
	if err != nil {
		return nil, err
	}
	rows, err := h.queries.ListWorkspaceTargets(ctx, wsID)
	if err != nil {
		return nil, err
	}
	out := make([]WorkspaceTarget, 0, len(rows))
	for _, r := range rows {
		if !usable(r) {
			continue
		}
		out = append(out, WorkspaceTarget{
			BindingId:         openapi_types.UUID(r.BindingID.Bytes),
			CatalogId:         openapi_types.UUID(r.CatalogID.Bytes),
			CatalogName:       r.CatalogName,
			EngineId:          openapi_types.UUID(r.EngineID.Bytes),
			EngineName:        r.EngineName,
			EngineKind:        r.EngineKind,
			EngineCatalogName: r.EngineCatalogName,
			Mode:              WorkspaceTargetMode(r.Mode),
			Permission:        WorkspaceTargetPermission(r.Permission),
			IsDefault:         ws.DefaultBindingID.Valid && ws.DefaultBindingID == r.BindingID,
		})
	}
	return ListWorkspaceTargets200JSONResponse(out), nil
}

// asTargetError reports whether err is a target failure.
func asTargetError(err error) (*targetError, bool) {
	var te *targetError
	ok := errors.As(err, &te)
	return te, ok
}
