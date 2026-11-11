package offload

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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
	// (a batch, not one query per execution), within [from, until), capped at
	// limit. The second result reports truncation (more statements remain), so
	// the caller can split the page instead of buffering unbounded.
	OffloadQueryStatements(ctx context.Context, executionIDs []model.UUID, from, until time.Time, limit int) ([]model.QueryStatement, bool, error)
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

// statementPageSize caps one statement fetch for an execution page, so a single
// execution with an unreasonable number of statements cannot buffer unbounded.
var statementPageSize = ChunkRows * 8

// EarliestMonth is the month of the first execution, or the zero time.
func (r *QueryHistoryReader) EarliestMonth(ctx context.Context, before time.Time) (time.Time, error) {
	execs, err := r.Store.OffloadQueryExecutions(ctx, time.Time{}, "", before, 1)
	if err != nil || len(execs) == 0 {
		return time.Time{}, err
	}
	return monthStart(execs[0].StartedAt), nil
}

// ReadMonth streams the executions and statements of the month as bounded
// chunks. The execution chunk and the statement chunk of a flush travel
// together: the cursor commits to the execution position only after both are
// persisted, so an interrupted pass never seals an execution whose statements
// are still unwritten.
func (r *QueryHistoryReader) ReadMonth(ctx context.Context, month, before time.Time, pos *export.Position, emit func([]Chunk, export.Position) error) error {
	limit := nextMonth(month)
	pending, err := r.Store.PendingExecutions(ctx, month, limit)
	if err != nil {
		return err
	}
	if pending {
		// An execution started in the month is still queued or running, so its
		// row (and its statements) can still change. Sealing now would archive
		// a mid-run state; wait for the next run.
		return ErrNotQuiescent
	}
	readPos := *pos
	execCh := &chunker{object: ExecutionsObject}
	stmtCh := &chunker{object: StatementsObject}

	// flushPair emits both chunks in one flush, committing the cursor to the
	// execution position after both objects are written.
	flushPair := func() error {
		execChunk, execOK := execCh.take()
		stmtChunk, stmtOK := stmtCh.take()
		if !execOK && !stmtOK {
			return nil
		}
		chunks := make([]Chunk, 0, 2)
		commitTo := export.Position{}
		if execOK {
			chunks = append(chunks, execChunk)
			commitTo = execChunk.Last
		}
		if stmtOK {
			chunks = append(chunks, stmtChunk)
			if !execOK {
				commitTo = stmtChunk.Last
			}
		}
		return emit(chunks, commitTo)
	}

	for {
		execs, err := r.Store.OffloadQueryExecutions(ctx, readPos.OccurredAt, readPos.Key, before, r.batch())
		if err != nil {
			return err
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
			var processPage func([]model.QueryExecution) error
			processPage = func(execs []model.QueryExecution) error {
				ids := make([]model.UUID, len(execs))
				for i, e := range execs {
					ids[i] = e.ID
				}
				stmts, truncated, err := r.Store.OffloadQueryStatements(ctx, ids, month, limit, statementPageSize)
				if err != nil {
					return err
				}
				if truncated {
					if len(execs) <= 1 {
						return fmt.Errorf("query_history: one execution has more than %d statements", statementPageSize)
					}
					// Split the overflowing page so a large page keeps making
					// progress instead of failing on every pass.
					mid := len(execs) / 2
					if err := processPage(execs[:mid]); err != nil {
						return err
					}
					return processPage(execs[mid:])
				}
				byExec := make(map[model.UUID][]model.QueryStatement, len(execs))
				for _, s := range stmts {
					byExec[s.ExecutionID] = append(byExec[s.ExecutionID], s)
				}
				for _, e := range execs {
					ed, err := json.Marshal(toExecutionDoc(e))
					if err != nil {
						return err
					}
					execCh.add(executionPos(e), ed)
					for _, s := range byExec[e.ID] {
						sd, err := json.Marshal(toStatementDoc(s))
						if err != nil {
							return err
						}
						stmtCh.add(statementPos(s), sd)
					}
					if execCh.full() || stmtCh.full() {
						if err := flushPair(); err != nil {
							return err
						}
					}
				}
				return nil
			}
			if err := processPage(page); err != nil {
				return err
			}
			readPos = executionPos(page[len(page)-1])
		}
		if done || len(execs) < r.batch() {
			break
		}
	}
	return flushPair()
}

// executionPos is the cursor position of one execution (started_at, id uuid).
func executionPos(e model.QueryExecution) export.Position {
	return export.Position{OccurredAt: e.StartedAt, Key: string(e.ID)}
}

// statementPos pins one statement's chunk identity: started_at plus the
// (execution, position) tie, so two chunks of the same execution never collide.
func statementPos(s model.QueryStatement) export.Position {
	return export.Position{OccurredAt: s.ExecutionStartedAt, Key: string(s.ExecutionID) + ":" + strconv.Itoa(int(s.Position))}
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
