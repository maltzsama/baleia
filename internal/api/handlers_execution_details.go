package api

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/queryprofile"
	"github.com/galileostudio/baleia/internal/workbench/executions"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// GetExecutionDetails — GET /workspaces/{id}/executions/{execution_id}/details
// Per-statement rows, bytes and duration, plus the Trino profile Lens recorded
// for each statement (B3, #532). A direct engine has no profile; the endpoint
// still reports its rows and duration.
func (h *QueryHandlers) GetExecutionDetails(ctx context.Context, request GetExecutionDetailsRequestObject) (GetExecutionDetailsResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); err != nil {
		return GetExecutionDetails401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "workspace not found"}}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	executionID := pgtype.UUID{Bytes: request.ExecutionId, Valid: true}

	execution, err := h.executions.Get(ctx, wsID, executionID)
	if errors.Is(err, executions.ErrNotFound) {
		return GetExecutionDetails404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "execution not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if !h.canSeeExecution(ctx, wsID, execution) {
		return GetExecutionDetails404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "execution not found"}}, nil
	}
	statements, err := h.executions.Statements(ctx, wsID, executionID)
	if err != nil {
		return nil, err
	}

	engineKind := ""
	if target := executionTarget(execution); target != nil && target.EngineKind != nil {
		engineKind = *target.EngineKind
	}
	out := ExecutionDetails{
		ExecutionId: openapi_types.UUID(execution.ID.Bytes),
		DurationMs:  intFrom64(execution.DurationMs),
		Statements:  make([]ExecutionStatement, 0, len(statements)),
		Timeline: ExecutionTimeline{
			QueueWaitMs: intFrom64(execution.QueueWaitMs),
			TotalMs:     intFrom64(execution.DurationMs),
		},
	}
	if engineKind != "" {
		out.EngineKind = &engineKind
	}

	var queued, planning, executionMs int
	var haveQueued, havePlanning, haveExecution bool
	for _, s := range statements {
		st := ExecutionStatement{
			Id:            openapi_types.UUID(s.ID.Bytes),
			Position:      int(s.Position),
			Sql:           s.Sql,
			Status:        ExecutionStatementStatus(s.Status),
			EngineQueryId: s.EngineQueryID,
			RowsReturned:  intFrom64(s.RowsReturned),
			ResultBytes:   intFrom64(s.ResultBytes),
			DurationMs:    intFrom64(s.DurationMs),
		}
		if engineKind == "trino" && s.EngineQueryID != nil && *s.EngineQueryID != "" {
			st.Profile = h.trinoProfile(ctx, *s.EngineQueryID)
		}
		if p := st.Profile; p != nil {
			if p.QueuedMs != nil {
				queued += *p.QueuedMs
				haveQueued = true
			}
			if p.PlanningMs != nil {
				planning += *p.PlanningMs
				havePlanning = true
			}
			if p.ExecutionMs != nil {
				executionMs += *p.ExecutionMs
				haveExecution = true
			}
		}
		out.Statements = append(out.Statements, st)
	}
	if haveQueued {
		out.Timeline.EngineQueuedMs = &queued
	}
	if havePlanning {
		out.Timeline.PlanningMs = &planning
	}
	if haveExecution {
		out.Timeline.ExecutionMs = &executionMs
	}
	return GetExecutionDetails200JSONResponse(out), nil
}

// trinoProfile reads the snapshot Lens wrote for one engine query; absent is
// nil, never an error (the UI says the profile is unavailable).
func (h *QueryHandlers) trinoProfile(ctx context.Context, queryID string) *ExecutionProfile {
	snap, err := h.queries.GetTrinoQuerySnapshot(ctx, queryID)
	if err != nil {
		return nil
	}
	return executionProfile(queryprofile.FromPayload(snap.RawTrinoPayload))
}

// executionProfile maps the neutral profile to the API model.
func executionProfile(p *queryprofile.Profile) *ExecutionProfile {
	if p == nil {
		return nil
	}
	out := &ExecutionProfile{
		QueuedMs:        intFrom64(p.QueuedMs),
		AnalysisMs:      intFrom64(p.AnalysisMs),
		PlanningMs:      intFrom64(p.PlanningMs),
		ExecutionMs:     intFrom64(p.ExecutionMs),
		CpuMs:           intFrom64(p.CPUMs),
		PeakMemoryBytes: intFrom64(p.PeakMemoryBytes),
		SpilledBytes:    intFrom64(p.SpilledBytes),
		InputBytes:      intFrom64(p.InputBytes),
		InputRows:       intFrom64(p.InputRows),
		OutputBytes:     intFrom64(p.OutputBytes),
		OutputRows:      intFrom64(p.OutputRows),
	}
	if len(p.Inputs) > 0 {
		inputs := make([]ExecutionInput, 0, len(p.Inputs))
		for _, in := range p.Inputs {
			catalog, schema, table := in.Catalog, in.Schema, in.Table
			inputs = append(inputs, ExecutionInput{
				Catalog: &catalog, Schema: &schema, Table: &table,
				InputBytes: intFrom64(in.Bytes), InputRows: intFrom64(in.Rows),
			})
		}
		out.Inputs = &inputs
	}
	return out
}
