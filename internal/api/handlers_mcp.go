package api

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/agentpolicy"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/results"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// AgentWorkspaces returns the workspaces the current agent principal is bound
// to (#58). The MCP list_workspaces tool uses it instead of the owner's full
// list.
func (h *AgentHandlers) AgentWorkspaces(ctx context.Context) ([]Workspace, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if !p.IsAgent() {
		return nil, authz.ErrUnauthenticated
	}
	rows, err := h.queries.ListAgentWorkspaces(ctx, p.AgentID)
	if err != nil {
		return nil, err
	}
	out := make([]Workspace, 0, len(rows))
	for _, ws := range rows {
		out = append(out, dbWorkspaceToPublic(ws))
	}
	return out, nil
}

// MCPResults reads a bounded page of a stored Arrow result for an agent tool
func (h *AgentHandlers) MCPResults(ctx context.Context, workspaceID, executionID openapi_types.UUID, offset, limit, maxRows, maxBytes int) (results.Result, error) {
	if err := requireWorkspaceAccess(ctx, h.queries, workspaceID); err != nil {
		return results.Result{}, err
	}
	var id pgtype.UUID
	id.Bytes = executionID
	id.Valid = true
	exec, err := h.queries.GetQueryExecutionByIDAndWorkspace(ctx, db.GetQueryExecutionByIDAndWorkspaceParams{
		ID:          id,
		WorkspaceID: workspaceIDFromRequest(workspaceID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return results.Result{}, ErrWorkspaceNotFound
		}
		return results.Result{}, err
	}
	if !agentOwnsExecution(ctx, exec) {
		return results.Result{}, ErrWorkspaceNotFound
	}
	if exec.Status != "finished" || exec.ResultKey == nil || *exec.ResultKey == "" {
		return results.Result{}, ErrWorkspaceNotFound
	}
	if h.s3 == nil {
		return results.Result{}, errors.New("results storage is not configured")
	}
	return results.Read(ctx, h.s3, *exec.ResultKey, offset, limit, maxRows, maxBytes)
}

// AgentCeilings exposes the server-side policy ceilings to the MCP wiring.
func (h *AgentHandlers) AgentCeilings() agentpolicy.Ceilings { return h.agentCeilings() }

// EngineReadOnly reports whether an engine can enforce a read-only session;
// the agent gate needs it.
func (h *AgentHandlers) EngineReadOnly(ctx context.Context, engineID pgtype.UUID) (queryengine.ReadOnlySupport, error) {
	eng, err := h.engines.Get(ctx, engineID)
	if err != nil {
		return "", err
	}
	return eng.Capabilities().ReadOnly, nil
}
