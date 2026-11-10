// Package webhooks defines the outbound event envelope and its constructors.
// Events are staged in the durable outbox and relayed onto the webhooks Redis
// Stream (ADR 0022).
package webhooks

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Event is one outbound webhook event. WorkspaceID is empty for events that are
// not workspace-scoped (a catalog sync is engine-wide).
type Event struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	WorkspaceID string          `json:"workspace_id,omitempty"`
	OccurredAt  time.Time       `json:"occurred_at"`
	Data        json.RawMessage `json:"data,omitempty"`
}

// New builds an event with a fresh id and the current time.
func New(typ, workspaceID string, data any) Event {
	raw, _ := json.Marshal(data)
	return Event{
		ID:          uuid.NewString(),
		Type:        typ,
		WorkspaceID: workspaceID,
		OccurredAt:  time.Now().UTC(),
		Data:        raw,
	}
}

// Marshal is the JSON stored in the outbox and the Stream.
func (e Event) Marshal() string {
	raw, _ := json.Marshal(e)
	return string(raw)
}

// ExecutionFinishedEvent builds the terminal-execution event.
func ExecutionFinishedEvent(workspaceID, executionID, status string, rows *int64, durationMs *int64, errorCode *string) Event {
	return New("execution.finished", workspaceID, map[string]any{
		"execution_id":  executionID,
		"status":        status,
		"rows_returned": rows,
		"duration_ms":   durationMs,
		"error_code":    errorCode,
	})
}

// CatalogSyncedEvent builds the metadata-sync event. It carries no tenant
// data, only the catalog id.
func CatalogSyncedEvent(catalogID string) Event {
	return New("catalog.synced", "", map[string]any{"catalog_id": catalogID})
}

// AuditEvent builds a best-effort audit event.
func AuditEvent(workspaceID, action, resourceType, resourceID, outcome string) Event {
	return New("audit", workspaceID, map[string]any{
		"action":        action,
		"resource_type": resourceType,
		"resource_id":   resourceID,
		"outcome":       outcome,
	})
}
