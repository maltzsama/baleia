package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	qtrino "github.com/galileostudio/baleia/internal/queryengine/trino"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/galileostudio/baleia/internal/workbench/executions"
	"github.com/galileostudio/baleia/internal/workload"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.temporal.io/sdk/client"
)

// maxRuntimeTimeoutMs is the largest millisecond value time.Duration can hold
// without overflowing when multiplied by time.Millisecond.
const maxRuntimeTimeoutMs = int64(math.MaxInt64) / int64(time.Millisecond)

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
// admission workflow so the change applies without a restart. While the
// engine hands admission to Trino (#600), the same transaction publishes the
// resource groups generated from those capacities (#635), so Baleia never
// stops queuing a run that Trino has no group for.
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
	environment := cmp.Or(h.settings.Execution.TrinoResourceGroupsEnvironment, defaultResourceGroupsEnvironment)
	var (
		handover bool
		revision int
		refusal  *UnprocessableEntityJSONResponse
	)
	err = h.engineQueueTx(ctx, func(q engineQueueTxStore) error {
		// The switch and the one-owner rule are read under the lock, so two
		// concurrent saves cannot both take the groups over, nor can a save
		// that omits the switch miss an enable that commits meanwhile.
		if err := q.LockTrinoResourceGroups(ctx); err != nil {
			return err
		}
		eng, err := q.GetEngineByID(ctx, id)
		if err != nil {
			return err
		}
		handover = eng.TrinoResourceGroupsEnabled
		if request.Body.TrinoResourceGroupsEnabled != nil {
			handover = *request.Body.TrinoResourceGroupsEnabled
		}
		if handover {
			if refusal, err = resourceGroupsRefusal(ctx, q, eng); err != nil {
				return err
			} else if refusal != nil {
				return errHandoverRefused
			}
		}
		if err := q.DeleteEngineQueueCapacities(ctx, id); err != nil {
			return err
		}
		for _, row := range rows {
			if err := q.UpsertEngineQueueCapacity(ctx, row); err != nil {
				return err
			}
		}
		_, err = q.SetEngineMaxRunningTotal(ctx, db.SetEngineMaxRunningTotalParams{
			ID:              id,
			MaxRunningTotal: intToInt32(request.Body.MaxRunningTotal),
		})
		if err != nil {
			return err
		}
		if request.Body.TrinoResourceGroupsEnabled != nil {
			_, err = q.SetEngineTrinoResourceGroups(ctx, db.SetEngineTrinoResourceGroupsParams{
				ID:                         id,
				TrinoResourceGroupsEnabled: *request.Body.TrinoResourceGroupsEnabled,
			})
			if err != nil {
				return err
			}
		}
		if !handover {
			return nil
		}
		revision, err = q.PublishTrinoResourceGroups(ctx, id, environment, resourceGroupsDocument(caps))
		if err != nil {
			return fmt.Errorf("publish trino resource groups: %w", err)
		}
		return nil
	})
	if refusal != nil {
		return PutEngineQueues422JSONResponse{UnprocessableEntityJSONResponse: *refusal}, nil
	}
	if err != nil {
		return nil, err
	}
	changes := map[string]audit.Change{
		"max_running_total":             {To: request.Body.MaxRunningTotal},
		"trino_resource_groups_enabled": {To: request.Body.TrinoResourceGroupsEnabled},
		"classes":                       {To: request.Body.Classes},
	}
	if handover {
		changes["trino_resource_groups_revision"] = audit.Change{To: revision}
	}

	h.audited(ctx, audit.Event{
		Action:       audit.WorkloadUpdate,
		ResourceType: "engine",
		ResourceID:   idString(id),
		Outcome:      audit.Allowed,
		Changes:      changes,
	})
	h.signalAdmission(ctx, idString(id), caps)

	eng, err := h.topology.Engine(ctx, id)
	if err != nil {
		return nil, err
	}
	out, err := h.engineQueues(ctx, eng)
	if err != nil {
		return nil, err
	}
	return PutEngineQueues200JSONResponse(out), nil
}

// defaultResourceGroupsEnvironment is Trino's default node.environment, used
// when no environment is configured.
const defaultResourceGroupsEnvironment = "production"

// errHandoverRefused rolls back a save whose handover resourceGroupsRefusal
// refused; the handler answers with the refusal.
var errHandoverRefused = errors.New("trino resource group handover refused")

