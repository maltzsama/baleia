package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/galileostudio/baleia/internal/workbench/executions"
	"github.com/galileostudio/baleia/internal/workload"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.temporal.io/sdk/client"
)

// workloadClasses is the order the queue API reports and accepts.
var workloadClasses = []queryengine.WorkloadClass{
	queryengine.ClassInteractive,
	queryengine.ClassHeavy,
	queryengine.ClassBackground,
}

// GetEngineQueues — GET /engines/{id}/queues (#540). Admins and the engine
// owner see the configured capacity and the live depth of every class.
func (h *CatalogHandlers) GetEngineQueues(ctx context.Context, request GetEngineQueuesRequestObject) (GetEngineQueuesResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetEngineQueues401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.Id, Valid: true}
	eng, _, err := h.authz().Engine(ctx, p, id, authz.ActRead)
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return GetEngineQueues404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	case errors.Is(err, authz.ErrForbidden):
		return GetEngineQueues403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only the engine owner or an admin may see its queues"}}, nil
	case err != nil:
		return nil, err
	}
	out, err := h.engineQueues(ctx, eng)
	if err != nil {
		return nil, err
	}
	return GetEngineQueues200JSONResponse(out), nil
}

// PutEngineQueues — PUT /engines/{id}/queues (#540). Writes the capacities,
// the queue timeouts and the optional ceiling, then signals the engine's
// admission workflow so the change applies without a restart.
func (h *CatalogHandlers) PutEngineQueues(ctx context.Context, request PutEngineQueuesRequestObject) (PutEngineQueuesResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return PutEngineQueues401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, _, err := h.authz().Engine(ctx, p, id, authz.ActWrite); errors.Is(err, authz.ErrNotFound) {
		return PutEngineQueues404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	} else if errors.Is(err, authz.ErrForbidden) {
		return PutEngineQueues403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only the engine owner or an admin may change its queues"}}, nil
	} else if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return PutEngineQueues422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "a body is required")}, nil
	}

	rows, caps, err := queueCapacityUpdate(id, request.Body)
	if err != nil {
		return PutEngineQueues422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
	}
	if err := h.inTx(ctx, func(q db.Querier) error {
		if err := q.DeleteEngineQueueCapacities(ctx, id); err != nil {
			return err
		}
		for _, row := range rows {
			if err := q.UpsertEngineQueueCapacity(ctx, row); err != nil {
				return err
			}
		}
		_, err := q.SetEngineMaxRunningTotal(ctx, db.SetEngineMaxRunningTotalParams{
			ID:              id,
			MaxRunningTotal: intToInt32(request.Body.MaxRunningTotal),
		})
		return err
	}); err != nil {
		return nil, err
	}

	h.audited(ctx, audit.Event{
		Action:       audit.WorkloadUpdate,
		ResourceType: "engine",
		ResourceID:   idString(id),
		Outcome:      audit.Allowed,
		Changes: map[string]audit.Change{
			"max_running_total": {To: request.Body.MaxRunningTotal},
			"classes":           {To: caps.Classes},
		},
	})
	h.signalAdmission(ctx, idString(id), caps)

	eng, err := h.queries.GetEngineByID(ctx, id)
	if err != nil {
		return nil, err
	}
	out, err := h.engineQueues(ctx, eng)
	if err != nil {
		return nil, err
	}
	return PutEngineQueues200JSONResponse(out), nil
}

// engineQueues merges the configured capacity (or its default) with the live
// state the admission workflow reports.
func (h *CatalogHandlers) engineQueues(ctx context.Context, eng db.Engine) (EngineQueues, error) {
	rows, err := h.queries.ListEngineQueueCapacities(ctx, eng.ID)
	if err != nil {
		return EngineQueues{}, err
	}
	configured := make(map[queryengine.WorkloadClass]*workload.Capacity, len(rows))
	for _, row := range rows {
		configured[queryengine.WorkloadClass(row.Class)] = &workload.Capacity{
			MaxRunning:   int(row.MaxRunning),
			MaxQueued:    int(row.MaxQueued),
			QueueTimeout: time.Duration(row.QueueTimeoutMs) * time.Millisecond,
		}
	}
	state, _ := h.admissionState(ctx, eng)

	out := EngineQueues{
		EngineId:        openapi_types.UUID(eng.ID.Bytes),
		MaxRunningTotal: int32ToIntPtr(eng.MaxRunningTotal),
		Classes:         make([]EngineQueue, 0, len(workloadClasses)),
	}
	for _, class := range workloadClasses {
		capacity := workload.Resolve(eng.Class, class, configured[class])
		running, queued, oldest := liveCounts(state, class)
		out.Classes = append(out.Classes, EngineQueue{
			Class:          EngineQueueClass(class),
			MaxRunning:     capacity.MaxRunning,
			MaxQueued:      capacity.MaxQueued,
			QueueTimeoutMs: int(capacity.QueueTimeout / time.Millisecond),
			Running:        running,
			Queued:         queued,
			OldestWaitMs:   oldest,
		})
	}
	return out, nil
}

