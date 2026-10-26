package api

import (
	"context"
	"errors"
	"time"

	agentmanage "github.com/galileostudio/baleia/internal/agents/manage"
	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Agents (#59). External agents only: assistant agents are managed by #62 and
// are never listed or minted here.

func (h *core) agentsEnabled() bool {
	return h.settings.Agents.Enabled
}

func (h *core) agentTokenDefaultTTL() time.Duration {
	if h.settings.Agents.TokenDefaultTTL > 0 {
		return h.settings.Agents.TokenDefaultTTL
	}
	return auth.DefaultAgentTokenTTL
}

func (h *core) agentTokenMaxTTL() time.Duration {
	if h.settings.Agents.TokenMaxTTL > 0 {
		return h.settings.Agents.TokenMaxTTL
	}
	return auth.MaxAgentTokenTTL
}

// agentNotFound is the single answer for a missing, foreign or assistant agent.
func agentNotFound() NotFoundJSONResponse {
	return NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "agent not found"}
}

// agentScope loads an external agent the caller may see: its owner or a global
// administrator.
func (h *AgentHandlers) agentScope(ctx context.Context, p authz.Principal, id pgtype.UUID) (db.Agent, bool, error) {
	return h.agents.Visible(ctx, agentmanage.Viewer{UserID: p.UserID, Admin: p.IsAdmin()}, id)
}

// agentOwned loads an external agent only when the caller owns it.
func (h *AgentHandlers) agentOwned(ctx context.Context, p authz.Principal, id pgtype.UUID) (db.Agent, bool, error) {
	return h.agents.Owned(ctx, agentmanage.Viewer{UserID: p.UserID, Admin: p.IsAdmin()}, id)
}

func (h *core) agentActions(p authz.Principal, agent db.Agent) []AgentAllowedAction {
	if agent.OwnerID.Bytes == p.UserID.Bytes {
		return []AgentAllowedAction{
			AgentAllowedActionView, AgentAllowedActionUpdate, AgentAllowedActionDisable,
			AgentAllowedActionDelete, AgentAllowedActionManageTokens, AgentAllowedActionManagePolicy,
		}
	}
	if p.IsAdmin() {
		return []AgentAllowedAction{AgentAllowedActionView, AgentAllowedActionDisable, AgentAllowedActionDelete, AgentAllowedActionManagePolicy}
	}
	return nil
}

// agentPublic builds the API view. Token summaries are computed from the
// token list (agents own at most a handful of tokens).
func (h *AgentHandlers) agentPublic(ctx context.Context, p authz.Principal, agent db.Agent) (Agent, error) {
	workspaces, err := h.agents.Workspaces(ctx, agent.ID)
	if err != nil {
		return Agent{}, err
	}
	tokens, err := h.agents.Tokens(ctx, agent.ID)
	if err != nil {
		return Agent{}, err
	}

	now := time.Now()
	active := 0
	var nextExpiry, lastUsed *time.Time
	for _, t := range tokens {
		if t.LastUsedAt.Valid {
			if lastUsed == nil || t.LastUsedAt.Time.After(*lastUsed) {
				v := t.LastUsedAt.Time
				lastUsed = &v
			}
		}
		if t.RevokedAt.Valid || !t.ExpiresAt.Valid || !t.ExpiresAt.Time.After(now) {
			continue
		}
		active++
		if nextExpiry == nil || t.ExpiresAt.Time.Before(*nextExpiry) {
			v := t.ExpiresAt.Time
			nextExpiry = &v
		}
	}

	out := Agent{
		Id:               openapi_types.UUID(agent.ID.Bytes),
		OwnerId:          openapi_types.UUID(agent.OwnerID.Bytes),
		Name:             agent.Name,
		Description:      &agent.Description,
		Status:           AgentStatus(agent.Status),
		Workspaces:       make([]AgentWorkspace, 0, len(workspaces)),
		ActiveTokenCount: &active,
		NextTokenExpiry:  nextExpiry,
		LastUsedAt:       lastUsed,
		CreatedAt:        pgTimestampToTime(agent.CreatedAt),
		AllowedActions:   h.agentActions(p, agent),
	}
	if agent.UpdatedAt.Valid {
		v := agent.UpdatedAt.Time
		out.UpdatedAt = &v
	}
	for _, ws := range workspaces {
		out.Workspaces = append(out.Workspaces, AgentWorkspace{
			Id:   openapi_types.UUID(ws.ID.Bytes),
			Name: ws.Name,
		})
	}
	return out, nil
}

func agentTokenPublic(t db.AgentToken) AgentToken {
	return AgentToken{
		Id:         openapi_types.UUID(t.ID.Bytes),
		Name:       t.Name,
		Prefix:     t.Prefix + "...",
		ExpiresAt:  pgTimestampToTime(t.ExpiresAt),
		CreatedAt:  pgTimestampToTime(t.CreatedAt),
		LastUsedAt: pgTimestampToTimePtr(t.LastUsedAt),
		RevokedAt:  pgTimestampToTimePtr(t.RevokedAt),
	}
}

// validateAgentWorkspaces keeps the workspace ids the owner is a member of.
// Any inaccessible id fails the whole request: the caller cannot probe for
// workspaces it cannot see.
func (h *AgentHandlers) validateAgentWorkspaces(ctx context.Context, owner pgtype.UUID, ids []openapi_types.UUID) ([]pgtype.UUID, error) {
	ws := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		ws = append(ws, pgtype.UUID{Bytes: id, Valid: true})
	}
	return h.agents.CheckWorkspaces(ctx, owner, ws)
}

