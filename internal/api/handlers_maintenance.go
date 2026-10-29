package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/monitoring"
	"github.com/galileostudio/baleia/internal/monitoring/maintenance"
	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.temporal.io/sdk/client"
)

// Per-table Iceberg maintenance (#500): parameters, plans, manual runs and
// "analyze now". Reading needs view on the catalog; everything else needs
// write, as the existing manual maintenance does.

// maintenanceTarget is a table the caller may act on, with its catalog.
type maintenanceTarget struct {
	key     monitoring.TableKey
	catalog db.Catalog
	actor   pgtype.UUID
}

// maintenanceTable resolves the request's table; ok is false when the
// catalog does not exist or the caller may not act on it (a 404, as the
// manual maintenance answers).
func (h *OpsHandlers) maintenanceTable(ctx context.Context, catalogID openapi_types.UUID, namespace, table string, act authz.Action) (maintenanceTarget, bool, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return maintenanceTarget{}, false, nil
	}
	id := pgtype.UUID{Bytes: catalogID, Valid: true}
	catalog, ok, err := h.catalogForMonitoring(ctx, p, id, act)
	if err != nil || !ok {
		return maintenanceTarget{}, ok, err
	}
	return maintenanceTarget{
		key:     monitoring.TableKey{CatalogID: id, Namespace: namespace, Table: table},
		catalog: catalog,
		actor:   p.UserID,
	}, true, nil
}

func (h *OpsHandlers) capableEngine(ctx context.Context, engineID pgtype.UUID) bool {
	eng, err := h.engines.Get(ctx, engineID)
	return err == nil && eng.Capabilities().IcebergMaintenance
}

// maintenanceBinding is the binding runs use and its retention floor.
func (h *OpsHandlers) maintenanceBinding(ctx context.Context, catalogID pgtype.UUID) (db.CatalogBinding, time.Duration, bool, error) {
	bindings, err := h.queries.ListBindingsByCatalog(ctx, catalogID)
	if err != nil {
		return db.CatalogBinding{}, 0, false, err
	}
	b, ok := monitoring.MaintenanceBinding(ctx, bindings, h.capableEngine)
	if !ok {
		return db.CatalogBinding{}, maintenance.UnmanagedRetentionFloor, false, nil
	}
	floor, err := h.monitor.RetentionFloor(ctx, b)
	if err != nil {
		return db.CatalogBinding{}, 0, false, err
	}
	return b, floor, true, nil
}

func notFound(msg string) NotFoundJSONResponse {
	return NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: msg}
}

const catalogNotFound = "catalog not found"