// admissionState asks the engine's admission workflow for its live state. A
// missing workflow (workload management never used on this engine) is not an
// error: the queues are simply empty.
func (h *CatalogHandlers) admissionState(ctx context.Context, eng db.Engine) (workflows.AdmissionState, bool) {
	if h.temporalClient == nil {
		return workflows.AdmissionState{}, false
	}
	value, err := h.temporalClient.QueryWorkflow(ctx, workflows.AdmissionWorkflowID(idString(eng.ID)), "", workflows.AdmissionStateQuery)
	if err != nil {
		return workflows.AdmissionState{}, false
	}
	var state workflows.AdmissionState
	if err := value.Get(&state); err != nil {
		return workflows.AdmissionState{}, false
	}
	return state, true
}

// signalAdmission applies the new capacities. SignalWithStart starts the
// entity workflow when this is the first capacity write for the engine.
func (h *CatalogHandlers) signalAdmission(ctx context.Context, engineID string, caps workflows.AdmissionCapacities) {
	if h.temporalClient == nil {
		return
	}
	state := workflows.AdmissionState{MaxRunningTotal: caps.MaxRunningTotal, Capacities: caps.Classes}
	if _, err := h.temporalClient.SignalWithStartWorkflow(ctx,
		workflows.AdmissionWorkflowID(engineID),
		workflows.AdmissionCapacitySignal, caps,
		client.StartWorkflowOptions{ID: workflows.AdmissionWorkflowID(engineID), TaskQueue: executions.TaskQueue},
		workflows.AdmissionWorkflow, engineID, state,
	); err != nil {
		h.logger.WarnContext(ctx, "could not update the admission workflow", "engine_id", engineID, "err", err)
	}
}

// queueCapacityUpdate validates the request and returns the rows to write and
// the capacities to signal.
func queueCapacityUpdate(id pgtype.UUID, body *PutEngineQueuesJSONRequestBody) ([]db.UpsertEngineQueueCapacityParams, workflows.AdmissionCapacities, error) {
	if len(body.Classes) != len(workloadClasses) {
		return nil, workflows.AdmissionCapacities{}, fmt.Errorf("every workload class must be set exactly once")
	}
	if body.MaxRunningTotal != nil && (*body.MaxRunningTotal < 0 || *body.MaxRunningTotal > math.MaxInt32) {
		return nil, workflows.AdmissionCapacities{}, fmt.Errorf("max_running_total is out of range")
	}
	seen := make(map[queryengine.WorkloadClass]bool, len(body.Classes))
	rows := make([]db.UpsertEngineQueueCapacityParams, 0, len(body.Classes))
	caps := make([]workflows.AdmissionCapacity, 0, len(body.Classes))
	for _, c := range body.Classes {
		class := queryengine.WorkloadClass(c.Class)
		if !class.Valid() {
			return nil, workflows.AdmissionCapacities{}, fmt.Errorf("unknown workload class %q", c.Class)
		}
		if seen[class] {
			return nil, workflows.AdmissionCapacities{}, fmt.Errorf("workload class %q appears twice", class)
		}
		seen[class] = true
		if c.MaxRunning < 0 || c.MaxRunning > math.MaxInt32 || c.MaxQueued < 0 || c.MaxQueued > math.MaxInt32 || c.QueueTimeoutMs < 1 {
			return nil, workflows.AdmissionCapacities{}, fmt.Errorf("class %q has an invalid capacity", class)
		}
		rows = append(rows, db.UpsertEngineQueueCapacityParams{
			EngineID:       id,
			Class:          string(class),
			MaxRunning:     int32(c.MaxRunning), //nolint:gosec // bounded above
			MaxQueued:      int32(c.MaxQueued),  //nolint:gosec // bounded above
			QueueTimeoutMs: int64(c.QueueTimeoutMs),
		})
		caps = append(caps, workflows.AdmissionCapacity{
			Class:        class,
			MaxRunning:   c.MaxRunning,
			MaxQueued:    c.MaxQueued,
			QueueTimeout: time.Duration(c.QueueTimeoutMs) * time.Millisecond,
		})
	}
	maxRunningTotal := 0
	if body.MaxRunningTotal != nil {
		maxRunningTotal = *body.MaxRunningTotal
	}
	return rows, workflows.AdmissionCapacities{MaxRunningTotal: maxRunningTotal, Classes: caps}, nil
}

func liveCounts(state workflows.AdmissionState, class queryengine.WorkloadClass) (running, queued int, oldestWaitMs *int) {
	var oldest time.Time
	for _, lease := range state.Running {
		if lease.Class == class {
			running++
		}
	}
	for _, q := range state.Queued {
		if q.Class != class {
			continue
		}
		queued++
		if !q.EnqueuedAt.IsZero() && (oldest.IsZero() || q.EnqueuedAt.Before(oldest)) {
			oldest = q.EnqueuedAt
		}
	}
	if queued > 0 && !oldest.IsZero() {
		ms := int(time.Since(oldest) / time.Millisecond)
		if ms < 0 {
			ms = 0
		}
		oldestWaitMs = &ms
	}
	return running, queued, oldestWaitMs
}

func int32ToIntPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func intToInt32(v *int) *int32 {
	if v == nil || *v < 0 || *v > math.MaxInt32 {
		return nil
	}
	n := int32(*v) //nolint:gosec // bounded above
	return &n
}
