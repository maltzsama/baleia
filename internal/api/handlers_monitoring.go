package api

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.temporal.io/sdk/client"
)

var maintenanceRetentionRe = regexp.MustCompile(`^[1-9][0-9]{0,4}[dhm]$`)

var maintenanceOperations = []string{"optimize", "expire_snapshots", "remove_orphan_files"}

// ListMonitoringEngines returns the engines with runtime monitoring the caller
// may view (#117).
func (h *Handlers) ListMonitoringEngines(ctx context.Context, _ ListMonitoringEnginesRequestObject) (ListMonitoringEnginesResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListMonitoringEngines401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	engines, err := h.queries.ListEngines(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MonitoringEngine, 0, len(engines))
	for _, e := range engines {
		if !e.Enabled {
			continue
		}
		if _, _, err := h.authz().Engine(ctx, p, e.ID, authz.ActRead); err != nil {
			continue
		}
		if h.engines == nil {
			continue
		}
		eng, err := h.engines.Get(ctx, e.ID)
		if err != nil || !eng.Capabilities().Monitoring {
			continue
		}
		out = append(out, MonitoringEngine{
			Id: openapi_types.UUID(e.ID.Bytes), Name: e.Name, Kind: e.Kind,
			Class: e.Class,
		})
	}
	return ListMonitoringEngines200JSONResponse(out), nil
}

func (h *Handlers) engineForMonitoring(ctx context.Context, p authz.Principal, id pgtype.UUID) (db.Engine, bool) {
	if h.engines == nil {
		return db.Engine{}, false
	}
	engine, _, err := h.authz().Engine(ctx, p, id, authz.ActRead)
	if err != nil {
		return db.Engine{}, false
	}
	return engine, true
}

// GetEngineCluster returns the latest snapshot and a downsampled window.
func (h *Handlers) GetEngineCluster(ctx context.Context, request GetEngineClusterRequestObject) (GetEngineClusterResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetEngineCluster401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	engineID := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, ok := h.engineForMonitoring(ctx, p, engineID); !ok {
		return GetEngineCluster404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "engine not found"}}, nil
	}

	window := "1h"
	if request.Params.Window != nil {
		window = string(*request.Params.Window)
	}
	duration := time.Hour
	switch window {
	case "6h":
		duration = 6 * time.Hour
	case "24h":
		duration = 24 * time.Hour
	}

	series := ClusterSeries{Series: []ClusterPoint{}}
	if latest, err := h.queries.GetLatestClusterSnapshotByEngine(ctx, engineID); err == nil {
		point := clusterPoint(latest)
		series.Latest = &point
	}
	rows, err := h.queries.ListClusterSnapshotsByEngine(ctx, db.ListClusterSnapshotsByEngineParams{
		EngineID: engineID,
		FromTime: pgtype.Timestamptz{Time: time.Now().Add(-duration), Valid: true},
		Lim:      300,
	})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		series.Series = append(series.Series, clusterPoint(row))
	}
	return GetEngineCluster200JSONResponse(series), nil
}

func clusterPoint(s db.ClusterSnapshot) ClusterPoint {
	out := ClusterPoint{
		CapturedAt:     pgTimestampToTime(s.CapturedAt),
		ActiveWorkers:  int(s.ActiveWorkers),
		RunningQueries: int(s.RunningQueries),
		QueuedQueries:  int(s.QueuedQueries),
		BlockedQueries: int(s.BlockedQueries),
	}
	if s.TotalMemoryBytes != nil {
		v := int(*s.TotalMemoryBytes)
		out.TotalMemoryBytes = &v
	}
	if s.FreeMemoryBytes != nil {
		v := int(*s.FreeMemoryBytes)
		out.FreeMemoryBytes = &v
	}
	return out
}

// ListMonitoringQueries returns query snapshots of one engine. Members see
// their own Trino identity; only admins may ask for everyone.
func (h *Handlers) ListMonitoringQueries(ctx context.Context, request ListMonitoringQueriesRequestObject) (ListMonitoringQueriesResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListMonitoringQueries401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	engineID := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, ok := h.engineForMonitoring(ctx, p, engineID); !ok {
		return ListMonitoringQueries404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "engine not found"}}, nil
	}

	scope := "mine"
	if request.Params.Scope != nil {
		scope = string(*request.Params.Scope)
	}
	var userName *string
	if scope == "all" {
		if !p.IsAdmin() {
			return ListMonitoringQueries403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only administrators may see every query"}}, nil
		}
	} else {
		identity, err := h.trinoIdentityFor(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		userName = &identity
	}

	limit, offset := 20, 0
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	if request.Params.Offset != nil {
		offset = *request.Params.Offset
	}
	params := db.ListTrinoQuerySnapshotsByEngineParams{
		EngineID: engineID, UserName: userName, Q: request.Params.Q,
		Lim: int32(limit), Off: int32(offset),
	}
	if request.Params.State != nil {
		state := string(*request.Params.State)
		params.State = &state
	}
	rows, err := h.queries.ListTrinoQuerySnapshotsByEngine(ctx, params)
	if err != nil {
		return nil, err
	}
	page := MonitoringQueryPage{Items: make([]MonitoringQuery, 0, len(rows))}
	for _, row := range rows {
		page.Items = append(page.Items, monitoringQuery(row))
	}
	if len(rows) == limit {
		next := offset + limit
		page.NextOffset = &next
	}
	return ListMonitoringQueries200JSONResponse(page), nil
}