// GetTableMaintenance returns a table's parameters, what its catalog allows,
// its recent runs and its current plan.
func (h *OpsHandlers) GetTableMaintenance(ctx context.Context, request GetTableMaintenanceRequestObject) (GetTableMaintenanceResponseObject, error) {
	t, ok, err := h.maintenanceTable(ctx, request.CatalogId, request.Namespace, request.Table, authz.ActRead)
	if err != nil {
		return nil, err
	}
	if !ok {
		return GetTableMaintenance404JSONResponse{NotFoundJSONResponse: notFound(catalogNotFound)}, nil
	}
	_, floor, _, err := h.maintenanceBinding(ctx, t.key.CatalogID)
	if err != nil {
		return nil, err
	}
	out := TableMaintenance{
		CatalogId:             request.CatalogId,
		Namespace:             request.Namespace,
		TableName:             request.Table,
		RetentionFloorSeconds: int64(floor / time.Second),
		Runs:                  []MaintenanceRun{},
	}
	actions := h.tableMaintenanceActions(ctx, t.catalog.ConnectionID, t.key.CatalogID)
	out.AllowedActions = &actions
	health, err := h.queries.GetTableHealthByTable(ctx, db.GetTableHealthByTableParams{
		CatalogID: t.key.CatalogID, Namespace: t.key.Namespace, TableName: t.key.Table,
	})
	switch {
	case err == nil:
		if health.FormatVersion != nil {
			v := int(*health.FormatVersion)
			out.FormatVersion = &v
		}
		var props map[string]string
		if json.Unmarshal(health.TableProperties, &props) == nil && len(props) > 0 {
			out.Properties = &props
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	runs, err := h.monitor.TableRuns(ctx, t.key)
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		out.Runs = append(out.Runs, maintenanceRun(r))
	}
	row, err := h.monitor.TablePolicy(ctx, t.key)
	if errors.Is(err, monitoring.ErrPolicyNotFound) {
		return GetTableMaintenance200JSONResponse(out), nil
	}
	if err != nil {
		return nil, err
	}
	stored, err := storedPolicy(row)
	if err != nil {
		return nil, err
	}
	out.Policy = &stored
	if row.Mode != maintenance.ModeMaintainedElsewhere {
		now := time.Now()
		plan, err := h.monitor.PlanTable(ctx, t.key, row, maintenance.Automatic, now)
		if err != nil {
			return nil, err
		}
		api, err := apiPlan(plan, row.Revision, now)
		if err != nil {
			return nil, err
		}
		out.Plan = &api
	}
	return GetTableMaintenance200JSONResponse(out), nil
}

// PutTableMaintenance creates or replaces a table's parameters.
func (h *OpsHandlers) PutTableMaintenance(ctx context.Context, request PutTableMaintenanceRequestObject) (PutTableMaintenanceResponseObject, error) {
	t, ok, err := h.maintenanceTable(ctx, request.CatalogId, request.Namespace, request.Table, authz.ActWrite)
	if err != nil {
		return nil, err
	}
	if !ok {
		return PutTableMaintenance404JSONResponse{NotFoundJSONResponse: notFound(catalogNotFound)}, nil
	}
	if request.Body == nil {
		return PutTableMaintenance422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "missing body")}, nil
	}
	var revision *int64
	if m := request.Params.IfMatch; m != nil && strings.TrimSpace(*m) != "" {
		v, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(*m), `"`), 10, 64)
		if err != nil {
			return PutTableMaintenance422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields(
				"If-Match must be a revision", FieldError{Field: "If-Match", Code: FieldCodeInvalidFormat})}, nil
		}
		revision = &v
	}
	policy, err := maintenancePolicyFromAPI(*request.Body)
	if err != nil {
		return PutTableMaintenance422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
	}
	binding, floor, _, err := h.maintenanceBinding(ctx, t.key.CatalogID)
	if err != nil {
		return nil, err
	}
	table := maintenance.Table{RetentionFloor: floor}
	if health, err := h.queries.GetTableHealthByTable(ctx, db.GetTableHealthByTableParams{
		CatalogID: t.key.CatalogID, Namespace: t.key.Namespace, TableName: t.key.Table,
	}); err == nil && health.FormatVersion != nil {
		v := int(*health.FormatVersion)
		table.FormatVersion = &v
	}
	if errs := maintenance.Validate(policy, table); len(errs) > 0 {
		// Below Trino's default on a binding Baleia does not manage: no
		// retention there can go lower, whatever the value.
		if binding.Mode != "managed" && retentionBelowFloor(errs) {
			return PutTableMaintenance409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{
				Code:    ErrorCodeCatalogNotManaged,
				Message: "this catalog's binding is not managed by Baleia: retentions shorter than 7 days are refused by the engine",
			}}, nil
		}
		return PutTableMaintenance422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields(
			"invalid maintenance parameters", fieldErrors(errs)...)}, nil
	}
	stored, before, err := h.monitor.SetTablePolicy(ctx, t.key, policy, revision, t.actor)
	if errors.Is(err, monitoring.ErrPolicyRevision) {
		code, msg := ErrorCodeVersionConflict, "the parameters changed since they were read; reload and apply the change again"
		if revision == nil {
			code, msg = ErrorCodeConflict, "the table already has parameters; send If-Match with their revision to replace them"
		}
		return PutTableMaintenance409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: code, Message: msg}}, nil
	}
	if errors.Is(err, monitoring.ErrPolicyNotFound) {
		return PutTableMaintenance404JSONResponse{NotFoundJSONResponse: notFound("the table has no maintenance parameters to replace")}, nil
	}
	if err != nil {
		return nil, err
	}
	changes := map[string]audit.Change{"mode": {From: before.Mode, To: stored.Mode}, "revision": {From: before.Revision, To: stored.Revision}}
	h.audited(ctx, audit.Event{Action: audit.MaintenanceParametersSet, ResourceType: "table_maintenance",
		ResourceID: tableResource(t.key), Changes: changes}.Allowed())
	out, err := storedPolicy(stored)
	if err != nil {
		return nil, err
	}
	return PutTableMaintenance200JSONResponse(out), nil
}

