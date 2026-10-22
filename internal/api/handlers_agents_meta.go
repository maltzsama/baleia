package api

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/agentpolicy"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// agentToolDoc is the human text the agents UI shows for the v1 MCP registry
// (#120). The names stay in agentpolicy.AllTools — the single source shared
// with the policy validator and the gate. Only cancel_execution changes state.
func agentToolDoc(name string) (label, description string, readOnly bool) {
	switch name {
	case agentpolicy.ToolListWorkspaces:
		return "List workspaces", "The workspaces this agent is bound to.", true
	case agentpolicy.ToolListCatalogs:
		return "List catalogs", "The catalogs the bound workspaces may use.", true
	case agentpolicy.ToolListSchemas:
		return "List schemas", "Schemas inside a catalog.", true
	case agentpolicy.ToolListTables:
		return "List tables", "Tables and views inside a schema.", true
	case agentpolicy.ToolDescribeTable:
		return "Describe table", "Columns, types and comments for one table.", true
	case agentpolicy.ToolSearchMetadata:
		return "Search metadata", "Find tables and columns by name or description.", true
	case agentpolicy.ToolListMetrics:
		return "List metrics", "The semantic metrics available to the workspace.", true
	case agentpolicy.ToolRenderMetric:
		return "Render metric", "Run a semantic metric and return its rows.", true
	case agentpolicy.ToolListModels:
		return "List models", "The semantic models visible to the workspace.", true
	case agentpolicy.ToolDescribeModel:
		return "Describe model", "Dimensions, metrics and joins of one semantic model.", true
	case agentpolicy.ToolQuerySemantic:
		return "Query semantic layer", "Compile an explore query over a model into SQL; never accepts raw SQL.", true
	case agentpolicy.ToolRunQuery:
		return "Run query", "Run a read-only SQL query on a workspace target.", true
	case agentpolicy.ToolGetExecution:
		return "Get execution", "Status and metadata of a query this agent ran.", true
	case agentpolicy.ToolGetResults:
		return "Get results", "A page of rows from a query this agent ran.", true
	case agentpolicy.ToolCancelExecution:
		return "Cancel query", "Stop a query this agent started.", false
	default:
		return name, "", true
	}
}

func ttlDays(d time.Duration) int {
	days := int(d.Hours() / 24)
	if days < 1 {
		return 1
	}
	return days
}

// GetAgentsMeta — GET /agents/meta (#120). Any authenticated user with the
// feature enabled: the UI needs the MCP URL, token TTLs and the tool registry
// before an agent exists.
func (h *Handlers) GetAgentsMeta(ctx context.Context, _ GetAgentsMetaRequestObject) (GetAgentsMetaResponseObject, error) {
	if !h.agentsEnabled() {
		return GetAgentsMeta404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	if _, err := authz.PrincipalFromContext(ctx); err != nil {
		return GetAgentsMeta401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	tools := make([]AgentTool, 0, len(agentpolicy.AllTools))
	for _, name := range agentpolicy.AllTools {
		label, description, readOnly := agentToolDoc(name)
		tools = append(tools, AgentTool{Name: name, Label: label, Description: description, ReadOnly: readOnly})
	}
	mcpURL := h.settings.Mail.PublicURL + "/mcp"
	if h.settings.Mail.PublicURL == "" {
		mcpURL = "/mcp"
	}
	return GetAgentsMeta200JSONResponse(AgentsMeta{
		McpUrl:              mcpURL,
		TokenDefaultTtlDays: ttlDays(h.agentTokenDefaultTTL()),
		TokenMaxTtlDays:     ttlDays(h.agentTokenMaxTTL()),
		Tools:               tools,
	}), nil
}

// GetAgentEligibility — GET /agents/{agent_id}/eligibility (#120). For each
// catalog granted to the agent's bound workspaces, list the engines hosting it
// and whether the agent may run there.
func (h *Handlers) GetAgentEligibility(ctx context.Context, request GetAgentEligibilityRequestObject) (GetAgentEligibilityResponseObject, error) {
	if !h.agentsEnabled() {
		return GetAgentEligibility404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetAgentEligibility401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id, err := uuidFromString(request.AgentId.String())
	if err != nil {
		return GetAgentEligibility404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	agent, ok, err := h.agentScope(ctx, p, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return GetAgentEligibility404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	stored, err := agentpolicy.Parse(agent.Policy)
	if err != nil {
		return nil, err
	}
	effective := stored.Effective(h.agentCeilings())

	workspaces, err := h.queries.ListAgentWorkspaces(ctx, agent.ID)
	if err != nil {
		return nil, err
	}

	out := make([]AgentCatalogEligibility, 0)
	seen := make(map[[16]byte]bool)
	for _, ws := range workspaces {
		grants, err := h.queries.ListWorkspaceCatalogGrants(ctx, ws.ID)
		if err != nil {
			return nil, err
		}
		for _, g := range grants {
			if !g.CatalogID.Valid || seen[g.CatalogID.Bytes] {
				continue
			}
			seen[g.CatalogID.Bytes] = true
			entry := AgentCatalogEligibility{
				CatalogId:   openapi_types.UUID(g.CatalogID.Bytes),
				CatalogName: g.CatalogName,
				Engines:     []AgentEngineEligibility{},
			}
			bindings, err := h.queries.ListBindingsByCatalog(ctx, g.CatalogID)
			if err != nil {
				return nil, err
			}
			for _, b := range bindings {
				if !b.Enabled || !b.EngineID.Valid {
					continue
				}
				engine, err := h.queries.GetEngineByID(ctx, b.EngineID)
				if err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						continue
					}
					return nil, err
				}
				if !engine.Enabled {
					continue
				}
				entry.Engines = append(entry.Engines, h.engineEligibility(ctx, effective, g.CatalogID, b.EngineID, engine))
			}
			out = append(out, entry)
		}
	}
	return GetAgentEligibility200JSONResponse(out), nil
}

func (h *Handlers) engineEligibility(ctx context.Context, effective agentpolicy.Effective, catalogID, engineID pgtype.UUID, engine db.Engine) AgentEngineEligibility {
	out := AgentEngineEligibility{
		EngineId:   openapi_types.UUID(engineID.Bytes),
		EngineName: engine.Name,
		Kind:       engine.Kind,
		Status:     AgentEngineEligibilityStatusOk,
	}
	cat := uuid.UUID(catalogID.Bytes).String()
	eng := uuid.UUID(engineID.Bytes).String()
	if len(effective.Catalogs) > 0 && !effective.Catalogs[cat] {
		out.Status = AgentEngineEligibilityStatusExcludedByPolicy
		return out
	}
	if len(effective.Engines) > 0 && !effective.Engines[eng] {
		out.Status = AgentEngineEligibilityStatusExcludedByPolicy
		return out
	}
	if h.engines == nil {
		return out
	}
	readOnly, err := h.EngineReadOnly(ctx, engineID)
	if err != nil || (readOnly != queryengine.ReadOnlyEnforced && (h.queries == nil || !h.connectionReadOnly(ctx, engineID))) {
		out.Status = AgentEngineEligibilityStatusReadOnlyNotEnforced
	}
	return out
}