func monitoringQuery(s db.TrinoQuerySnapshot) MonitoringQuery {
	first := firstSQLine(s.Sql)
	out := MonitoringQuery{
		TrinoQueryId: s.TrinoQueryID,
		State:        s.State,
		User:         s.User,
		SqlFirstLine: &first,
		Catalog:      strOrEmpty(s.Catalog),
		Schema:       s.Schema,
		CreatedAt:    pgTimestampToTime(s.CreatedAt),
		Origin:       &s.Origin,
	}
	if s.ExecutionID.Valid {
		id := openapi_types.UUID(s.ExecutionID.Bytes)
		out.ExecutionId = &id
	}
	if s.ElapsedMs != nil {
		v := int(*s.ElapsedMs)
		out.ElapsedMs = &v
	}
	if s.CpuMs != nil {
		v := int(*s.CpuMs)
		out.CpuMs = &v
	}
	if s.PhysicalInputBytes != nil {
		v := int(*s.PhysicalInputBytes)
		out.PhysicalInputBytes = &v
	}
	if s.OutputRows != nil {
		v := int(*s.OutputRows)
		out.OutputRows = &v
	}
	return out
}

func firstSQLine(sql string) string {
	for _, line := range strings.Split(sql, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// GetMonitoringQuery returns the full statement of one query, scoped like the
// list.
func (h *Handlers) GetMonitoringQuery(ctx context.Context, request GetMonitoringQueryRequestObject) (GetMonitoringQueryResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetMonitoringQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	engineID := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, ok := h.engineForMonitoring(ctx, p, engineID); !ok {
		return GetMonitoringQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "query not found"}}, nil
	}
	snapshot, err := h.queries.GetTrinoQuerySnapshotByEngine(ctx, db.GetTrinoQuerySnapshotByEngineParams{
		EngineID: engineID, TrinoQueryID: request.QueryId,
	})
	if err != nil {
		return GetMonitoringQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "query not found"}}, nil
	}
	if !h.maySeeQuery(ctx, p, snapshot) {
		return GetMonitoringQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "query not found"}}, nil
	}
	first := firstSQLine(snapshot.Sql)
	detail := MonitoringQueryDetail{
		TrinoQueryId: snapshot.TrinoQueryID,
		State:        snapshot.State,
		User:         snapshot.User,
		SqlFirstLine: &first,
		Catalog:      strOrEmpty(snapshot.Catalog),
		Schema:       snapshot.Schema,
		CreatedAt:    pgTimestampToTime(snapshot.CreatedAt),
		Origin:       &snapshot.Origin,
		Sql:          &snapshot.Sql,
		ErrorMessage: snapshot.ErrorMessage,
	}
	if snapshot.ExecutionID.Valid {
		id := openapi_types.UUID(snapshot.ExecutionID.Bytes)
		detail.ExecutionId = &id
	}
	return GetMonitoringQuery200JSONResponse(detail), nil
}

func (h *Handlers) maySeeQuery(ctx context.Context, p authz.Principal, snapshot db.TrinoQuerySnapshot) bool {
	if p.IsAdmin() {
		return true
	}
	identity, err := h.trinoIdentityFor(ctx, p.UserID)
	return err == nil && snapshot.User == identity
}

// KillMonitoringQuery cancels a running query through the engine's kill.
func (h *Handlers) KillMonitoringQuery(ctx context.Context, request KillMonitoringQueryRequestObject) (KillMonitoringQueryResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return KillMonitoringQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	engineID := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, ok := h.engineForMonitoring(ctx, p, engineID); !ok {
		return KillMonitoringQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "query not found"}}, nil
	}
	snapshot, err := h.queries.GetTrinoQuerySnapshotByEngine(ctx, db.GetTrinoQuerySnapshotByEngineParams{
		EngineID: engineID, TrinoQueryID: request.QueryId,
	})
	if err != nil || !h.maySeeQuery(ctx, p, snapshot) {
		return KillMonitoringQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "query not found"}}, nil
	}
	if snapshot.State != "running" {
		return KillMonitoringQuery409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeNotRunning, Message: "the query is no longer running"}}, nil
	}
	eng, err := h.engines.Get(ctx, engineID)
	if err != nil {
		return nil, err
	}
	if err := eng.Cancel(ctx, queryengine.QueryHandle{QueryID: request.QueryId}); err != nil {
		return KillMonitoringQuery409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeCancelFailed, Message: "the engine did not cancel the query"}}, nil
	}
	return KillMonitoringQuery202Response{}, nil
}