// ── handlers ─────────────────────────────────────────────────────────────────

func (h *AgentHandlers) ListAgents(ctx context.Context, request ListAgentsRequestObject) (ListAgentsResponseObject, error) {
	if !h.agentsEnabled() {
		return ListAgents404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListAgents401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	all := request.Params.All != nil && *request.Params.All
	if all && !p.IsAdmin() {
		return ListAgents403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "admin only"}}, nil
	}

	agents, err := h.agents.List(ctx, p.UserID, all)
	if err != nil {
		return nil, err
	}

	out := make([]Agent, 0, len(agents))
	for _, a := range agents {
		pub, err := h.agentPublic(ctx, p, a)
		if err != nil {
			return nil, err
		}
		out = append(out, pub)
	}
	return ListAgents200JSONResponse(out), nil
}

func (h *AgentHandlers) CreateAgent(ctx context.Context, request CreateAgentRequestObject) (CreateAgentResponseObject, error) {
	if !h.agentsEnabled() {
		return CreateAgent404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return CreateAgent401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if request.Body == nil {
		return CreateAgent422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: ErrorCodeValidationError, Message: "missing body"}}, nil
	}
	name, err := agentmanage.Name(request.Body.Name)
	if err != nil {
		return CreateAgent422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: ErrorCodeValidationError, Message: err.Error()}}, nil
	}
	description := ""
	if request.Body.Description != nil {
		description = *request.Body.Description
	}

	var workspaceIDs []pgtype.UUID
	if request.Body.WorkspaceIds != nil {
		workspaceIDs, err = h.validateAgentWorkspaces(ctx, p.UserID, *request.Body.WorkspaceIds)
		if err != nil {
			if errors.Is(err, agentmanage.ErrWorkspaceNotVisible) {
				return CreateAgent404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
			}
			return nil, err
		}
	}

	// The agent and its workspace set are one write: a partial set would
	// silently widen or narrow the agent's reach.
	agent, err := h.agents.Create(ctx, p.UserID, name, description, workspaceIDs)
	if err != nil {
		return nil, err
	}

	h.audited(ctx, audit.Event{Action: audit.AgentCreate, ResourceType: "agent", ResourceID: idString(agent.ID), Outcome: audit.Allowed,
		Changes: map[string]audit.Change{"name": {To: agent.Name}}})
	pub, err := h.agentPublic(ctx, p, agent)
	if err != nil {
		return nil, err
	}
	return CreateAgent201JSONResponse(pub), nil
}

