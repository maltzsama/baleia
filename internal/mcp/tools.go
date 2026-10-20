package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/agentpolicy"
	"github.com/galileostudio/baleia/internal/api"
	"github.com/jackc/pgx/v5/pgtype"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

var (
	errNotFound = errors.New("not found")
	errInvalid  = errors.New("invalid argument")
)

func (s *Server) registerTools() {
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{Name: agentpolicy.ToolListWorkspaces, Description: "List the workspaces this agent is bound to."}, s.listWorkspaces)
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{Name: agentpolicy.ToolListCatalogs, Description: "List the catalogs granted to a bound workspace, with the engines that host them."}, s.listCatalogs)
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{Name: agentpolicy.ToolListSchemas, Description: "List the namespaces (schemas) of a catalog."}, s.listSchemas)
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{Name: agentpolicy.ToolListTables, Description: "List the tables of a schema."}, s.listTables)
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{Name: agentpolicy.ToolDescribeTable, Description: "Describe a table: columns, types, documentation and freshness."}, s.describeTable)
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{Name: agentpolicy.ToolSearchMetadata, Description: "Search tables, columns and metrics visible in a workspace."}, s.searchMetadata)
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{Name: agentpolicy.ToolListMetrics, Description: "List the semantic metrics of a workspace."}, s.listMetrics)
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{Name: agentpolicy.ToolRenderMetric, Description: "Render a metric's SQL for a binding; run it with run_query."}, s.renderMetric)
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{Name: agentpolicy.ToolListModels, Description: "List the semantic models visible in a workspace."}, s.listModels)
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{Name: agentpolicy.ToolDescribeModel, Description: "Describe a semantic model: source, dimensions, metrics and joins."}, s.describeModel)
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{Name: agentpolicy.ToolQuerySemantic, Description: "Compile an explore query over a semantic model to SQL; run it with run_query."}, s.querySemantic)
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{Name: agentpolicy.ToolRunQuery, Description: "Run one read-only SQL statement and wait for the result."}, s.runQuery)
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{Name: agentpolicy.ToolGetExecution, Description: "Get the state of an execution this agent created."}, s.getExecution)
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{Name: agentpolicy.ToolGetResults, Description: "Read stored rows of a finished execution, with caps."}, s.getResults)
	mcpsdk.AddTool(s.sdk, &mcpsdk.Tool{Name: agentpolicy.ToolCancelExecution, Description: "Cancel an execution this agent created."}, s.cancelExecution)
}

