package offload

import (
	"context"
	"encoding/json"
	"time"

	"github.com/galileostudio/baleia/internal/audit/export"
)

// AuditReader is the PeriodReader of the audit stream: it reads audit_events
// through the export Source and maps each row to a lossless cold document. The
// object is named after the stream, so the chunk keys are
// audit-YYYYMMDD-<tie>.ndjson.gz.
type AuditReader struct {
	Source   export.Source
	Name     string
	Batch    int
	Document func(export.Event) map[string]any
}

// EarliestPeriod is the day of the first audit row, or the zero time.
func (r *AuditReader) EarliestPeriod(ctx context.Context, before time.Time) (time.Time, error) {
	events, err := r.Source.Events(ctx, export.Position{}, before, 1)
	if err != nil || len(events) == 0 {
		return time.Time{}, err
	}
	return period.Start(events[0].OccurredAt), nil
}

// ReadPeriod streams the audit rows of the day as bounded chunks.
func (r *AuditReader) ReadPeriod(ctx context.Context, day, before time.Time, pos *export.Position, emit func([]Chunk, export.Position) error) error {
	doc := r.Document
	if doc == nil {
		doc = ColdAuditDocument
	}
	name := r.Name
	if name == "" {
		name = export.StreamAudit
	}
	readPos := *pos
	limit := period.Next(day)
	ch := &chunker{object: name}
	for {
		events, err := r.Source.Events(ctx, readPos, before, r.batch())
		if err != nil {
			return err
		}
		if len(events) == 0 {
			break
		}
		for _, ev := range events {
			if !ev.OccurredAt.Before(limit) {
				return r.flush(ch, emit)
			}
			data, err := json.Marshal(doc(ev))
			if err != nil {
				return err
			}
			readPos = export.Position{OccurredAt: ev.OccurredAt, ID: ev.ID}
			ch.add(readPos, data)
			if ch.full() {
				if err := r.flush(ch, emit); err != nil {
					return err
				}
			}
		}
		if len(events) < r.batch() {
			break
		}
	}
	return r.flush(ch, emit)
}

func (r *AuditReader) flush(ch *chunker, emit func([]Chunk, export.Position) error) error {
	c, ok := ch.take()
	if !ok {
		return nil
	}
	return emit([]Chunk{c}, c.Last)
}

func (r *AuditReader) batch() int {
	if r.Batch <= 0 {
		return 500
	}
	return r.Batch
}

// ColdAuditDocument maps an audit row to a lossless cold-storage document: it
// keeps every column, including the id and on_behalf_of the OpenSearch
// projection omits, because the cold object outlives the hot partition.
func ColdAuditDocument(e export.Event) map[string]any {
	doc := map[string]any{
		"id":            e.ID,
		"occurred_at":   e.OccurredAt.UTC().Format(time.RFC3339Nano),
		"actor_kind":    e.ActorKind,
		"action":        e.Action,
		"resource_type": e.ResourceType,
		"outcome":       e.Outcome,
	}
	if e.ActorID != "" {
		doc["actor_id"] = e.ActorID
	}
	if e.AuthMethod != "" {
		doc["auth_method"] = e.AuthMethod
	}
	if e.ResourceID != "" {
		doc["resource_id"] = e.ResourceID
	}
	if e.WorkspaceID != "" {
		doc["workspace_id"] = e.WorkspaceID
	}
	if e.Reason != "" {
		doc["reason"] = e.Reason
	}
	if e.RequestID != "" {
		doc["request_id"] = e.RequestID
	}
	if e.TraceID != "" {
		doc["trace_id"] = e.TraceID
	}
	if e.ClientIP != "" {
		doc["client_ip"] = e.ClientIP
	}
	if e.UserAgent != "" {
		doc["user_agent"] = e.UserAgent
	}
	if e.OnBehalfOf != "" {
		doc["on_behalf_of"] = e.OnBehalfOf
	}
	if len(e.Changes) > 0 {
		// Keep the raw JSON: decoding into any would round large numbers
		// through float64 and lose precision.
		doc["changes"] = e.Changes
	}
	return doc
}
