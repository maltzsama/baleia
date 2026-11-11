package offload

import (
	"context"
	"encoding/json"
	"time"

	"github.com/galileostudio/baleia/internal/audit/export"
	"github.com/galileostudio/baleia/internal/store/model"
)

// QueryHistoryStore is the store surface the query-history offload needs. The
// cursor is ordered by (started_at, id uuid), so the tiebreaker is a string
// (export.Position.Key), not the audit's bigint id.
type QueryHistoryStore interface {
	// OffloadQueryExecutions returns up to limit executions with
	// (started_at, id) strictly after (afterAt, afterID) and started_at before
	// before, ordered by (started_at, id).
	OffloadQueryExecutions(ctx context.Context, afterAt time.Time, afterID string, before time.Time, limit int) ([]model.QueryExecution, error)
	// OffloadQueryStatements returns the statements of a page of executions
	// (a batch, not one query per execution), within [from, until).
	OffloadQueryStatements(ctx context.Context, executionIDs []model.UUID, from, until time.Time) ([]model.QueryStatement, error)
	// PendingExecutions reports whether any execution in [from, until) is not
	// terminal (queued or running), so the month cannot be sealed yet.
	PendingExecutions(ctx context.Context, from, until time.Time) (bool, error)
}

// QueryHistoryReader is the MonthReader of the query_history stream: it reads
// executions and their statements in started_at order and writes the pair as
// two objects, query_executions- and query_statements- (ADR 0024: statements
// are never offloaded without their parent execution, and the month's manifest
// records the pair).
type QueryHistoryReader struct {
	Store QueryHistoryStore
	Batch int
}

// Object names of the query-history stream.
const (
	ExecutionsObject = "query_executions"
	StatementsObject = "query_statements"
)

// QueryHistorySafetyLag delays sealing a query-history month until any
// execution started in it has finished (the execution timeout is two hours),
// so the archived pair is final before retention can drop the partition.
const QueryHistorySafetyLag = 24 * time.Hour

// EarliestMonth is the month of the first execution, or the zero time.
func (r *QueryHistoryReader) EarliestMonth(ctx context.Context, before time.Time) (time.Time, error) {
	execs, err := r.Store.OffloadQueryExecutions(ctx, time.Time{}, "", before, 1)
	if err != nil || len(execs) == 0 {
		return time.Time{}, err
	}
	return monthStart(execs[0].StartedAt), nil
}

// ReadMonth reads the executions and statements of the month. It reads one
// page of executions and fetches that page's statements in a single query, so
// the statement reads never become one round trip per execution.
func (r *QueryHistoryReader) ReadMonth(ctx context.Context, month, before time.Time, pos *export.Position) (map[string][]json.RawMessage, error) {
	limit := nextMonth(month)
	pending, err := r.Store.PendingExecutions(ctx, month, limit)
	if err != nil {
		return nil, err
	}
	if pending {
		// An execution started in the month is still queued or running, so its
		// row (and its statements) can still change. Sealing now would archive
		// a mid-run state; wait for the next run.
		return nil, ErrNotQuiescent
	}
	var execRows, stmtRows []json.RawMessage
	for {
		execs, err := r.Store.OffloadQueryExecutions(ctx, pos.OccurredAt, pos.Key, before, r.batch())
		if err != nil {
			return nil, err
		}
		if len(execs) == 0 {
			break
		}
		page := make([]model.QueryExecution, 0, len(execs))
		done := false
		for _, e := range execs {
			if !e.StartedAt.Before(limit) {
				done = true
				break
			}
			page = append(page, e)
		}
		if len(page) > 0 {
			ids := make([]model.UUID, len(page))
			for i, e := range page {
				ids[i] = e.ID
			}
			stmts, err := r.Store.OffloadQueryStatements(ctx, ids, month, limit)
			if err != nil {
				return nil, err
			}
			byExec := make(map[model.UUID][]model.QueryStatement, len(page))
			for _, s := range stmts {
				byExec[s.ExecutionID] = append(byExec[s.ExecutionID], s)
			}
			for _, e := range page {
				ed, err := json.Marshal(toExecutionDoc(e))
				if err != nil {
					return nil, err
				}
				execRows = append(execRows, ed)
				for _, s := range byExec[e.ID] {
					sd, err := json.Marshal(toStatementDoc(s))
					if err != nil {
						return nil, err
					}
					stmtRows = append(stmtRows, sd)
				}
			}
			last := page[len(page)-1]
			*pos = export.Position{OccurredAt: last.StartedAt, Key: string(last.ID)}
		}
		if done || len(execs) < r.batch() {
			break
		}
	}
	return map[string][]json.RawMessage{
		ExecutionsObject: execRows,
		StatementsObject: stmtRows,
	}, nil
}

func (r *QueryHistoryReader) batch() int {
	if r.Batch <= 0 {
		return 500
	}
	return r.Batch
}