type emptyIn struct{}
type workspaceIn struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"workspace UUID"`
}
type catalogIn struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"workspace UUID"`
	CatalogID   string `json:"catalog_id" jsonschema:"catalog UUID"`
}
type schemasIn struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"workspace UUID"`
	CatalogID   string `json:"catalog_id" jsonschema:"catalog UUID"`
	Parent      string `json:"parent,omitempty" jsonschema:"optional dotted parent namespace"`
}
type tablesIn struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"workspace UUID"`
	CatalogID   string `json:"catalog_id" jsonschema:"catalog UUID"`
	Schema      string `json:"schema,omitempty" jsonschema:"dotted namespace; empty means no namespace"`
	Cursor      string `json:"cursor,omitempty" jsonschema:"next_cursor of the previous page"`
	Limit       int    `json:"limit,omitempty" jsonschema:"page size"`
}
type tableIn struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"workspace UUID"`
	CatalogID   string `json:"catalog_id" jsonschema:"catalog UUID"`
	Schema      string `json:"schema,omitempty" jsonschema:"dotted namespace"`
	Table       string `json:"table" jsonschema:"table name"`
}
type searchIn struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"workspace UUID"`
	Query       string `json:"query" jsonschema:"search text"`
	Kind        string `json:"kind,omitempty" jsonschema:"table, column or metric"`
	Limit       int    `json:"limit,omitempty" jsonschema:"maximum results (50)"`
}
type renderIn struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"workspace UUID"`
	MetricID    string `json:"metric_id,omitempty" jsonschema:"metric UUID from list_metrics"`
	Slug        string `json:"slug,omitempty" jsonschema:"metric slug, alternative to metric_id"`
	BindingID   string `json:"binding_id,omitempty" jsonschema:"binding UUID (engine + catalog)"`
	Grain       string `json:"grain,omitempty" jsonschema:"time grain"`
	From        string `json:"from,omitempty" jsonschema:"ISO-8601 start"`
	To          string `json:"to,omitempty" jsonschema:"ISO-8601 end"`
}
type modelIn struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"workspace UUID"`
	ModelID     string `json:"model_id,omitempty" jsonschema:"model UUID from list_models"`
	Slug        string `json:"slug,omitempty" jsonschema:"model slug, alternative to model_id"`
}
type semanticIn struct {
	WorkspaceID string             `json:"workspace_id" jsonschema:"workspace UUID"`
	Model       string             `json:"model" jsonschema:"base model slug"`
	Dimensions  []string           `json:"dimensions,omitempty" jsonschema:"dimension fields"`
	Metrics     []string           `json:"metrics,omitempty" jsonschema:"metric fields"`
	Filters     []semanticFilterIn `json:"filters,omitempty" jsonschema:"filters on dimensions (WHERE) or metrics (HAVING)"`
	Sorts       []semanticSortIn   `json:"sorts,omitempty" jsonschema:"sorts on selected fields"`
	Limit       int                `json:"limit,omitempty" jsonschema:"row cap"`
	TimeGrain   string             `json:"time_grain,omitempty" jsonschema:"time grain for a selected time dimension"`
	BindingID   string             `json:"binding_id,omitempty" jsonschema:"binding UUID (defaults to the model's)"`
}
type semanticFilterIn struct {
	Field  string `json:"field"`
	Op     string `json:"op" jsonschema:"one of = != > >= < <= in not_in is_null is_not_null"`
	Values []any  `json:"values,omitempty"`
}
type semanticSortIn struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}
type runIn struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"workspace UUID"`
	SQL         string `json:"sql" jsonschema:"one read-only SQL statement"`
	BindingID   string `json:"binding_id,omitempty" jsonschema:"binding UUID (preferred target)"`
	CatalogID   string `json:"catalog_id,omitempty" jsonschema:"catalog UUID when no binding is given"`
	EngineID    string `json:"engine_id,omitempty" jsonschema:"engine UUID when the catalog has several bindings"`
	Schema      string `json:"schema,omitempty" jsonschema:"default schema"`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"how long to wait for the result (max 60)"`
}
type executionIn struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"workspace UUID"`
	ExecutionID string `json:"execution_id" jsonschema:"execution UUID"`
}
type resultsIn struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"workspace UUID"`
	ExecutionID string `json:"execution_id" jsonschema:"execution UUID"`
	Offset      int    `json:"offset,omitempty" jsonschema:"row offset"`
	Limit       int    `json:"limit,omitempty" jsonschema:"maximum rows"`
}

func id(s string) (openapi_types.UUID, error) {
	var u openapi_types.UUID
	if err := u.UnmarshalText([]byte(s)); err != nil {
		return u, fmt.Errorf("%w: %q is not a UUID", errInvalid, s)
	}
	return u, nil
}

func pathOf(u openapi_types.UUID) api.PathId { return api.PathId(u) }

func pgUUID(u openapi_types.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pgPtr(u openapi_types.UUID) *pgtype.UUID {
	id := pgUUID(u)
	return &id
}

func dotted(s string) *[]string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ".")
	return &parts
}

func (s *Server) listWorkspaces(ctx context.Context, _ *mcpsdk.CallToolRequest, _ emptyIn) (*mcpsdk.CallToolResult, any, error) {
	return s.dispatch(ctx, agentpolicy.Call{Tool: agentpolicy.ToolListWorkspaces}, func(ctx context.Context, _ agentpolicy.Effective) (any, error) {
		return s.handlers.AgentWorkspaces(ctx)
	})
}