func (h *AgentHandlers) GetAgent(ctx context.Context, request GetAgentRequestObject) (GetAgentResponseObject, error) {
	if !h.agentsEnabled() {
		return GetAgent404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetAgent401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id, err := uuidFromString(request.AgentId.String())
	if err != nil {
		return GetAgent404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	agent, ok, err := h.agentScope(ctx, p, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return GetAgent404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	pub, err := h.agentPublic(ctx, p, agent)
	if err != nil {
		return nil, err
	}
	return GetAgent200JSONResponse(pub), nil
}

func (h *AgentHandlers) UpdateAgent(ctx context.Context, request UpdateAgentRequestObject) (UpdateAgentResponseObject, error) {
	if !h.agentsEnabled() {
		return UpdateAgent404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return UpdateAgent401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if request.Body == nil {
		return UpdateAgent422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: ErrorCodeValidationError, Message: "missing body"}}, nil
	}
	id, err := uuidFromString(request.AgentId.String())
	if err != nil {
		return UpdateAgent404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	agent, ok, err := h.agentScope(ctx, p, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return UpdateAgent404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}

	owner := agent.OwnerID.Bytes == p.UserID.Bytes
	// An administrator who does not own the agent may only disable it (#59).
	if !owner {
		onlyDisable := request.Body.Status != nil && *request.Body.Status == UpdateAgentRequestStatusDisabled &&
			request.Body.Name == nil && request.Body.Description == nil && request.Body.WorkspaceIds == nil
		if !onlyDisable {
			return UpdateAgent403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "an admin may only disable an agent"}}, nil
		}
	}

	name := agent.Name
	if request.Body.Name != nil {
		if name, err = agentmanage.Name(*request.Body.Name); err != nil {
			return UpdateAgent422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: ErrorCodeValidationError, Message: err.Error()}}, nil
		}
	}
	description := agent.Description
	if request.Body.Description != nil {
		description = *request.Body.Description
	}

	// Validate the new workspace set before writing anything: a refused PATCH
	// must not leave a name or status change behind.
	var workspaceIDs []pgtype.UUID
	if request.Body.WorkspaceIds != nil {
		workspaceIDs, err = h.validateAgentWorkspaces(ctx, agent.OwnerID, *request.Body.WorkspaceIds)
		if err != nil {
			if errors.Is(err, agentmanage.ErrWorkspaceNotVisible) {
				return UpdateAgent404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
			}
			return nil, err
		}
	}

	changes := map[string]audit.Change{"name": {From: agent.Name, To: name}}
	if request.Body.Status != nil {
		changes["status"] = audit.Change{From: agent.Status, To: string(*request.Body.Status)}
	}
	if request.Body.WorkspaceIds != nil {
		current, err := h.agents.Workspaces(ctx, agent.ID)
		if err != nil {
			return nil, err
		}
		from := make([]string, 0, len(current))
		for _, ws := range current {
			from = append(from, idString(ws.ID))
		}
		to := make([]string, 0, len(workspaceIDs))
		for _, id := range workspaceIDs {
			to = append(to, idString(id))
		}
		changes["workspace_ids"] = audit.Change{From: from, To: to}
	}

	// Name, status and workspace set change together or not at all (#376).
	edit := agentmanage.Edit{Name: name, Description: description}
	if request.Body.Status != nil {
		status := string(*request.Body.Status)
		edit.Status = &status
	}
	if request.Body.WorkspaceIds != nil {
		edit.Workspaces = &workspaceIDs
	}
	updated, err := h.agents.Update(ctx, agent, edit)
	if err != nil {
		return nil, err
	}

	action := audit.AgentUpdate
	if request.Body.Status != nil && *request.Body.Status == UpdateAgentRequestStatusDisabled {
		action = audit.AgentDisable
	}
	h.audited(ctx, audit.Event{Action: action, ResourceType: "agent", ResourceID: idString(agent.ID), Outcome: audit.Allowed,
		Changes: changes})

	pub, err := h.agentPublic(ctx, p, updated)
	if err != nil {
		return nil, err
	}
	return UpdateAgent200JSONResponse(pub), nil
}

