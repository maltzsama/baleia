package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/bindings/stored"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/engines/manage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ReconcileEngine — POST /engines/{id}/reconcile (#37). Admins only; a dry
// run unless dry_run=false.
func (h *CatalogHandlers) ReconcileEngine(ctx context.Context, request ReconcileEngineRequestObject) (ReconcileEngineResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ReconcileEngine401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if err := p.RequireAdmin(); err != nil {
		return ReconcileEngine403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "administrator role required"}}, nil
	}
	id := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, err := h.queries.GetEngineByID(ctx, id); errors.Is(err, pgx.ErrNoRows) {
		return ReconcileEngine404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "engine not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	run, unsupported, err := h.reconcile(ctx, id, request.Params.DryRun == nil || *request.Params.DryRun)
	if err != nil {
		return nil, err
	}
	if unsupported != nil {
		return ReconcileEngine422JSONResponse{UnprocessableEntityJSONResponse: *unsupported}, nil
	}
	if n := h.reconcileMaxActions(); n > 0 {
		run.MaxActions = &n
	}
	return ReconcileEngine200JSONResponse(run), nil
}

// ListReconcileRuns — GET /engines/{id}/reconcile-runs. Admins only.
func (h *CatalogHandlers) ListReconcileRuns(ctx context.Context, request ListReconcileRunsRequestObject) (ListReconcileRunsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListReconcileRuns401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if err := p.RequireAdmin(); err != nil {
		return ListReconcileRuns403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "administrator role required"}}, nil
	}
	limit := 0
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	rows, err := h.engineAdmin.ReconcileRuns(ctx, pgtype.UUID{Bytes: request.Id, Valid: true}, limit)
	if errors.Is(err, manage.ErrNotFound) {
		return ListReconcileRuns404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: err.Error()}}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make(ListReconcileRuns200JSONResponse, 0, len(rows))
	for _, r := range rows {
		run, err := reconcileRunToPublic(r)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}

// reconcile runs and persists one reconcile of a federated engine;
// unsupported is set for an engine Baleia cannot reconcile.
func (h *CatalogHandlers) reconcile(ctx context.Context, engineID pgtype.UUID, dryRun bool) (ReconcileRun, *UnprocessableEntityJSONResponse, error) {
	eng, err := h.queries.GetEngineByID(ctx, engineID)
	if err != nil {
		return ReconcileRun{}, nil, err
	}
	if eng.Kind != h.binder.EngineKind() {
		u := unprocessable(ErrorCodeEngineUnsupported, "engine kind "+eng.Kind+" has no binder: only federated engines with bindings are reconciled")
		return ReconcileRun{}, &u, nil
	}
	cluster, err := h.queries.GetTrinoClusterByEngine(ctx, eng.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		u := unprocessable(ErrorCodeEngineUnsupported, "the engine has no Trino cluster")
		return ReconcileRun{}, &u, nil
	}
	if err != nil {
		return ReconcileRun{}, nil, err
	}
	r := bindings.Reconciler{
		Lifecycle:  h.lifecycleOn(cluster, eng),
		Store:      h.queries,
		Project:    stored.Projector{Store: h.queries, Keyring: h.keyring, Providers: h.connections, Binder: h.binder}.Project,
		MaxActions: h.reconcileMaxActions(),
		OnAction: func(ctx context.Context, a bindings.Action) {
			ev := audit.Event{Action: audit.CatalogReconcile, ResourceType: "engine", ResourceID: idString(eng.ID),
				Changes: map[string]audit.Change{"catalog": {To: a.Catalog}, "kind": {To: a.Kind}, "reason": {To: a.Reason}}}
			if a.Result == bindings.ResultFailed {
				h.audited(ctx, ev.Failed(audit.ReasonEngineError))
				return
			}
			h.audited(ctx, ev.Allowed())
		},
	}
	rep, runErr := r.Run(ctx, dryRun)
	h.catalogCache.Clear()
	row, err := stored.SaveRun(ctx, h.queries, eng.ID, rep, runErr)
	if err != nil {
		return ReconcileRun{}, nil, err
	}
	run, err := reconcileRunToPublic(row)
	return run, nil, err
}

func reconcileRunToPublic(r db.CatalogReconcileRun) (ReconcileRun, error) {
	out := ReconcileRun{
		Id: openapi_types.UUID(r.ID.Bytes), EngineId: openapi_types.UUID(r.EngineID.Bytes),
		StartedAt: r.StartedAt.Time, FinishedAt: r.FinishedAt.Time, DryRun: r.DryRun,
		Adopted: r.Adopted, Actions: []ReconcileAction{}, Error: r.Error,
	}
	if out.Adopted == nil {
		out.Adopted = []string{}
	}
	return out, json.Unmarshal(r.Actions, &out.Actions)
}

func (h *core) reconcileMaxActions() int {
	return h.settings.Ops.ReconcileMaxActions // zero: bindings.DefaultMaxActions
}