func (s *Server) listCatalogs(ctx context.Context, _ *mcpsdk.CallToolRequest, in workspaceIn) (*mcpsdk.CallToolResult, any, error) {
	ws, err := id(in.WorkspaceID)
	if err != nil {
		return toolError("invalid_argument", "workspace_id must be a UUID")
	}
	return s.dispatch(ctx, agentpolicy.Call{Tool: agentpolicy.ToolListCatalogs, WorkspaceID: pgUUID(ws)}, func(ctx context.Context, _ agentpolicy.Effective) (any, error) {
		got, err := s.handlers.ListMetadataCatalogs(ctx, api.ListMetadataCatalogsRequestObject{Id: pathOf(ws)})
		if err != nil {
			return nil, err
		}
		switch r := got.(type) {
		case api.ListMetadataCatalogs200JSONResponse:
			return r, nil
		case api.ListMetadataCatalogs401JSONResponse, api.ListMetadataCatalogs404JSONResponse:
			return nil, errNotFound
		default:
			return nil, fmt.Errorf("unmapped response %T", got)
		}
	})
}

func (s *Server) listSchemas(ctx context.Context, _ *mcpsdk.CallToolRequest, in schemasIn) (*mcpsdk.CallToolResult, any, error) {
	ws, err := id(in.WorkspaceID)
	if err != nil {
		return toolError("invalid_argument", "workspace_id must be a UUID")
	}
	cat, err := id(in.CatalogID)
	if err != nil {
		return toolError("invalid_argument", "catalog_id must be a UUID")
	}
	return s.dispatch(ctx, agentpolicy.Call{Tool: agentpolicy.ToolListSchemas, WorkspaceID: pgUUID(ws), CatalogID: pgPtr(cat)}, func(ctx context.Context, _ agentpolicy.Effective) (any, error) {
		got, err := s.handlers.ListMetadataNamespaces(ctx, api.ListMetadataNamespacesRequestObject{
			Id: pathOf(ws), CatalogId: cat, Params: api.ListMetadataNamespacesParams{Parent: dotted(in.Parent)},
		})
		if err != nil {
			return nil, err
		}
		switch r := got.(type) {
		case api.ListMetadataNamespaces200JSONResponse:
			return r, nil
		case api.ListMetadataNamespaces401JSONResponse, api.ListMetadataNamespaces404JSONResponse:
			return nil, errNotFound
		default:
			return nil, fmt.Errorf("unmapped response %T", got)
		}
	})
}

func (s *Server) listTables(ctx context.Context, _ *mcpsdk.CallToolRequest, in tablesIn) (*mcpsdk.CallToolResult, any, error) {
	ws, err := id(in.WorkspaceID)
	if err != nil {
		return toolError("invalid_argument", "workspace_id must be a UUID")
	}
	cat, err := id(in.CatalogID)
	if err != nil {
		return toolError("invalid_argument", "catalog_id must be a UUID")
	}
	params := api.ListMetadataTablesParams{Ns: dotted(in.Schema)}
	if in.Cursor != "" {
		params.Cursor = &in.Cursor
	}
	if in.Limit > 0 {
		params.Limit = &in.Limit
	}
	return s.dispatch(ctx, agentpolicy.Call{Tool: agentpolicy.ToolListTables, WorkspaceID: pgUUID(ws), CatalogID: pgPtr(cat)}, func(ctx context.Context, _ agentpolicy.Effective) (any, error) {
		got, err := s.handlers.ListMetadataTables(ctx, api.ListMetadataTablesRequestObject{Id: pathOf(ws), CatalogId: cat, Params: params})
		if err != nil {
			return nil, err
		}
		switch r := got.(type) {
		case api.ListMetadataTables200JSONResponse:
			return r, nil
		case api.ListMetadataTables401JSONResponse, api.ListMetadataTables404JSONResponse:
			return nil, errNotFound
		default:
			return nil, fmt.Errorf("unmapped response %T", got)
		}
	})
}