func (h *Handlers) catalogForMonitoring(ctx context.Context, p authz.Principal, id pgtype.UUID, act authz.Action) (db.Catalog, bool) {
	catalog, err := h.queries.GetCatalogByID(ctx, id)
	if err != nil {
		return db.Catalog{}, false
	}
	if _, _, err := h.authz().CatalogOfConnection(ctx, p, catalog.ConnectionID, id, act); err != nil {
		return db.Catalog{}, false
	}
	return catalog, true
}

// ListTableHealth returns the Iceberg health rows of one logical catalog.
func (h *Handlers) ListTableHealth(ctx context.Context, request ListTableHealthRequestObject) (ListTableHealthResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListTableHealth401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	catalogID := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	catalog, ok := h.catalogForMonitoring(ctx, p, catalogID, authz.ActRead)
	if !ok {
		return ListTableHealth404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
	}
	limit, offset := 50, 0
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	if request.Params.Offset != nil {
		offset = *request.Params.Offset
	}
	params := db.ListTableHealthByCatalogParams{
		CatalogID: catalogID, Ns: request.Params.Ns, Lim: int32(limit), Off: int32(offset),
	}
	if request.Params.Needs != nil {
		needs := string(*request.Params.Needs)
		params.Needs = &needs
	}
	if request.Params.MaxScore != nil {
		score := int32(*request.Params.MaxScore)
		params.MaxScore = &score
	}
	rows, err := h.queries.ListTableHealthByCatalog(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]TableHealth, 0, len(rows))
	for _, row := range rows {
		out = append(out, tableHealth(catalog, row))
	}
	return ListTableHealth200JSONResponse(out), nil
}

func tableHealth(catalog db.Catalog, s db.TableHealthSnapshot) TableHealth {
	out := TableHealth{
		CatalogId:           openapi_types.UUID(catalog.ID.Bytes),
		CatalogName:         catalog.Name,
		Namespace:           s.Namespace,
		TableName:           s.TableName,
		SnapshotCount:       intPtrValue(int(s.SnapshotCount)),
		HealthScore:         int(s.HealthScore),
		NeedsCompaction:     s.NeedsCompaction,
		NeedsSnapshotExpiry: s.NeedsSnapshotExpiry,
		NeedsOrphanCleanup:  s.NeedsOrphanCleanup,
	}
	if s.FileCount != nil {
		v := int(*s.FileCount)
		out.FileCount = &v
	}
	if s.TotalSizeBytes != nil {
		v := int(*s.TotalSizeBytes)
		out.TotalSizeBytes = &v
	}
	if s.OldestSnapshotAgeHours != nil {
		v := int(*s.OldestSnapshotAgeHours)
		out.OldestSnapshotAgeHours = &v
	}
	if s.LastMaintenanceAt.Valid {
		v := s.LastMaintenanceAt.Time
		out.LastMaintenanceAt = &v
	}
	return out
}