// DeleteTableMaintenance removes a table's parameters.
func (h *OpsHandlers) DeleteTableMaintenance(ctx context.Context, request DeleteTableMaintenanceRequestObject) (DeleteTableMaintenanceResponseObject, error) {
	t, ok, err := h.maintenanceTable(ctx, request.CatalogId, request.Namespace, request.Table, authz.ActWrite)
	if err != nil {
		return nil, err
	}
	if !ok {
		return DeleteTableMaintenance404JSONResponse{NotFoundJSONResponse: notFound(catalogNotFound)}, nil
	}
	before, err := h.monitor.DeleteTablePolicy(ctx, t.key)
	if errors.Is(err, monitoring.ErrPolicyNotFound) {
		return DeleteTableMaintenance404JSONResponse{NotFoundJSONResponse: notFound("the table has no maintenance parameters")}, nil
	}
	if err != nil {
		return nil, err
	}
	h.audited(ctx, audit.Event{Action: audit.MaintenanceParametersRemoved, ResourceType: "table_maintenance",
		ResourceID: tableResource(t.key), Changes: map[string]audit.Change{"mode": {From: before.Mode, To: nil}}}.Allowed())
	return DeleteTableMaintenance204Response{}, nil
}

// DryRunTableMaintenance plans the table now without running anything.
func (h *OpsHandlers) DryRunTableMaintenance(ctx context.Context, request DryRunTableMaintenanceRequestObject) (DryRunTableMaintenanceResponseObject, error) {
	t, ok, err := h.maintenanceTable(ctx, request.CatalogId, request.Namespace, request.Table, authz.ActWrite)
	if err != nil {
		return nil, err
	}
	if !ok {
		return DryRunTableMaintenance404JSONResponse{NotFoundJSONResponse: notFound(catalogNotFound)}, nil
	}
	row, err := h.monitor.TablePolicy(ctx, t.key)
	if errors.Is(err, monitoring.ErrPolicyNotFound) {
		return DryRunTableMaintenance404JSONResponse{NotFoundJSONResponse: notFound("the table has no maintenance parameters")}, nil
	}
	if err != nil {
		return nil, err
	}
	now := time.Now()
	plan, err := h.monitor.PlanTable(ctx, t.key, row, maintenance.DryRun, now)
	if errors.Is(err, maintenance.ErrMaintainedElsewhere) {
		return DryRunTableMaintenance409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{
			Code: ErrorCodeMaintenanceExternal, Message: "the table is maintained elsewhere: Baleia only analyzes it"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := h.monitor.RecordDryRun(ctx, t.key, row, plan, t.actor); err != nil {
		return nil, err
	}
	out, err := apiPlan(plan, row.Revision, now)
	if err != nil {
		return nil, err
	}
	return DryRunTableMaintenance200JSONResponse(out), nil
}

// RunTableMaintenance runs one operation on the table now.
func (h *OpsHandlers) RunTableMaintenance(ctx context.Context, request RunTableMaintenanceRequestObject) (RunTableMaintenanceResponseObject, error) {
	t, ok, err := h.maintenanceTable(ctx, request.CatalogId, request.Namespace, request.Table, authz.ActWrite)
	if err != nil {
		return nil, err
	}
	if !ok {
		return RunTableMaintenance404JSONResponse{NotFoundJSONResponse: notFound(catalogNotFound)}, nil
	}
	if request.Body == nil {
		return RunTableMaintenance422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "missing body")}, nil
	}
	row, err := h.monitor.TablePolicy(ctx, t.key)
	if errors.Is(err, monitoring.ErrPolicyNotFound) {
		return RunTableMaintenance404JSONResponse{NotFoundJSONResponse: notFound("the table has no maintenance parameters")}, nil
	}
	if err != nil {
		return nil, err
	}
	policy, err := monitoring.DecodePolicy(row)
	if err != nil {
		return nil, err
	}
	if policy.Mode == maintenance.ModeMaintainedElsewhere {
		return RunTableMaintenance409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{
			Code: ErrorCodeMaintenanceExternal, Message: "the table is maintained elsewhere: Baleia only analyzes it"}}, nil
	}
	op := string(request.Body.Operation)
	step := maintenance.Step{Operation: op, Reason: maintenance.Reason{Fired: "manual"}}
	if op == "next_due" {
		plan, err := h.monitor.PlanTable(ctx, t.key, row, maintenance.Manual, time.Now())
		if err != nil {
			return nil, err
		}
		next, ok := plan.Next()
		if !ok {
			return RunTableMaintenance409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{
				Code: ErrorCodeConflict, Message: "no operation is due on this table now"}}, nil
		}
		step = next
	}
	settings := policy.Operation(step.Operation)
	if !settings.Enabled {
		return RunTableMaintenance422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields(
			"the operation is not enabled in the table's parameters", FieldError{Field: "operation", Code: FieldCodeNotAllowed})}, nil
	}
	binding, _, ok, err := h.maintenanceBinding(ctx, t.key.CatalogID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return RunTableMaintenance422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeNotSupported, monitoring.ErrNoCapableBinding.Error())}, nil
	}
	run, err := activities.RecordMaintenanceRun(ctx, h.queries, t.key, binding, row, step, settings, string(maintenance.Manual), t.actor)
	if isUniqueViolation(err) {
		return RunTableMaintenance409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{
			Code: ErrorCodeConflict, Message: "a maintenance run is already in progress on this table"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if h.temporalClient != nil {
		if _, err := h.temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: run.WorkflowID, TaskQueue: "baleia-task-queue"},
			workflows.MaintenanceOperationWorkflow, uuid.UUID(run.ID.Bytes).String(), settings.TimeoutSeconds); err != nil {
			h.monitor.Fail(ctx, run.ID, err)
			return nil, err
		}
	}
	h.audited(ctx, audit.Event{Action: audit.MaintenanceRunRequested, ResourceType: "table_maintenance",
		ResourceID: tableResource(t.key), Changes: map[string]audit.Change{"operation": {To: step.Operation}}}.Allowed())
	return RunTableMaintenance202JSONResponse(maintenanceRun(run)), nil
}