func (s *Server) describeTable(ctx context.Context, _ *mcpsdk.CallToolRequest, in tableIn) (*mcpsdk.CallToolResult, any, error) {
	ws, err := id(in.WorkspaceID)
	if err != nil {
		return toolError("invalid_argument", "workspace_id must be a UUID")
	}
	cat, err := id(in.CatalogID)
	if err != nil {
		return toolError("invalid_argument", "catalog_id must be a UUID")
	}
	return s.dispatch(ctx, agentpolicy.Call{Tool: agentpolicy.ToolDescribeTable, WorkspaceID: pgUUID(ws), CatalogID: pgPtr(cat)}, func(ctx context.Context, _ agentpolicy.Effective) (any, error) {
		page, err := s.handlers.ListMetadataTables(ctx, api.ListMetadataTablesRequestObject{
			Id: pathOf(ws), CatalogId: cat, Params: api.ListMetadataTablesParams{Ns: dotted(in.Schema)},
		})
		if err != nil {
			return nil, err
		}
		list, ok := page.(api.ListMetadataTables200JSONResponse)
		if !ok {
			return nil, errNotFound
		}
		var tableID openapi_types.UUID
		found := false
		for _, t := range list.Items {
			if t.Name == in.Table {
				tableID, found = t.Id, true
				break
			}
		}
		if !found {
			return nil, errNotFound
		}
		got, err := s.handlers.GetMetadataTable(ctx, api.GetMetadataTableRequestObject{Id: pathOf(ws), TableId: tableID})
		if err != nil {
			return nil, err
		}
		switch r := got.(type) {
		case api.GetMetadataTable200JSONResponse:
			return r, nil
		case api.GetMetadataTable401JSONResponse, api.GetMetadataTable404JSONResponse:
			return nil, errNotFound
		default:
			return nil, fmt.Errorf("unmapped response %T", got)
		}
	})
}

func (s *Server) searchMetadata(ctx context.Context, _ *mcpsdk.CallToolRequest, in searchIn) (*mcpsdk.CallToolResult, any, error) {
	ws, err := id(in.WorkspaceID)
	if err != nil {
		return toolError("invalid_argument", "workspace_id must be a UUID")
	}
	params := api.SearchMetadataParams{Q: in.Query}
	if in.Kind != "" {
		k := api.SearchMetadataParamsKind(in.Kind)
		params.Kind = &k
	}
	if in.Limit > 0 {
		params.Limit = &in.Limit
	}
	return s.dispatch(ctx, agentpolicy.Call{Tool: agentpolicy.ToolSearchMetadata, WorkspaceID: pgUUID(ws)}, func(ctx context.Context, _ agentpolicy.Effective) (any, error) {
		got, err := s.handlers.SearchMetadata(ctx, api.SearchMetadataRequestObject{Id: pathOf(ws), Params: params})
		if err != nil {
			return nil, err
		}
		switch r := got.(type) {
		case api.SearchMetadata200JSONResponse:
			return r, nil
		case api.SearchMetadata401JSONResponse, api.SearchMetadata404JSONResponse:
			return nil, errNotFound
		default:
			return nil, fmt.Errorf("unmapped response %T", got)
		}
	})
}

func (s *Server) listMetrics(ctx context.Context, _ *mcpsdk.CallToolRequest, in workspaceIn) (*mcpsdk.CallToolResult, any, error) {
	ws, err := id(in.WorkspaceID)
	if err != nil {
		return toolError("invalid_argument", "workspace_id must be a UUID")
	}
	return s.dispatch(ctx, agentpolicy.Call{Tool: agentpolicy.ToolListMetrics, WorkspaceID: pgUUID(ws)}, func(ctx context.Context, _ agentpolicy.Effective) (any, error) {
		got, err := s.handlers.ListSemanticMetrics(ctx, api.ListSemanticMetricsRequestObject{Id: pathOf(ws), Params: api.ListSemanticMetricsParams{}})
		if err != nil {
			return nil, err
		}
		switch r := got.(type) {
		case api.ListSemanticMetrics200JSONResponse:
			return r, nil
		case api.ListSemanticMetrics401JSONResponse, api.ListSemanticMetrics404JSONResponse:
			return nil, errNotFound
		default:
			return nil, fmt.Errorf("unmapped response %T", got)
		}
	})
}