// resourceGroupsRefusal says why eng cannot hand admission over to Trino's
// resource groups, or nil when it can. Only a Trino engine can, and only one
// engine at a time: the groups are stored per environment, so a second engine
// would overwrite the first one's. q is the save's transaction, holding
// LockTrinoResourceGroups.
func resourceGroupsRefusal(ctx context.Context, q engineQueueTxStore, eng db.Engine) (*UnprocessableEntityJSONResponse, error) {
	if eng.Kind != qtrino.Kind {
		refusal := unprocessable(ErrorCodeEngineUnsupported, "only a Trino engine can hand its queue over to Trino resource groups")
		return &refusal, nil
	}
	engines, err := q.ListEngines(ctx)
	if err != nil {
		return nil, err
	}
	for _, other := range engines {
		if other.ID != eng.ID && other.TrinoResourceGroupsEnabled {
			refusal := unprocessable(ErrorCodeConflict, fmt.Sprintf("engine %q already owns the Trino resource groups; turn its switch off first", other.Name))
			return &refusal, nil
		}
	}
	return nil, nil
}

// resourceGroupsDocument renders the Trino resource groups from the
// capacities and the ceiling being written, the same ones Baleia's admission
// queue enforces.
func resourceGroupsDocument(caps workflows.AdmissionCapacities) workload.ResourceGroups {
	classes := make(map[queryengine.WorkloadClass]workload.Capacity, len(caps.Classes))
	for _, c := range caps.Classes {
		classes[c.Class] = workload.Capacity{MaxRunning: c.MaxRunning, MaxQueued: c.MaxQueued}
	}
	ceiling := caps.MaxRunningTotal
	return workload.Generate(classes, &ceiling)
}

// engineQueues merges the configured capacity (or its default) with the live
// state the admission workflow reports.
func (h *CatalogHandlers) engineQueues(ctx context.Context, eng db.Engine) (EngineQueues, error) {
	rows, err := h.capacityStore().ListEngineQueueCapacities(ctx, eng.ID)
	if err != nil {
		return EngineQueues{}, err
	}
	configured := make(map[queryengine.WorkloadClass]*workload.Capacity, len(rows))
	for _, row := range rows {
		capacity := &workload.Capacity{
			MaxRunning:   int(row.MaxRunning),
			MaxQueued:    int(row.MaxQueued),
			QueueTimeout: time.Duration(row.QueueTimeoutMs) * time.Millisecond,
		}
		if row.RuntimeTimeoutMs != nil {
			capacity.RuntimeTimeout = time.Duration(*row.RuntimeTimeoutMs) * time.Millisecond
		}
		configured[queryengine.WorkloadClass(row.Class)] = capacity
	}
	state, _ := h.admissionState(ctx, eng)

	out := EngineQueues{
		EngineId:                   openapi_types.UUID(eng.ID.Bytes),
		MaxRunningTotal:            int32ToIntPtr(eng.MaxRunningTotal),
		TrinoResourceGroupsEnabled: ptr(eng.TrinoResourceGroupsEnabled),
		Classes:                    make([]EngineQueue, 0, len(workloadClasses)),
	}
	for _, class := range workloadClasses {
		capacity := workload.Resolve(eng.Class, class, configured[class])
		running, queued, oldest := liveCounts(state, class)
		out.Classes = append(out.Classes, EngineQueue{
			Class:            EngineQueueClass(class),
			MaxRunning:       capacity.MaxRunning,
			MaxQueued:        capacity.MaxQueued,
			QueueTimeoutMs:   int(capacity.QueueTimeout / time.Millisecond),
			RuntimeTimeoutMs: int(capacity.RuntimeTimeout / time.Millisecond),
			Running:          running,
			Queued:           queued,
			OldestWaitMs:     oldest,
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
		if c.RuntimeTimeoutMs != nil && (*c.RuntimeTimeoutMs < 1 || int64(*c.RuntimeTimeoutMs) > maxRuntimeTimeoutMs) {
			return nil, workflows.AdmissionCapacities{}, fmt.Errorf("class %q has an invalid runtime timeout", class)
		}
		rows = append(rows, db.UpsertEngineQueueCapacityParams{
			EngineID:         id,
			Class:            string(class),
			MaxRunning:       int32(c.MaxRunning), //nolint:gosec // bounded above
			MaxQueued:        int32(c.MaxQueued),  //nolint:gosec // bounded above
			QueueTimeoutMs:   int64(c.QueueTimeoutMs),
			RuntimeTimeoutMs: int64Ptr(c.RuntimeTimeoutMs),
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

func int64Ptr(v *int) *int64 {
	if v == nil {
		return nil
	}
	n := int64(*v)
	return &n
}
