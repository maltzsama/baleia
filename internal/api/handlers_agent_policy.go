package api

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/galileostudio/baleia/internal/agentpolicy"
	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// agentCeilings are the server-side maxima (#60). A zero config value falls
// back to the packaged default.
func (h *core) agentCeilings() agentpolicy.Ceilings {
	c := agentpolicy.DefaultCeilings()
	if h.settings.Agents.MaxRows > 0 {
		c.MaxRowsReturned = h.settings.Agents.MaxRows
	}
	if h.settings.Agents.MaxResultBytes > 0 {
		c.MaxResultBytes = h.settings.Agents.MaxResultBytes
	}
	if h.settings.Agents.MaxQueryRuntimeSeconds > 0 {
		c.MaxQueryRuntimeSeconds = h.settings.Agents.MaxQueryRuntimeSeconds
	}
	if h.settings.Agents.MaxConcurrentQueries > 0 {
		c.MaxConcurrentQueries = h.settings.Agents.MaxConcurrentQueries
	}
	if h.settings.Agents.MaxQueriesPerHour > 0 {
		c.MaxQueriesPerHour = h.settings.Agents.MaxQueriesPerHour
	}
	if h.settings.Agents.MaxToolCallsPerMinute > 0 {
		c.MaxToolCallsPerMinute = h.settings.Agents.MaxToolCallsPerMinute
	}
	return c
}

func policyFromAPI(p AgentPolicy) agentpolicy.Policy {
	out := agentpolicy.Policy{Version: int(p.Version)}
	if p.Tools != nil {
		out.Tools = append([]string(nil), (*p.Tools)...)
	}
	if p.Catalogs != nil {
		for _, id := range *p.Catalogs {
			out.Catalogs = append(out.Catalogs, id.String())
		}
	}
	if p.Engines != nil {
		for _, id := range *p.Engines {
			out.Engines = append(out.Engines, id.String())
		}
	}
	setLimit(&out.MaxRowsReturned, p.MaxRowsReturned)
	setLimit(&out.MaxResultBytes, p.MaxResultBytes)
	setLimit(&out.MaxQueryRuntimeSeconds, p.MaxQueryRuntimeSeconds)
	setLimit(&out.MaxConcurrentQueries, p.MaxConcurrentQueries)
	setLimit(&out.MaxQueriesPerHour, p.MaxQueriesPerHour)
	setLimit(&out.MaxToolCallsPerMinute, p.MaxToolCallsPerMinute)
	return out
}

func setLimit(dst *int, src *int) {
	if src != nil {
		*dst = *src
	}
}

// policyToAPI renders a stored policy. Zero limits are omitted (they mean
// "default"), so the UI can show a placeholder for each unset field.
func policyToAPI(p agentpolicy.Policy) AgentPolicy {
	out := AgentPolicy{Version: AgentPolicyVersion(p.Version)}
	if out.Version == 0 {
		out.Version = AgentPolicyVersion(1)
	}
	if p.Tools != nil {
		tools := append([]string(nil), p.Tools...)
		out.Tools = &tools
	}
	if p.Catalogs != nil {
		ids := make([]openapi_types.UUID, 0, len(p.Catalogs))
		for _, s := range p.Catalogs {
			if id, err := uuid.Parse(s); err == nil {
				ids = append(ids, openapi_types.UUID(id))
			}
		}
		out.Catalogs = &ids
	}
	if p.Engines != nil {
		ids := make([]openapi_types.UUID, 0, len(p.Engines))
		for _, s := range p.Engines {
			if id, err := uuid.Parse(s); err == nil {
				ids = append(ids, openapi_types.UUID(id))
			}
		}
		out.Engines = &ids
	}
	out.MaxRowsReturned = limitPtr(p.MaxRowsReturned)
	out.MaxResultBytes = limitPtr(p.MaxResultBytes)
	out.MaxQueryRuntimeSeconds = limitPtr(p.MaxQueryRuntimeSeconds)
	out.MaxConcurrentQueries = limitPtr(p.MaxConcurrentQueries)
	out.MaxQueriesPerHour = limitPtr(p.MaxQueriesPerHour)
	out.MaxToolCallsPerMinute = limitPtr(p.MaxToolCallsPerMinute)
	return out
}