func (s *Server) renderMetric(ctx context.Context, _ *mcpsdk.CallToolRequest, in renderIn) (*mcpsdk.CallToolResult, any, error) {
	ws, err := id(in.WorkspaceID)
	if err != nil {
		return toolError("invalid_argument", "workspace_id must be a UUID")
	}
	metricID, err := id(in.MetricID)
	if in.MetricID == "" {
		metricID, err = openapi_types.UUID{}, nil
	}
	if err != nil {
		return toolError("invalid_argument", "metric_id must be a UUID")
	}
	return s.dispatch(ctx, agentpolicy.Call{Tool: agentpolicy.ToolRenderMetric, WorkspaceID: pgUUID(ws)}, func(ctx context.Context, _ agentpolicy.Effective) (any, error) {
		if in.MetricID == "" && in.Slug != "" {
			got, err := s.handlers.ListSemanticMetrics(ctx, api.ListSemanticMetricsRequestObject{Id: pathOf(ws), Params: api.ListSemanticMetricsParams{}})
			if err != nil {
				return nil, err
			}
			list, ok := got.(api.ListSemanticMetrics200JSONResponse)
			if !ok {
				return nil, errNotFound
			}
			for _, m := range list {
				if m.Slug == in.Slug {
					metricID = m.Id
					break
				}
			}
			if metricID == (openapi_types.UUID{}) {
				return nil, errNotFound
			}
		}
		params := api.GetSemanticMetricSqlParams{}
		if in.BindingID != "" {
			b, err := id(in.BindingID)
			if err != nil {
				return nil, errInvalid
			}
			params.BindingId = &b
		}
		if in.Grain != "" {
			g := api.GetSemanticMetricSqlParamsGrain(in.Grain)
			params.Grain = &g
		}
		if in.From != "" {
			t, err := time.Parse(time.RFC3339, in.From)
			if err != nil {
				return nil, errInvalid
			}
			params.From = &t
		}
		if in.To != "" {
			t, err := time.Parse(time.RFC3339, in.To)
			if err != nil {
				return nil, errInvalid
			}
			params.To = &t
		}
		got, err := s.handlers.GetSemanticMetricSql(ctx, api.GetSemanticMetricSqlRequestObject{Id: pathOf(ws), MetricId: metricID, Params: params})
		if err != nil {
			return nil, err
		}
		switch r := got.(type) {
		case api.GetSemanticMetricSql200JSONResponse:
			return r, nil
		case api.GetSemanticMetricSql401JSONResponse, api.GetSemanticMetricSql404JSONResponse:
			return nil, errNotFound
		default:
			return nil, fmt.Errorf("unmapped response %T", got)
		}
	})
}

func (s *Server) listModels(ctx context.Context, _ *mcpsdk.CallToolRequest, in workspaceIn) (*mcpsdk.CallToolResult, any, error) {
	ws, err := id(in.WorkspaceID)
	if err != nil {
		return toolError("invalid_argument", "workspace_id must be a UUID")
	}
	return s.dispatch(ctx, agentpolicy.Call{Tool: agentpolicy.ToolListModels, WorkspaceID: pgUUID(ws)}, func(ctx context.Context, _ agentpolicy.Effective) (any, error) {
		got, err := s.handlers.ListSemanticModels(ctx, api.ListSemanticModelsRequestObject{Id: pathOf(ws)})
		if err != nil {
			return nil, err
		}
		switch r := got.(type) {
		case api.ListSemanticModels200JSONResponse:
			return r, nil
		case api.ListSemanticModels401JSONResponse, api.ListSemanticModels404JSONResponse:
			return nil, errNotFound
		default:
			return nil, fmt.Errorf("unmapped response %T", got)
		}
	})
}

