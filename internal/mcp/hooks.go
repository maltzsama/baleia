package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/galileostudio/baleia/internal/agentpolicy"
	"github.com/galileostudio/baleia/internal/audit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// AuditHooks records exactly one agent.tool_call per invocation (#61). The
// dispatch wrapper calls it once for a refusal and once for a completed call;
// no other code records agent tool calls.
type AuditHooks struct{ rec audit.Recorder }

// NewAuditHooks wires the audit recorder into the MCP dispatch.
func NewAuditHooks(rec audit.Recorder) AuditHooks { return AuditHooks{rec: rec} }

// OnToolCall writes the event. Arguments are never stored verbatim: SQL lives
// in query_executions and args_sha256 lets an investigator match a statement.
func (h AuditHooks) OnToolCall(ctx context.Context, c agentpolicy.Call, o Outcome) {
	if h.rec == nil {
		return
	}
	outcome := audit.Allowed
	reason := ""
	switch {
	case !o.Allowed:
		outcome = audit.Denied
		reason = string(o.Reason)
	case o.Err != nil:
		outcome = audit.Error
		reason = audit.ReasonInternal
		if errors.Is(o.Err, errNotFound) {
			outcome, reason = audit.Denied, audit.ReasonNotFound
		}
		if errors.Is(o.Err, errInvalid) {
			outcome, reason = audit.Denied, audit.ReasonValidation
		}
	}
	resourceType, resourceID := auditResource(c, o)
	ev := audit.Event{
		Action:       audit.AgentToolCall,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		WorkspaceID:  uuidString(c.WorkspaceID),
		Outcome:      outcome,
		Reason:       reason,
		Changes: map[string]audit.Change{
			"tool":          {To: c.Tool},
			"args_sha256":   {To: argsHash(c)},
			"rows_returned": {To: o.Rows},
			"truncated":     {To: o.Truncated},
			"duration_ms":   {To: o.Duration.Milliseconds()},
		},
	}
	_ = h.rec.Record(ctx, ev)
}

func auditResource(c agentpolicy.Call, o Outcome) (string, string) {
	if o.ExecutionID != "" {
		return "execution", o.ExecutionID
	}
	if c.ExecutionID != nil && c.ExecutionID.Valid {
		return "execution", uuidString(*c.ExecutionID)
	}
	if c.CatalogID != nil && c.CatalogID.Valid && c.Tool != agentpolicy.ToolRunQuery {
		return "catalog", uuidString(*c.CatalogID)
	}
	return "workspace", uuidString(c.WorkspaceID)
}

type auditArgs struct {
	Tool        string `json:"tool"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	CatalogID   string `json:"catalog_id,omitempty"`
	EngineID    string `json:"engine_id,omitempty"`
	ExecutionID string `json:"execution_id,omitempty"`
	SQL         string `json:"sql,omitempty"`
}

// argsHash is SHA-256 over the canonical JSON of the arguments.
func argsHash(c agentpolicy.Call) string {
	a := auditArgs{Tool: c.Tool, WorkspaceID: uuidString(c.WorkspaceID)}
	if c.CatalogID != nil {
		a.CatalogID = uuidString(*c.CatalogID)
	}
	if c.EngineID != nil {
		a.EngineID = uuidString(*c.EngineID)
	}
	if c.ExecutionID != nil {
		a.ExecutionID = uuidString(*c.ExecutionID)
	}
	if c.SQL != nil {
		a.SQL = *c.SQL
	}
	raw, _ := json.Marshal(a)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