// effectiveToAPI renders the evaluated policy: every limit is present and the
// tools set is explicit.
func effectiveToAPI(e agentpolicy.Effective, version int) AgentPolicy {
	out := AgentPolicy{Version: AgentPolicyVersion(version)}
	if out.Version == 0 {
		out.Version = AgentPolicyVersion(1)
	}
	tools := make([]string, 0, len(e.Tools))
	for _, t := range agentpolicy.AllTools {
		if e.Tools[t] {
			tools = append(tools, t)
		}
	}
	out.Tools = &tools
	if e.Catalogs != nil {
		ids := make([]openapi_types.UUID, 0, len(e.Catalogs))
		for _, s := range sortedKeys(e.Catalogs) {
			if id, err := uuid.Parse(s); err == nil {
				ids = append(ids, openapi_types.UUID(id))
			}
		}
		out.Catalogs = &ids
	}
	if e.Engines != nil {
		ids := make([]openapi_types.UUID, 0, len(e.Engines))
		for _, s := range sortedKeys(e.Engines) {
			if id, err := uuid.Parse(s); err == nil {
				ids = append(ids, openapi_types.UUID(id))
			}
		}
		out.Engines = &ids
	}
	out.MaxRowsReturned = &e.MaxRowsReturned
	out.MaxResultBytes = &e.MaxResultBytes
	out.MaxQueryRuntimeSeconds = &e.MaxQueryRuntimeSeconds
	out.MaxConcurrentQueries = &e.MaxConcurrentQueries
	out.MaxQueriesPerHour = &e.MaxQueriesPerHour
	out.MaxToolCallsPerMinute = &e.MaxToolCallsPerMinute
	return out
}

func ceilingsToAPI(c agentpolicy.Ceilings) AgentPolicyLimits {
	return AgentPolicyLimits{
		MaxRowsReturned:        &c.MaxRowsReturned,
		MaxResultBytes:         &c.MaxResultBytes,
		MaxQueryRuntimeSeconds: &c.MaxQueryRuntimeSeconds,
		MaxConcurrentQueries:   &c.MaxConcurrentQueries,
		MaxQueriesPerHour:      &c.MaxQueriesPerHour,
		MaxToolCallsPerMinute:  &c.MaxToolCallsPerMinute,
	}
}

func limitPtr(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (h *AgentHandlers) GetAgentPolicy(ctx context.Context, request GetAgentPolicyRequestObject) (GetAgentPolicyResponseObject, error) {
	if !h.agentsEnabled() {
		return GetAgentPolicy404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetAgentPolicy401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id, err := uuidFromString(request.AgentId.String())
	if err != nil {
		return GetAgentPolicy404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	agent, ok, err := h.agentScope(ctx, p, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return GetAgentPolicy404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	stored, err := agentpolicy.Parse(agent.Policy)
	if err != nil {
		return nil, err
	}
	ceilings := h.agentCeilings()
	effective := stored.Effective(ceilings)
	return GetAgentPolicy200JSONResponse(AgentPolicyResponse{
		Policy:   effectiveToAPI(effective, stored.Version),
		Stored:   policyToAPI(stored),
		Ceilings: ceilingsToAPI(ceilings),
	}), nil
}

func (h *AgentHandlers) PutAgentPolicy(ctx context.Context, request PutAgentPolicyRequestObject) (PutAgentPolicyResponseObject, error) {
	if !h.agentsEnabled() {
		return PutAgentPolicy404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return PutAgentPolicy401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if request.Body == nil {
		return PutAgentPolicy422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "missing body")}, nil
	}
	id, err := uuidFromString(request.AgentId.String())
	if err != nil {
		return PutAgentPolicy404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	agent, ok, err := h.agentScope(ctx, p, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return PutAgentPolicy404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}

	stored := policyFromAPI(*request.Body)
	ceilings := h.agentCeilings()
	if err := stored.Validate(ceilings); err != nil {
		return PutAgentPolicy422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}
	updated, err := h.agents.SetPolicy(ctx, agent.ID, raw, p.UserID)
	if err != nil {
		return nil, err
	}
	h.audited(ctx, audit.Event{Action: audit.AgentPolicyUpdate, ResourceType: "agent", ResourceID: idString(agent.ID), Outcome: audit.Allowed,
		Changes: map[string]audit.Change{"policy": {From: string(agent.Policy), To: string(raw)}}})

	stored, err = agentpolicy.Parse(updated.Policy)
	if err != nil {
		return nil, err
	}
	effective := stored.Effective(ceilings)
	return PutAgentPolicy200JSONResponse(AgentPolicyResponse{
		Policy:   effectiveToAPI(effective, stored.Version),
		Stored:   policyToAPI(stored),
		Ceilings: ceilingsToAPI(ceilings),
	}), nil
}
