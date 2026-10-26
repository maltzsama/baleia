package api

import (
	"context"
	"errors"
	"strings"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/monitoring"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.temporal.io/sdk/client"
)

// ListMonitoringEngines returns the engines with runtime monitoring the caller
// may view (#117).
func (h *OpsHandlers) ListMonitoringEngines(ctx context.Context, _ ListMonitoringEnginesRequestObject) (ListMonitoringEnginesResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListMonitoringEngines401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	engines, err := h.monitor.Engines(ctx)
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

func (h *core) engineForMonitoring(ctx context.Context, p authz.Principal, id pgtype.UUID) (db.Engine, bool) {
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
func (h *OpsHandlers) GetEngineCluster(ctx context.Context, request GetEngineClusterRequestObject) (GetEngineClusterResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetEngineCluster401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	engineID := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, ok := h.engineForMonitoring(ctx, p, engineID); !ok {
		return GetEngineCluster404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "engine not found"}}, nil
	}

	window := ""
	if request.Params.Window != nil {
		window = string(*request.Params.Window)
	}
	cluster, err := h.monitor.Cluster(ctx, engineID, window)
	if err != nil {
		return nil, err
	}
	series := ClusterSeries{Series: make([]ClusterPoint, 0, len(cluster.Series))}
	if cluster.Latest != nil {
		point := clusterPoint(*cluster.Latest)
		series.Latest = &point
	}
	for _, row := range cluster.Series {
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
func (h *OpsHandlers) ListMonitoringQueries(ctx context.Context, request ListMonitoringQueriesRequestObject) (ListMonitoringQueriesResponseObject, error) {
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

	filter := monitoring.QueryFilter{UserName: userName, Q: request.Params.Q, Limit: request.Params.Limit, Offset: request.Params.Offset}
	if request.Params.State != nil {
		state := string(*request.Params.State)
		filter.State = &state
	}
	rows, err := h.monitor.Queries(ctx, engineID, filter)
	if err != nil {
		return nil, err
	}
	limit, offset := monitoring.DefaultQueries, 0
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	if request.Params.Offset != nil {
		offset = *request.Params.Offset
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
func (h *OpsHandlers) GetMonitoringQuery(ctx context.Context, request GetMonitoringQueryRequestObject) (GetMonitoringQueryResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetMonitoringQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	engineID := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, ok := h.engineForMonitoring(ctx, p, engineID); !ok {
		return GetMonitoringQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "query not found"}}, nil
	}
	snapshot, err := h.monitor.Query(ctx, engineID, request.QueryId)
	if errors.Is(err, monitoring.ErrQueryNotFound) {
		return GetMonitoringQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: err.Error()}}, nil
	}
	if err != nil {
		return nil, err
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

func (h *core) maySeeQuery(ctx context.Context, p authz.Principal, snapshot db.TrinoQuerySnapshot) bool {
	if p.IsAdmin() {
		return true
	}
	identity, err := h.trinoIdentityFor(ctx, p.UserID)
	return err == nil && snapshot.User == identity
}

// KillMonitoringQuery cancels a running query through the engine's kill.
func (h *OpsHandlers) KillMonitoringQuery(ctx context.Context, request KillMonitoringQueryRequestObject) (KillMonitoringQueryResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return KillMonitoringQuery401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	engineID := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, ok := h.engineForMonitoring(ctx, p, engineID); !ok {
		return KillMonitoringQuery404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "query not found"}}, nil
	}
	snapshot, err := h.monitor.Query(ctx, engineID, request.QueryId)
	if err != nil && !errors.Is(err, monitoring.ErrQueryNotFound) {
		return nil, err
	}
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

func (h *OpsHandlers) catalogForMonitoring(ctx context.Context, p authz.Principal, id pgtype.UUID, act authz.Action) (db.Catalog, bool, error) {
	catalog, err := h.monitor.Catalog(ctx, id)
	if errors.Is(err, monitoring.ErrCatalogNotFound) {
		return db.Catalog{}, false, nil
	}
	if err != nil {
		return db.Catalog{}, false, err
	}
	if _, _, err := h.authz().CatalogOfConnection(ctx, p, catalog.ConnectionID, id, act); err != nil {
		if errors.Is(err, authz.ErrNotFound) || errors.Is(err, authz.ErrForbidden) {
			return db.Catalog{}, false, nil
		}
		return db.Catalog{}, false, err
	}
	return catalog, true, nil
}

// ListTableHealth returns the Iceberg health rows of one logical catalog.
func (h *OpsHandlers) ListTableHealth(ctx context.Context, request ListTableHealthRequestObject) (ListTableHealthResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListTableHealth401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	catalogID := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	catalog, ok, err := h.catalogForMonitoring(ctx, p, catalogID, authz.ActRead)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ListTableHealth404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
	}
	filter := monitoring.HealthFilter{Namespace: request.Params.Ns, MaxScore: request.Params.MaxScore, Limit: request.Params.Limit, Offset: request.Params.Offset}
	if request.Params.Needs != nil {
		needs := string(*request.Params.Needs)
		filter.Needs = &needs
	}
	rows, err := h.monitor.TableHealth(ctx, catalogID, filter)
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
func (h *OpsHandlers) CreateMaintenanceRun(ctx context.Context, request CreateMaintenanceRunRequestObject) (CreateMaintenanceRunResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return CreateMaintenanceRun401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	catalogID := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	_, ok, err := h.catalogForMonitoring(ctx, p, catalogID, authz.ActWrite)
	if err != nil {
		return nil, err
	}
	if !ok {
		return CreateMaintenanceRun404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
	}
	if request.Body == nil {
		return CreateMaintenanceRun422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "missing body")}, nil
	}
	m := monitoring.Maintenance{
		CatalogID: catalogID, Namespace: request.Body.Namespace, Table: request.Body.Table,
		Operations: request.Body.Operations, Retention: request.Body.Retention, StartedBy: p.UserID,
	}
	if request.Body.BindingId != nil {
		b := pgtype.UUID{Bytes: [16]byte(*request.Body.BindingId), Valid: true}
		m.Binding = &b
	}
	plan, err := h.monitor.Prepare(ctx, m, func(ctx context.Context, engineID pgtype.UUID) bool {
		eng, err := h.engines.Get(ctx, engineID)
		return err == nil && eng.Capabilities().IcebergMaintenance
	})
	var invalid *monitoring.InvalidError
	switch {
	case errors.As(err, &invalid):
		return CreateMaintenanceRun422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, invalid.Msg)}, nil
	case errors.Is(err, monitoring.ErrNoCapableBinding):
		return CreateMaintenanceRun422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeNotSupported, err.Error())}, nil
	case err != nil:
		return nil, err
	}

	runID := uuid.New()
	workflowID := "maintenance-" + runID.String()
	run, err := h.monitor.Record(ctx, plan, workflowID)
	if err != nil {
		return nil, err
	}
	if h.temporalClient != nil {
		_, err = h.temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID: workflowID, TaskQueue: "baleia-task-queue",
		}, workflows.MaintenanceRunWorkflow, runID.String(), uuid.UUID(plan.Binding.EngineID.Bytes).String(),
			plan.Binding.EngineCatalogName, plan.Namespace, plan.Table, plan.Retention, plan.Operations)
		if err != nil {
			h.monitor.Fail(ctx, run.ID, err)
			return nil, err
		}
	}
	h.audited(ctx, audit.Event{Action: audit.MaintenanceTrigger, ResourceType: "catalog",
		ResourceID: uuid.UUID(catalogID.Bytes).String(), WorkspaceID: ""}.Allowed())
	return CreateMaintenanceRun202JSONResponse(maintenanceRun(run)), nil
}

// ListMaintenanceRuns lists the runs of one catalog, newest first.
func (h *OpsHandlers) ListMaintenanceRuns(ctx context.Context, request ListMaintenanceRunsRequestObject) (ListMaintenanceRunsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListMaintenanceRuns401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	catalogID := pgtype.UUID{Bytes: request.CatalogId, Valid: true}
	if _, ok, err := h.catalogForMonitoring(ctx, p, catalogID, authz.ActRead); err != nil {
		return nil, err
	} else if !ok {
		return ListMaintenanceRuns404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}}, nil
	}
	rows, err := h.monitor.Runs(ctx, catalogID, request.Params.Limit, request.Params.Offset)
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

func intPtrValue(value int) *int { return &value }
