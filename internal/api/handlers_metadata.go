package api

import (
	"context"
	"errors"
	"log/slog"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// SyncCatalogMetadata — POST /catalogs/{catalog_id}/metadata/sync
// Starts one metadata pass of the catalog on the worker (#15). One pass per
// catalog runs at a time: the workflow id is the catalog's.
func (h *Handlers) SyncCatalogMetadata(ctx context.Context, request SyncCatalogMetadataRequestObject) (SyncCatalogMetadataResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return SyncCatalogMetadata401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	ev := audit.Event{Action: audit.MetadataSync, ResourceType: "catalog", ResourceID: idString(id)}
	ctx = audit.WithAction(ctx, ev.Action)
	if _, _, err := h.authz().Catalog(ctx, p, id, authz.ActWrite); err != nil {
		nf, fb := catalogAccessError(err)
		switch {
		case nf:
			return SyncCatalogMetadata404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
		case fb:
			return SyncCatalogMetadata403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "not allowed to sync this catalog"}}, nil
		}
		return nil, err
	}
	catalogID := uuid.UUID(request.CatalogId).String()
	wid := workflows.MetadataSyncWorkflowID(catalogID)
	_, err = h.temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        wid,
		TaskQueue: "baleia-task-queue",
		// A running pass of the same catalog is an answer, not a new run.
		WorkflowExecutionErrorWhenAlreadyStarted: true,
	}, workflows.MetadataSyncWorkflow, workflows.MetadataSyncInput{CatalogID: catalogID})
	var running *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &running) {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return SyncCatalogMetadata409JSONResponse{Code: ErrorCodeSyncInProgress, Message: "a metadata pass of this catalog is already running"}, nil
	}
	if err != nil {
		return nil, err
	}
	h.audited(ctx, ev.Allowed())
	return SyncCatalogMetadata202JSONResponse{CatalogId: request.CatalogId, WorkflowId: wid}, nil
}

// ListCatalogMetadataSyncRuns — GET /catalogs/{catalog_id}/metadata/sync-runs
// Anyone who may read the catalog sees how its passes ended (codes only).
func (h *Handlers) ListCatalogMetadataSyncRuns(ctx context.Context, request ListCatalogMetadataSyncRunsRequestObject) (ListCatalogMetadataSyncRunsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListCatalogMetadataSyncRuns401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	if _, _, err := h.authz().Catalog(ctx, p, id, authz.ActRead); err != nil {
		if nf, _ := catalogAccessError(err); nf {
			return ListCatalogMetadataSyncRuns404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
		}
		return nil, err
	}
	limit := 20
	if request.Params.Limit != nil && *request.Params.Limit >= 1 && *request.Params.Limit <= 100 {
		limit = *request.Params.Limit
	}
	rows, err := h.queries.ListMetadataSyncRuns(ctx, db.ListMetadataSyncRunsParams{CatalogID: id, Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make(ListCatalogMetadataSyncRuns200JSONResponse, 0, len(rows))
	for _, r := range rows {
		run := MetadataSyncRun{
			Id:         openapi_types.UUID(r.ID.Bytes),
			CatalogId:  openapi_types.UUID(r.CatalogID.Bytes),
			Status:     MetadataSyncRunStatus(r.Status),
			SourcePath: MetadataSyncRunSourcePath(r.SourcePath),
			StartedAt:  r.StartedAt.Time,
			ErrorCode:  r.ErrorCode,
			TablesSeen: intPtr(r.TablesSeen), TablesChanged: intPtr(r.TablesChanged), TablesRemoved: intPtr(r.TablesRemoved),
		}
		if r.FinishedAt.Valid {
			t := r.FinishedAt.Time
			run.FinishedAt = &t
		}
		out = append(out, run)
	}
	return out, nil
}

func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

// startMetadataSync starts a catalog's metadata pass right after the catalog
// was created or changed (#17), so its metadata does not wait for the
// schedule. A pass already running is reused (same workflow id). It never
// fails the request: a failure is logged and the schedule catches up.
func (h *Handlers) startMetadataSync(ctx context.Context, catalogID pgtype.UUID) {
	if h.temporalClient == nil {
		return
	}
	id := uuid.UUID(catalogID.Bytes).String()
	_, err := h.temporalClient.ExecuteWorkflow(context.WithoutCancel(ctx), client.StartWorkflowOptions{
		ID:                       workflows.MetadataSyncWorkflowID(id),
		TaskQueue:                "baleia-task-queue",
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, workflows.MetadataSyncWorkflow, workflows.MetadataSyncInput{CatalogID: id})
	if err != nil {
		slog.Warn("start metadata sync", "catalog_id", id, "err", err)
	}
}
