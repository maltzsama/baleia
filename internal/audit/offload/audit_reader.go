package offload

import (
	"context"
	"encoding/json"
	"time"

	"github.com/galileostudio/baleia/internal/audit/export"
)

// AuditReader is the MonthReader of the audit stream: it reads audit_events
// through the export Source and maps each row to the OpenSearch-shaped
// document. The object is named after the stream, so the keys stay
// audit-YYYYMM.ndjson.gz.
type AuditReader struct {
	Source   export.Source
	Name     string
	Batch    int
	Document func(export.Event) map[string]any
}

// EarliestMonth is the month of the first audit row, or the zero time.
func (r *AuditReader) EarliestMonth(ctx context.Context, before time.Time) (time.Time, error) {
	events, err := r.Source.Events(ctx, export.Position{}, before, 1)
	if err != nil || len(events) == 0 {
		return time.Time{}, err
	}
	return monthStart(events[0].OccurredAt), nil
}

// ReadMonth reads the audit rows of the month.
func (r *AuditReader) ReadMonth(ctx context.Context, month, before time.Time, pos *export.Position) (map[string][]json.RawMessage, error) {
	doc := r.Document
	if doc == nil {
		doc = export.Document
	}
	name := r.Name
	if name == "" {
		name = export.StreamAudit
	}
	var rows []json.RawMessage
	limit := nextMonth(month)
	for {
		events, err := r.Source.Events(ctx, *pos, before, r.batch())
		if err != nil {
			return nil, err
		}
		if len(events) == 0 {
			return map[string][]json.RawMessage{name: rows}, nil
		}
		for _, ev := range events {
			if !ev.OccurredAt.Before(limit) {
				return map[string][]json.RawMessage{name: rows}, nil
			}
			data, err := json.Marshal(doc(ev))
			if err != nil {
				return nil, err
			}
			rows = append(rows, data)
			*pos = export.Position{OccurredAt: ev.OccurredAt, ID: ev.ID}
		}
		if len(events) < r.batch() {
			return map[string][]json.RawMessage{name: rows}, nil
		}
	}
}

func (r *AuditReader) batch() int {
	if r.Batch <= 0 {
		return 500
	}
	return r.Batch
}