func (s *Server) describeModel(ctx context.Context, _ *mcpsdk.CallToolRequest, in modelIn) (*mcpsdk.CallToolResult, any, error) {
	ws, err := id(in.WorkspaceID)
	if err != nil {
		return toolError("invalid_argument", "workspace_id must be a UUID")
	}
	return s.dispatch(ctx, agentpolicy.Call{Tool: agentpolicy.ToolDescribeModel, WorkspaceID: pgUUID(ws)}, func(ctx context.Context, _ agentpolicy.Effective) (any, error) {
		var modelID openapi_types.UUID
		switch {
		case in.ModelID != "":
			modelID, err = id(in.ModelID)
			if err != nil {
				return nil, errInvalid
			}
		case in.Slug != "":
			got, err := s.handlers.ListSemanticModels(ctx, api.ListSemanticModelsRequestObject{Id: pathOf(ws)})
			if err != nil {
				return nil, err
			}
			list, ok := got.(api.ListSemanticModels200JSONResponse)
			if !ok {
				return nil, errNotFound
			}
			for _, m := range list {
				if m.Slug == in.Slug {
					modelID = m.Id
					break
				}
			}
			if modelID == (openapi_types.UUID{}) {
				return nil, errNotFound
			}
		default:
			return nil, errInvalid
		}
		got, err := s.handlers.GetSemanticModel(ctx, api.GetSemanticModelRequestObject{Id: pathOf(ws), ModelId: modelID})
		if err != nil {
			return nil, err
		}
		switch r := got.(type) {
		case api.GetSemanticModel200JSONResponse:
			return r, nil
		case api.GetSemanticModel401JSONResponse, api.GetSemanticModel403JSONResponse, api.GetSemanticModel404JSONResponse:
			return nil, errNotFound
		default:
			return nil, fmt.Errorf("unmapped response %T", got)
		}
	})
}

func (s *Server) querySemantic(ctx context.Context, _ *mcpsdk.CallToolRequest, in semanticIn) (*mcpsdk.CallToolResult, any, error) {
	ws, err := id(in.WorkspaceID)
	if err != nil {
		return toolError("invalid_argument", "workspace_id must be a UUID")
	}
	q, err := semanticQueryFromInput(in)
	if err != nil {
		return toolError("invalid_argument", "invalid explore query")
	}
	return s.dispatch(ctx, agentpolicy.Call{Tool: agentpolicy.ToolQuerySemantic, WorkspaceID: pgUUID(ws)}, func(ctx context.Context, _ agentpolicy.Effective) (any, error) {
		body := &api.CompileExploreQueryJSONRequestBody{Query: q}
		if in.BindingID != "" {
			b, err := id(in.BindingID)
			if err != nil {
				return nil, errInvalid
			}
			body.BindingId = &b
		}
		got, err := s.handlers.CompileExploreQuery(ctx, api.CompileExploreQueryRequestObject{Id: pathOf(ws), Body: body})
		if err != nil {
			return nil, err
		}
		switch r := got.(type) {
		case api.CompileExploreQuery200JSONResponse:
			return r, nil
		case api.CompileExploreQuery401JSONResponse, api.CompileExploreQuery403JSONResponse, api.CompileExploreQuery404JSONResponse:
			return nil, errNotFound
		case api.CompileExploreQuery422JSONResponse:
			return nil, errInvalid
		default:
			return nil, fmt.Errorf("unmapped response %T", got)
		}
	})
}