// executionDoc is one query_executions row as written to the cold store.
type executionDoc struct {
	ID              string          `json:"id"`
	WorkspaceID     string          `json:"workspace_id"`
	UserID          string          `json:"user_id"`
	SavedQueryID    *string         `json:"saved_query_id,omitempty"`
	Status          string          `json:"status"`
	Origin          string          `json:"origin"`
	BIKeyID         *string         `json:"bi_key_id,omitempty"`
	SQL             string          `json:"sql"`
	EngineQueryID   *string         `json:"engine_query_id,omitempty"`
	RowsReturned    *int64          `json:"rows_returned,omitempty"`
	DurationMs      *int64          `json:"duration_ms,omitempty"`
	StartedAt       time.Time       `json:"started_at"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
	TrinoCatalog    *string         `json:"trino_catalog,omitempty"`
	ResultKey       *string         `json:"result_key,omitempty"`
	RequestID       *string         `json:"request_id,omitempty"`
	EngineID        *string         `json:"engine_id,omitempty"`
	TargetBindingID *string         `json:"target_binding_id,omitempty"`
	TargetCatalogID *string         `json:"target_catalog_id,omitempty"`
	TargetSchema    *string         `json:"target_schema,omitempty"`
	TargetSnapshot  json.RawMessage `json:"target_snapshot,omitempty"`
	AgentID         *string         `json:"agent_id,omitempty"`
	QueueClass      *string         `json:"queue_class,omitempty"`
	QueueReason     *string         `json:"queue_reason,omitempty"`
	FairnessKey     *string         `json:"fairness_key,omitempty"`
	QueuedAt        *time.Time      `json:"queued_at,omitempty"`
	AdmittedAt      *time.Time      `json:"admitted_at,omitempty"`
	QueueWaitMs     *int64          `json:"queue_wait_ms,omitempty"`
	ErrorCode       *string         `json:"error_code,omitempty"`
}

func toExecutionDoc(e model.QueryExecution) executionDoc {
	return executionDoc{
		ID:              string(e.ID),
		WorkspaceID:     string(e.WorkspaceID),
		UserID:          string(e.UserID),
		SavedQueryID:    uuidPtr(e.SavedQueryID),
		Status:          string(e.Status),
		Origin:          string(e.Origin),
		BIKeyID:         uuidPtr(e.BIKeyID),
		SQL:             string(e.SQL),
		EngineQueryID:   e.EngineQueryID,
		RowsReturned:    e.RowsReturned,
		DurationMs:      e.DurationMs,
		StartedAt:       e.StartedAt,
		FinishedAt:      e.FinishedAt,
		TrinoCatalog:    e.TrinoCatalog,
		ResultKey:       e.ResultKey,
		RequestID:       e.RequestID,
		EngineID:        uuidPtr(e.EngineID),
		TargetBindingID: uuidPtr(e.TargetBindingID),
		TargetCatalogID: uuidPtr(e.TargetCatalogID),
		TargetSchema:    e.TargetSchema,
		TargetSnapshot:  jsonPtr(e.TargetSnapshot),
		AgentID:         uuidPtr(e.AgentID),
		QueueClass:      e.QueueClass,
		QueueReason:     e.QueueReason,
		FairnessKey:     e.FairnessKey,
		QueuedAt:        e.QueuedAt,
		AdmittedAt:      e.AdmittedAt,
		QueueWaitMs:     e.QueueWaitMs,
		ErrorCode:       e.ErrorCode,
	}
}

// statementDoc is one query_statements row as written to the cold store.
type statementDoc struct {
	ID                 string          `json:"id"`
	ExecutionID        string          `json:"execution_id"`
	ExecutionStartedAt time.Time       `json:"execution_started_at"`
	Position           int32           `json:"position"`
	SQL                string          `json:"sql"`
	Status             string          `json:"status"`
	EngineQueryID      *string         `json:"engine_query_id,omitempty"`
	RowsReturned       *int64          `json:"rows_returned,omitempty"`
	DurationMs         *int64          `json:"duration_ms,omitempty"`
	ErrorMessage       *string         `json:"error_message,omitempty"`
	StartedAt          time.Time       `json:"started_at"`
	FinishedAt         *time.Time      `json:"finished_at,omitempty"`
	ResultKey          *string         `json:"result_key,omitempty"`
	ResultBytes        *int64          `json:"result_bytes,omitempty"`
	Truncated          bool            `json:"truncated"`
	QueryHandle        json.RawMessage `json:"query_handle,omitempty"`
	ResultSets         json.RawMessage `json:"result_sets,omitempty"`
	ErrorCode          *string         `json:"error_code,omitempty"`
}

func toStatementDoc(s model.QueryStatement) statementDoc {
	return statementDoc{
		ID:                 string(s.ID),
		ExecutionID:        string(s.ExecutionID),
		ExecutionStartedAt: s.ExecutionStartedAt,
		Position:           s.Position,
		SQL:                s.SQL,
		Status:             s.Status,
		EngineQueryID:      s.EngineQueryID,
		RowsReturned:       s.RowsReturned,
		DurationMs:         s.DurationMs,
		ErrorMessage:       s.ErrorMessage,
		StartedAt:          s.StartedAt,
		FinishedAt:         s.FinishedAt,
		ResultKey:          s.ResultKey,
		ResultBytes:        s.ResultBytes,
		Truncated:          s.Truncated,
		QueryHandle:        jsonPtr(s.QueryHandle),
		ResultSets:         json.RawMessage(s.ResultSets),
		ErrorCode:          s.ErrorCode,
	}
}

func uuidPtr(u *model.UUID) *string {
	if u == nil {
		return nil
	}
	s := string(*u)
	return &s
}

func jsonPtr(j *model.JSON) json.RawMessage {
	if j == nil {
		return nil
	}
	return json.RawMessage(*j)
}