func (h *AgentHandlers) DeleteAgent(ctx context.Context, request DeleteAgentRequestObject) (DeleteAgentResponseObject, error) {
	if !h.agentsEnabled() {
		return DeleteAgent404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return DeleteAgent401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id, err := uuidFromString(request.AgentId.String())
	if err != nil {
		return DeleteAgent404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	agent, ok, err := h.agentScope(ctx, p, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return DeleteAgent404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	if err := h.agents.Delete(ctx, agent.ID); err != nil {
		return nil, err
	}
	h.audited(ctx, audit.Event{Action: audit.AgentDelete, ResourceType: "agent", ResourceID: idString(agent.ID), Outcome: audit.Allowed})
	return DeleteAgent204Response{}, nil
}

func (h *AgentHandlers) ListAgentTokens(ctx context.Context, request ListAgentTokensRequestObject) (ListAgentTokensResponseObject, error) {
	if !h.agentsEnabled() {
		return ListAgentTokens404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListAgentTokens401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id, err := uuidFromString(request.AgentId.String())
	if err != nil {
		return ListAgentTokens404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	agent, ok, err := h.agentScope(ctx, p, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ListAgentTokens404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	tokens, err := h.agents.Tokens(ctx, agent.ID)
	if err != nil {
		return nil, err
	}
	out := make([]AgentToken, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, agentTokenPublic(t))
	}
	return ListAgentTokens200JSONResponse(out), nil
}

func (h *AgentHandlers) CreateAgentToken(ctx context.Context, request CreateAgentTokenRequestObject) (CreateAgentTokenResponseObject, error) {
	if !h.agentsEnabled() {
		return CreateAgentToken404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return CreateAgentToken401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if request.Body == nil {
		return CreateAgentToken422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: ErrorCodeValidationError, Message: "missing body"}}, nil
	}
	name, err := agentmanage.Name(request.Body.Name)
	if err != nil {
		return CreateAgentToken422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: ErrorCodeValidationError, Message: err.Error()}}, nil
	}
	id, err := uuidFromString(request.AgentId.String())
	if err != nil {
		return CreateAgentToken404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	agent, ok, err := h.agentOwned(ctx, p, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return CreateAgentToken404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}

	ttl, err := agentmanage.TokenTTL(request.Body.ExpiresInDays, h.agentTokenDefaultTTL(), h.agentTokenMaxTTL())
	if err != nil {
		return CreateAgentToken422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: ErrorCodeValidationError, Message: err.Error()}}, nil
	}

	token, secret, err := h.agents.IssueToken(ctx, agent.ID, name, ttl, p.UserID)
	if err != nil {
		return nil, err
	}

	h.audited(ctx, audit.Event{Action: audit.AgentTokenCreate, ResourceType: "agent_token", ResourceID: idString(token.ID), Outcome: audit.Allowed,
		Changes: map[string]audit.Change{"name": {To: token.Name}, "prefix": {To: token.Prefix}}})
	pub := agentTokenPublic(token)
	return CreateAgentToken201JSONResponse(AgentTokenCreated{
		Id:         pub.Id,
		Name:       pub.Name,
		Prefix:     pub.Prefix,
		ExpiresAt:  pub.ExpiresAt,
		LastUsedAt: pub.LastUsedAt,
		RevokedAt:  pub.RevokedAt,
		CreatedAt:  pub.CreatedAt,
		Token:      secret,
	}), nil
}

func (h *AgentHandlers) RevokeAgentToken(ctx context.Context, request RevokeAgentTokenRequestObject) (RevokeAgentTokenResponseObject, error) {
	if !h.agentsEnabled() {
		return RevokeAgentToken404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return RevokeAgentToken401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id, err := uuidFromString(request.AgentId.String())
	if err != nil {
		return RevokeAgentToken404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	agent, ok, err := h.agentOwned(ctx, p, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return RevokeAgentToken404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
	}
	tokenID, err := uuidFromString(request.TokenId.String())
	if err != nil {
		return RevokeAgentToken404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "agent token not found"}}, nil
	}
	err = h.agents.RevokeToken(ctx, agent.ID, tokenID)
	if errors.Is(err, agentmanage.ErrTokenNotFound) {
		return RevokeAgentToken404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: err.Error()}}, nil
	}
	if err != nil {
		return nil, err
	}
	h.audited(ctx, audit.Event{Action: audit.AgentTokenRevoke, ResourceType: "agent_token", ResourceID: idString(tokenID), Outcome: audit.Allowed})
	return RevokeAgentToken204Response{}, nil
}