// semanticQueryFromInput maps the flat MCP arguments onto the API's explore
// query shape; the compiler still validates every field.
func semanticQueryFromInput(in semanticIn) (api.ExploreQuery, error) {
	payload := map[string]any{
		"model": in.Model, "dimensions": in.Dimensions, "metrics": in.Metrics,
		"filters": in.Filters, "sorts": in.Sorts, "limit": in.Limit, "time_grain": in.TimeGrain,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return api.ExploreQuery{}, err
	}
	var q api.ExploreQuery
	if err := json.Unmarshal(raw, &q); err != nil {
		return api.ExploreQuery{}, err
	}
	return q, nil
}

func (s *Server) runQuery(ctx context.Context, _ *mcpsdk.CallToolRequest, in runIn) (*mcpsdk.CallToolResult, any, error) {
	ws, err := id(in.WorkspaceID)
	if err != nil {
		return toolError("invalid_argument", "workspace_id must be a UUID")
	}
	target := &api.TargetRequest{}
	if in.BindingID != "" {
		b, err := id(in.BindingID)
		if err != nil {
			return toolError("invalid_argument", "binding_id must be a UUID")
		}
		target.BindingId = &b
	}
	if in.CatalogID != "" {
		c, err := id(in.CatalogID)
		if err != nil {
			return toolError("invalid_argument", "catalog_id must be a UUID")
		}
		target.CatalogId = &c
	}
	if in.EngineID != "" {
		e, err := id(in.EngineID)
		if err != nil {
			return toolError("invalid_argument", "engine_id must be a UUID")
		}
		target.EngineId = &e
	}
	if in.Schema != "" {
		target.Schema = &in.Schema
	}
	var enginePtr *pgtype.UUID
	if in.EngineID != "" {
		e, _ := id(in.EngineID)
		enginePtr = pgPtr(e)
	}
	var catalogPtr *pgtype.UUID
	if in.CatalogID != "" {
		c, _ := id(in.CatalogID)
		catalogPtr = pgPtr(c)
	}
	wait := in.WaitSeconds
	if wait <= 0 || wait > 60 {
		wait = 30
	}
	return s.dispatch(ctx, agentpolicy.Call{Tool: agentpolicy.ToolRunQuery, WorkspaceID: pgUUID(ws), CatalogID: catalogPtr, EngineID: enginePtr, SQL: &in.SQL}, func(ctx context.Context, eff agentpolicy.Effective) (any, error) {
		body := &api.ExecutionRequest{Sql: in.SQL, Target: target, WorkspaceId: ws}
		got, err := s.handlers.RunQuery(ctx, api.RunQueryRequestObject{Id: pathOf(ws), Body: body})
		if err != nil {
			return nil, err
		}
		switch r := got.(type) {
		case api.RunQuery202JSONResponse:
			exec := api.Execution(r)
			exec = s.waitFor(ctx, ws, exec, time.Duration(wait)*time.Second)
			out := map[string]any{
				"execution_id": exec.Id,
				"status":       exec.Status,
				"sql":          exec.Sql,
			}
			if exec.ErrorCode != nil {
				out["error"] = *exec.ErrorCode
			}
			if exec.Status == api.ExecutionStatusFinished {
				res, err := s.handlers.MCPResults(ctx, ws, exec.Id, 0, eff.MaxRowsReturned, eff.MaxRowsReturned, eff.MaxResultBytes)
				if err == nil {
					out["columns"] = res.Columns
					out["rows"] = res.Rows
					out["row_count"] = res.RowCount
					out["truncated"] = res.Truncated
				}
			}
			return out, nil
		case api.RunQuery401JSONResponse, api.RunQuery404JSONResponse:
			return nil, errNotFound
		case api.RunQuery422JSONResponse:
			return nil, fmt.Errorf("%w: execution target rejected", errInvalid)
		default:
			return nil, fmt.Errorf("unmapped response %T", got)
		}
	})
}

