package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Agents (#59). External agents only: assistant agents are managed by #62 and
// are never listed or minted here.

var errWorkspaceNotVisible = errors.New("workspace not visible")

func (h *Handlers) agentsEnabled() bool {
	return h.cfg != nil && h.cfg.AgentsEnabled
}

func (h *Handlers) agentTokenDefaultTTL() time.Duration {
	if h.cfg != nil && h.cfg.AgentTokenDefaultTTL > 0 {
		return h.cfg.AgentTokenDefaultTTL
	}
	return auth.DefaultAgentTokenTTL
}

func (h *Handlers) agentTokenMaxTTL() time.Duration {
	if h.cfg != nil && h.cfg.AgentTokenMaxTTL > 0 {
		return h.cfg.AgentTokenMaxTTL
	}
	return auth.MaxAgentTokenTTL
}

// agentNotFound is the single answer for a missing, foreign or assistant agent.
func agentNotFound() NotFoundJSONResponse {
	return NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "agent not found"}
}

// agentScope loads an external agent the caller may see: its owner or a global
// administrator.
func (h *Handlers) agentScope(ctx context.Context, p authz.Principal, id pgtype.UUID) (db.Agent, bool, error) {
	agent, err := h.queries.GetAgentByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Agent{}, false, nil
		}
		return db.Agent{}, false, err
	}
	if agent.Kind != "external" {
		return db.Agent{}, false, nil
	}
	if agent.OwnerID.Bytes == p.UserID.Bytes || p.IsAdmin() {
		return agent, true, nil
	}
	return db.Agent{}, false, nil
}

// agentOwned loads an external agent only when the caller owns it.
func (h *Handlers) agentOwned(ctx context.Context, p authz.Principal, id pgtype.UUID) (db.Agent, bool, error) {
	agent, ok, err := h.agentScope(ctx, p, id)
	if err != nil || !ok {
		return db.Agent{}, false, err
	}
	if agent.OwnerID.Bytes != p.UserID.Bytes {
		return db.Agent{}, false, nil
	}
	return agent, true, nil
}