// AnalyzeCatalogTableHealth starts a health pass of one catalog now.
func (h *OpsHandlers) AnalyzeCatalogTableHealth(ctx context.Context, request AnalyzeCatalogTableHealthRequestObject) (AnalyzeCatalogTableHealthResponseObject, error) {
	t, ok, err := h.maintenanceTable(ctx, request.CatalogId, "", "", authz.ActWrite)
	if err != nil {
		return nil, err
	}
	if !ok {
		return AnalyzeCatalogTableHealth404JSONResponse{NotFoundJSONResponse: notFound(catalogNotFound)}, nil
	}
	ok, err = h.startAnalysis(ctx, t, "", "")
	if err != nil {
		return nil, err
	}
	if !ok {
		return AnalyzeCatalogTableHealth422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeNotSupported, notIcebergCatalog)}, nil
	}
	return AnalyzeCatalogTableHealth202Response{}, nil
}

// AnalyzeTableHealth starts a health pass of one table now.
func (h *OpsHandlers) AnalyzeTableHealth(ctx context.Context, request AnalyzeTableHealthRequestObject) (AnalyzeTableHealthResponseObject, error) {
	t, ok, err := h.maintenanceTable(ctx, request.CatalogId, request.Namespace, request.Table, authz.ActWrite)
	if err != nil {
		return nil, err
	}
	if !ok {
		return AnalyzeTableHealth404JSONResponse{NotFoundJSONResponse: notFound(catalogNotFound)}, nil
	}
	ok, err = h.startAnalysis(ctx, t, request.Namespace, request.Table)
	if err != nil {
		return nil, err
	}
	if !ok {
		return AnalyzeTableHealth422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeNotSupported, notIcebergCatalog)}, nil
	}
	return AnalyzeTableHealth202Response{}, nil
}

const notIcebergCatalog = "table health covers enabled Iceberg catalogs only"

// startAnalysis starts the pass and the round after it; false when the
// catalog is not an enabled Iceberg catalog.
func (h *OpsHandlers) startAnalysis(ctx context.Context, t maintenanceTarget, namespace, table string) (bool, error) {
	catalogs, err := h.queries.ListEnabledIcebergCatalogs(ctx)
	if err != nil {
		return false, err
	}
	for _, c := range catalogs {
		if c.ID != t.key.CatalogID {
			continue
		}
		if h.temporalClient == nil {
			return true, nil
		}
		params := activities.LensAnalyzeTableHealthParams{
			CatalogID: c.ID.String(), CatalogConnectionID: c.ConnectionID.String(), Catalog: c.Name,
			Namespace: namespace, Table: table,
		}
		_, err := h.temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID: "table-health-analyze-" + uuid.NewString(), TaskQueue: "baleia-task-queue",
		}, workflows.TableHealthAnalyzeWorkflow, params)
		return err == nil, err
	}
	return false, nil
}