func (s *Server) waitFor(ctx context.Context, ws openapi_types.UUID, exec api.Execution, timeout time.Duration) api.Execution {
	deadline := time.Now().Add(timeout)
	for {
		got, err := s.handlers.GetExecution(ctx, api.GetExecutionRequestObject{Id: pathOf(ws), ExecutionId: exec.Id})
		if err == nil {
			if r, ok := got.(api.GetExecution200JSONResponse); ok {
				exec = api.Execution(r)
				switch exec.Status {
				case api.ExecutionStatusFinished, api.ExecutionStatusFailed, api.ExecutionStatusCancelled:
					return exec
				}
			}
		}
		if time.Now().After(deadline) {
			return exec
		}
		select {
		case <-ctx.Done():
			return exec
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (s *Server) getExecution(ctx context.Context, _ *mcpsdk.CallToolRequest, in executionIn) (*mcpsdk.CallToolResult, any, error) {
	ws, err := id(in.WorkspaceID)
	if err != nil {
		return toolError("invalid_argument", "workspace_id must be a UUID")
	}
	execID, err := id(in.ExecutionID)
	if err != nil {
		return toolError("invalid_argument", "execution_id must be a UUID")
	}
	return s.dispatch(ctx, agentpolicy.Call{Tool: agentpolicy.ToolGetExecution, WorkspaceID: pgUUID(ws), ExecutionID: pgPtr(execID)}, func(ctx context.Context, _ agentpolicy.Effective) (any, error) {
		got, err := s.handlers.GetExecution(ctx, api.GetExecutionRequestObject{Id: pathOf(ws), ExecutionId: execID})
		if err != nil {
			return nil, err
		}
		switch r := got.(type) {
		case api.GetExecution200JSONResponse:
			return r, nil
		case api.GetExecution401JSONResponse, api.GetExecution404JSONResponse:
			return nil, errNotFound
		default:
			return nil, fmt.Errorf("unmapped response %T", got)
		}
	})
}

func (s *Server) getResults(ctx context.Context, _ *mcpsdk.CallToolRequest, in resultsIn) (*mcpsdk.CallToolResult, any, error) {
	ws, err := id(in.WorkspaceID)
	if err != nil {
		return toolError("invalid_argument", "workspace_id must be a UUID")
	}
	execID, err := id(in.ExecutionID)
	if err != nil {
		return toolError("invalid_argument", "execution_id must be a UUID")
	}
	return s.dispatch(ctx, agentpolicy.Call{Tool: agentpolicy.ToolGetResults, WorkspaceID: pgUUID(ws), ExecutionID: pgPtr(execID)}, func(ctx context.Context, eff agentpolicy.Effective) (any, error) {
		res, err := s.handlers.MCPResults(ctx, ws, execID, in.Offset, in.Limit, eff.MaxRowsReturned, eff.MaxResultBytes)
		if err != nil {
			if errors.Is(err, api.ErrWorkspaceNotFound) {
				return nil, errNotFound
			}
			return nil, err
		}
		return res, nil
	})
}

func (s *Server) cancelExecution(ctx context.Context, _ *mcpsdk.CallToolRequest, in executionIn) (*mcpsdk.CallToolResult, any, error) {
	ws, err := id(in.WorkspaceID)
	if err != nil {
		return toolError("invalid_argument", "workspace_id must be a UUID")
	}
	execID, err := id(in.ExecutionID)
	if err != nil {
		return toolError("invalid_argument", "execution_id must be a UUID")
	}
	return s.dispatch(ctx, agentpolicy.Call{Tool: agentpolicy.ToolCancelExecution, WorkspaceID: pgUUID(ws), ExecutionID: pgPtr(execID)}, func(ctx context.Context, _ agentpolicy.Effective) (any, error) {
		got, err := s.handlers.CancelExecution(ctx, api.CancelExecutionRequestObject{Id: pathOf(ws), ExecutionId: execID})
		if err != nil {
			return nil, err
		}
		switch r := got.(type) {
		case api.CancelExecution200JSONResponse:
			return r, nil
		case api.CancelExecution401JSONResponse, api.CancelExecution404JSONResponse:
			return nil, errNotFound
		default:
			return nil, fmt.Errorf("unmapped response %T", got)
		}
	})
}