func (h *Handlers) agentActions(p authz.Principal, agent db.Agent) []AgentAllowedAction {
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
func (h *Handlers) agentPublic(ctx context.Context, p authz.Principal, agent db.Agent) (Agent, error) {
	workspaces, err := h.queries.ListAgentWorkspaces(ctx, agent.ID)
	if err != nil {
		return Agent{}, err
	}
	tokens, err := h.queries.ListAgentTokensByAgent(ctx, agent.ID)
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
func (h *Handlers) validateAgentWorkspaces(ctx context.Context, owner pgtype.UUID, ids []openapi_types.UUID) ([]pgtype.UUID, error) {
	out := make([]pgtype.UUID, 0, len(ids))
	seen := make(map[[16]byte]bool, len(ids))
	for _, id := range ids {
		ws := pgtype.UUID{Bytes: id, Valid: true}
		if seen[ws.Bytes] {
			continue
		}
		seen[ws.Bytes] = true
		ok, err := h.queries.CheckWorkspaceAccess(ctx, db.CheckWorkspaceAccessParams{ID: ws, UserID: owner})
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errWorkspaceNotVisible
		}
		out = append(out, ws)
	}
	return out, nil
}

func (h *Handlers) addAgentWorkspaces(ctx context.Context, agentID pgtype.UUID, ws []pgtype.UUID) error {
	// Keep the replacement atomic: a partial set would silently widen or
	// narrow the agent's reach.
	return h.inTx(ctx, func(q db.Querier) error { return addAgentWorkspaces(ctx, q, agentID, ws) })
}

func addAgentWorkspaces(ctx context.Context, q db.Querier, agentID pgtype.UUID, ws []pgtype.UUID) error {
	if err := q.ClearAgentWorkspaces(ctx, agentID); err != nil {
		return err
	}
	for _, id := range ws {
		if err := q.AddAgentWorkspace(ctx, db.AddAgentWorkspaceParams{AgentID: agentID, WorkspaceID: id}); err != nil {
			return err
		}
	}
	return nil
}

// ── handlers ─────────────────────────────────────────────────────────────────

func (h *Handlers) ListAgents(ctx context.Context, request ListAgentsRequestObject) (ListAgentsResponseObject, error) {
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

	var agents []db.Agent
	if all {
		agents, err = h.queries.ListAllAgents(ctx)
	} else {
		agents, err = h.queries.ListAgentsByOwner(ctx, p.UserID)
	}
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

func (h *Handlers) CreateAgent(ctx context.Context, request CreateAgentRequestObject) (CreateAgentResponseObject, error) {
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
	name := strings.TrimSpace(request.Body.Name)
	if name == "" || len(name) > 80 {
		return CreateAgent422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: ErrorCodeValidationError, Message: "name must be 1-80 characters"}}, nil
	}
	description := ""
	if request.Body.Description != nil {
		description = *request.Body.Description
	}

	var workspaceIDs []pgtype.UUID
	if request.Body.WorkspaceIds != nil {
		workspaceIDs, err = h.validateAgentWorkspaces(ctx, p.UserID, *request.Body.WorkspaceIds)
		if err != nil {
			if errors.Is(err, errWorkspaceNotVisible) {
				return CreateAgent404JSONResponse{NotFoundJSONResponse: agentNotFound()}, nil
			}
			return nil, err
		}
	}

	agent, err := h.queries.CreateAgent(ctx, db.CreateAgentParams{
		OwnerID:     p.UserID,
		Kind:        "external",
		Name:        name,
		Description: description,
	})
	if err != nil {
		return nil, err
	}
	if err := h.addAgentWorkspaces(ctx, agent.ID, workspaceIDs); err != nil {
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

func (h *Handlers) GetAgent(ctx context.Context, request GetAgentRequestObject) (GetAgentResponseObject, error) {
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

func (h *Handlers) UpdateAgent(ctx context.Context, request UpdateAgentRequestObject) (UpdateAgentResponseObject, error) {
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
		name = strings.TrimSpace(*request.Body.Name)
		if name == "" || len(name) > 80 {
			return UpdateAgent422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: ErrorCodeValidationError, Message: "name must be 1-80 characters"}}, nil
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
			if errors.Is(err, errWorkspaceNotVisible) {
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
		current, err := h.queries.ListAgentWorkspaces(ctx, agent.ID)
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

	if _, err := h.queries.UpdateAgent(ctx, db.UpdateAgentParams{ID: agent.ID, Name: name, Description: description}); err != nil {
		return nil, err
	}
	if request.Body.Status != nil && string(*request.Body.Status) != agent.Status {
		if _, err := h.queries.SetAgentStatus(ctx, db.SetAgentStatusParams{ID: agent.ID, Status: string(*request.Body.Status)}); err != nil {
			return nil, err
		}
	}
	if request.Body.WorkspaceIds != nil {
		if err := h.addAgentWorkspaces(ctx, agent.ID, workspaceIDs); err != nil {
			return nil, err
		}
	}

	action := audit.AgentUpdate
	if request.Body.Status != nil && *request.Body.Status == UpdateAgentRequestStatusDisabled {
		action = audit.AgentDisable
	}
	h.audited(ctx, audit.Event{Action: action, ResourceType: "agent", ResourceID: idString(agent.ID), Outcome: audit.Allowed,
		Changes: changes})

	agent, err = h.queries.GetAgentByID(ctx, agent.ID)
	if err != nil {
		return nil, err
	}
	pub, err := h.agentPublic(ctx, p, agent)
	if err != nil {
		return nil, err
	}
	return UpdateAgent200JSONResponse(pub), nil
}

func (h *Handlers) DeleteAgent(ctx context.Context, request DeleteAgentRequestObject) (DeleteAgentResponseObject, error) {
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
	if err := h.queries.DeleteAgent(ctx, agent.ID); err != nil {
		return nil, err
	}
	h.audited(ctx, audit.Event{Action: audit.AgentDelete, ResourceType: "agent", ResourceID: idString(agent.ID), Outcome: audit.Allowed})
	return DeleteAgent204Response{}, nil
}

func (h *Handlers) ListAgentTokens(ctx context.Context, request ListAgentTokensRequestObject) (ListAgentTokensResponseObject, error) {
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
	tokens, err := h.queries.ListAgentTokensByAgent(ctx, agent.ID)
	if err != nil {
		return nil, err
	}
	out := make([]AgentToken, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, agentTokenPublic(t))
	}
	return ListAgentTokens200JSONResponse(out), nil
}

func (h *Handlers) CreateAgentToken(ctx context.Context, request CreateAgentTokenRequestObject) (CreateAgentTokenResponseObject, error) {
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
	name := strings.TrimSpace(request.Body.Name)
	if name == "" || len(name) > 80 {
		return CreateAgentToken422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: ErrorCodeValidationError, Message: "name must be 1-80 characters"}}, nil
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

	ttl := h.agentTokenDefaultTTL()
	if request.Body.ExpiresInDays != nil {
		days := *request.Body.ExpiresInDays
		if days < 1 {
			return CreateAgentToken422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: ErrorCodeValidationError, Message: "expires_in_days must be at least 1"}}, nil
		}
		ttl = time.Duration(days) * 24 * time.Hour
		if ttl > h.agentTokenMaxTTL() {
			return CreateAgentToken422JSONResponse{UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{Code: ErrorCodeValidationError, Message: "expires_in_days exceeds the server maximum"}}, nil
		}
	}

	secret, hash, prefix := auth.NewAgentToken()
	token, err := h.queries.CreateAgentToken(ctx, db.CreateAgentTokenParams{
		AgentID:   agent.ID,
		Name:      name,
		TokenHash: hash,
		Prefix:    prefix,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(ttl), Valid: true},
		CreatedBy: p.UserID,
	})
	if err != nil {
		return nil, err
	}

	// Never audit the secret, only its name and prefix.
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

func (h *Handlers) RevokeAgentToken(ctx context.Context, request RevokeAgentTokenRequestObject) (RevokeAgentTokenResponseObject, error) {
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
	if _, err := h.queries.GetAgentTokenByID(ctx, db.GetAgentTokenByIDParams{ID: tokenID, AgentID: agent.ID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RevokeAgentToken404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "agent token not found"}}, nil
		}
		return nil, err
	}
	if err := h.queries.RevokeAgentToken(ctx, db.RevokeAgentTokenParams{ID: tokenID, AgentID: agent.ID}); err != nil {
		return nil, err
	}
	h.audited(ctx, audit.Event{Action: audit.AgentTokenRevoke, ResourceType: "agent_token", ResourceID: idString(tokenID), Outcome: audit.Allowed})
	return RevokeAgentToken204Response{}, nil
}