// GetCatalogMaintenancePlan plans every table of the catalog with parameters.
func (h *OpsHandlers) GetCatalogMaintenancePlan(ctx context.Context, request GetCatalogMaintenancePlanRequestObject) (GetCatalogMaintenancePlanResponseObject, error) {
	t, ok, err := h.maintenanceTable(ctx, request.CatalogId, "", "", authz.ActRead)
	if err != nil {
		return nil, err
	}
	if !ok {
		return GetCatalogMaintenancePlan404JSONResponse{NotFoundJSONResponse: notFound(catalogNotFound)}, nil
	}
	rows, err := h.queries.ListTableMaintenancePolicies(ctx, t.key.CatalogID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	plans, err := h.monitor.PlanCatalog(ctx, t.key.CatalogID, rows, maintenance.Automatic, now)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogMaintenancePlanItem, 0, len(plans))
	for _, p := range plans {
		item := CatalogMaintenancePlanItem{Namespace: p.Row.Namespace, TableName: p.Row.TableName, Mode: MaintenanceMode(p.Row.Mode),
			Plan: MaintenancePlan{EvaluatedAt: now, Plan: []MaintenancePlanStep{}}}
		if p.Row.Mode != maintenance.ModeMaintainedElsewhere {
			if item.Plan, err = apiPlan(p.Plan, p.Row.Revision, now); err != nil {
				return nil, err
			}
		}
		if p.LastRun != nil {
			last := maintenanceRun(*p.LastRun)
			item.LastRun = &last
		}
		out = append(out, item)
	}
	return GetCatalogMaintenancePlan200JSONResponse(out), nil
}

func tableResource(k monitoring.TableKey) string {
	return fmt.Sprintf("%s/%s/%s", uuid.UUID(k.CatalogID.Bytes), k.Namespace, k.Table)
}

// retentionBelowFloor reports a retention refused only for being shorter
// than the floor.
func retentionBelowFloor(errs []maintenance.FieldError) bool {
	for _, e := range errs {
		if e.Code == maintenance.CodeOutOfRange &&
			(strings.HasSuffix(e.Field, ".params.retention_seconds") || strings.HasSuffix(e.Field, ".params.older_than_seconds")) {
			return true
		}
	}
	return false
}

func fieldErrors(errs []maintenance.FieldError) []FieldError {
	out := make([]FieldError, 0, len(errs))
	for _, e := range errs {
		fe := FieldError{Field: e.Field, Code: FieldCode(e.Code)}
		if len(e.Params) > 0 {
			params := e.Params
			fe.Params = &params
		}
		out = append(out, fe)
	}
	return out
}

// maintenancePolicyFromAPI and the conversions below go through JSON: the API schemas
// and the maintenance types share their shape, so neither side can drift.
func maintenancePolicyFromAPI(in MaintenancePolicy) (maintenance.Policy, error) {
	var p maintenance.Policy
	raw, err := json.Marshal(in)
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(raw, &p)
	return p, err
}

func storedPolicy(row db.TableMaintenancePolicy) (StoredMaintenancePolicy, error) {
	p, err := monitoring.DecodePolicy(row)
	if err != nil {
		return StoredMaintenancePolicy{}, err
	}
	var out StoredMaintenancePolicy
	raw, err := json.Marshal(p)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	out.Revision = row.Revision
	out.CreatedAt = row.CreatedAt.Time
	out.UpdatedAt = row.UpdatedAt.Time
	if row.UpdatedBy.Valid {
		id := openapi_types.UUID(row.UpdatedBy.Bytes)
		out.UpdatedBy = &id
	}
	return out, nil
}

func apiPlan(p maintenance.Plan, revision int64, at time.Time) (MaintenancePlan, error) {
	out := MaintenancePlan{EvaluatedAt: at, PolicyRevision: &revision, Plan: []MaintenancePlanStep{}}
	raw, err := json.Marshal(p.Steps)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out.Plan)
	return out, err
}