// CreateMaintenanceRun starts an Iceberg maintenance run and records it.
func (h *Handlers) CreateMaintenanceRun(ctx context.Context, request CreateMaintenanceRunRequestObject) (CreateMaintenanceRunResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return CreateMaintenanceRun401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	catalogID := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	catalog, ok := h.catalogForMonitoring(ctx, p, catalogID, authz.ActWrite)
	if !ok {
		return CreateMaintenanceRun404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
	}
	if request.Body == nil {
		return CreateMaintenanceRun422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "missing body")}, nil
	}
	ops := request.Body.Operations
	if len(ops) == 0 {
		return CreateMaintenanceRun422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "operations are required")}, nil
	}
	selected := map[string]bool{}
	for _, op := range ops {
		if !containsString(maintenanceOperations, op) {
			return CreateMaintenanceRun422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "unknown operation "+op)}, nil
		}
		selected[op] = true
	}
	retention := ""
	if request.Body.Retention != nil {
		retention = strings.TrimSpace(*request.Body.Retention)
	}
	needsRetention := selected["expire_snapshots"] || selected["remove_orphan_files"]
	if needsRetention && !maintenanceRetentionRe.MatchString(retention) {
		return CreateMaintenanceRun422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "retention must match ^[1-9][0-9]{0,4}[dhm]$ for expire_snapshots and remove_orphan_files")}, nil
	}

	var bindingID pgtype.UUID
	if request.Body.BindingId != nil {
		bindingID = pgtype.UUID{Bytes: [16]byte(*request.Body.BindingId), Valid: true}
	} else {
		bindings, err := h.queries.ListBindingsByCatalog(ctx, catalogID)
		if err != nil {
			return nil, err
		}
		for _, binding := range bindings {
			eng, err := h.engines.Get(ctx, binding.EngineID)
			if err == nil && eng.Capabilities().IcebergMaintenance {
				bindingID = binding.ID
				break
			}
		}
	}
	if !bindingID.Valid {
		return CreateMaintenanceRun422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeNotSupported, "no enabled binding of this catalog supports Iceberg maintenance")}, nil
	}

	runID := uuid.New()
	workflowID := "maintenance-" + runID.String()
	var retentionPtr *string
	if retention != "" {
		retentionPtr = &retention
	}
	run, err := h.queries.CreateMaintenanceRun(ctx, db.CreateMaintenanceRunParams{
		CatalogID:  catalogID,
		BindingID:  bindingID,
		Namespace:  request.Body.Namespace,
		TableName:  request.Body.Table,
		Operations: ops,
		Retention:  retentionPtr,
		StartedBy:  p.UserID,
		WorkflowID: workflowID,
	})
	if err != nil {
		return nil, err
	}
	trinoName := catalog.TrinoName()
	if h.temporalClient != nil {
		_, err = h.temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID: workflowID, TaskQueue: "baleia-task-queue",
		}, workflows.MaintenanceRunWorkflow, runID.String(), uuid.UUID(bindingID.Bytes).String(), trinoName, request.Body.Namespace, request.Body.Table, retention, ops)
		if err != nil {
			// Best effort: the start error below is what the caller sees.
			_, _ = h.queries.UpdateMaintenanceRun(ctx, db.UpdateMaintenanceRunParams{
				ID: run.ID, Status: "failed", ErrorCode: monitoringStringPtr("workflow_start_failed"), ErrorMessage: monitoringStringPtr(err.Error()),
			})
			return nil, err
		}
	}
	h.audited(ctx, audit.Event{Action: audit.MaintenanceTrigger, ResourceType: "catalog",
		ResourceID: uuid.UUID(catalogID.Bytes).String(), WorkspaceID: ""}.Allowed())
	return CreateMaintenanceRun202JSONResponse(maintenanceRun(run)), nil
}

// ListMaintenanceRuns lists the runs of one catalog, newest first.
func (h *Handlers) ListMaintenanceRuns(ctx context.Context, request ListMaintenanceRunsRequestObject) (ListMaintenanceRunsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListMaintenanceRuns401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	catalogID := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	if _, ok := h.catalogForMonitoring(ctx, p, catalogID, authz.ActRead); !ok {
		return ListMaintenanceRuns404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
	}
	limit, offset := 20, 0
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	if request.Params.Offset != nil {
		offset = *request.Params.Offset
	}
	rows, err := h.queries.ListMaintenanceRuns(ctx, db.ListMaintenanceRunsParams{
		CatalogID: catalogID, Lim: int32(limit), Off: int32(offset),
	})
	if err != nil {
		return nil, err
	}
	out := make([]MaintenanceRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, maintenanceRun(row))
	}
	return ListMaintenanceRuns200JSONResponse(out), nil
}

func maintenanceRun(r db.MaintenanceRun) MaintenanceRun {
	out := MaintenanceRun{
		Id:           openapi_types.UUID(r.ID.Bytes),
		CatalogId:    openapi_types.UUID(r.CatalogID.Bytes),
		Namespace:    r.Namespace,
		TableName:    r.TableName,
		Operations:   r.Operations,
		Retention:    r.Retention,
		Status:       r.Status,
		ErrorCode:    r.ErrorCode,
		ErrorMessage: r.ErrorMessage,
		StartedAt:    pgTimestampToTime(r.StartedAt),
		WorkflowId:   r.WorkflowID,
	}
	if r.BindingID.Valid {
		id := openapi_types.UUID(r.BindingID.Bytes)
		out.BindingId = &id
	}
	if r.StartedBy.Valid {
		id := openapi_types.UUID(r.StartedBy.Bytes)
		out.StartedBy = &id
	}
	if r.FinishedAt.Valid {
		v := r.FinishedAt.Time
		out.FinishedAt = &v
	}
	return out
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func monitoringStringPtr(value string) *string { return &value }

func intPtrValue(value int) *int { return &value }
